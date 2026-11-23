package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/draw"
	"os"
	"os/exec"

	"github.com/rs/zerolog/log"
	"golang.org/x/image/webp"
)

const incomingStickerFPS = 20

type animWebPFrame struct {
	img                 image.Image
	x, y                int
	durationMs          int
	disposeToBackground bool
	noBlend             bool
}

// ffmpeg 5.1's webp decoder skips ANIM/ANMF, so frames are decoded here and
// only the encode step goes through ffmpeg.
func convertAnimatedStickerToMP4(input []byte) ([]byte, error) {
	if len(input) == 0 {
		return nil, fmt.Errorf("empty sticker")
	}
	width, height, bg, frames, err := decodeAnimatedWebP(input)
	if err != nil {
		return nil, err
	}
	return encodeAnimatedFramesToMP4(width, height, bg, frames)
}

func decodeAnimatedWebP(data []byte) (int, int, colorNRGBA, []animWebPFrame, error) {
	if !isValidWebP(data) {
		return 0, 0, colorNRGBA{}, nil, fmt.Errorf("not a webp sticker")
	}

	chunks, _, err := parseWebPChunks(data)
	if err != nil {
		return 0, 0, colorNRGBA{}, nil, err
	}

	width, height := 0, 0
	bg := colorNRGBA{}
	var frames []animWebPFrame

	for _, chunk := range chunks {
		if len(chunk) < chunkHeaderSize {
			continue
		}
		tag := string(chunk[0:4])
		size := int(binary.LittleEndian.Uint32(chunk[4:8]))
		if chunkHeaderSize+size > len(chunk) {
			return 0, 0, colorNRGBA{}, nil, fmt.Errorf("truncated webp chunk: %s", tag)
		}
		payload := chunk[chunkHeaderSize : chunkHeaderSize+size]

		switch tag {
		case "VP8X":
			if len(payload) < vp8xPayloadSize {
				continue
			}
			width = readLE24(payload[4:7]) + 1
			height = readLE24(payload[7:10]) + 1
		case "ANIM":
			if len(payload) >= 4 {
				bg = colorNRGBA{R: payload[2], G: payload[1], B: payload[0], A: payload[3]}
			}
		case "ANMF":
			frame, err := decodeANMF(payload)
			if err != nil {
				return 0, 0, colorNRGBA{}, nil, err
			}
			frames = append(frames, frame)
		}
	}

	if len(frames) == 0 {
		return 0, 0, colorNRGBA{}, nil, fmt.Errorf("animated webp has no frames")
	}
	if width <= 0 || height <= 0 {
		b := frames[0].img.Bounds()
		width, height = b.Dx(), b.Dy()
	}
	return width, height, bg, frames, nil
}

type colorNRGBA struct {
	R, G, B, A uint8
}

func decodeANMF(payload []byte) (animWebPFrame, error) {
	if len(payload) < 16 {
		return animWebPFrame{}, fmt.Errorf("short animated webp frame")
	}
	frame := animWebPFrame{
		x:                   readLE24(payload[0:3]) * 2,
		y:                   readLE24(payload[3:6]) * 2,
		durationMs:          readLE24(payload[12:15]),
		disposeToBackground: payload[15]&0x01 != 0,
		noBlend:             payload[15]&0x02 != 0,
	}
	if frame.durationMs <= 0 {
		frame.durationMs = 100
	}
	width := readLE24(payload[6:9]) + 1
	height := readLE24(payload[9:12]) + 1

	bitstream := payload[16:]
	if len(bitstream) >= 4 && string(bitstream[0:4]) == "ALPH" {
		bitstream = append(vp8xAlphaChunk(width, height), bitstream...)
	}
	img, err := webp.Decode(bytes.NewReader(wrapWebPChunks(bitstream)))
	if err != nil {
		return animWebPFrame{}, fmt.Errorf("decode animated webp frame: %w", err)
	}
	frame.img = img
	return frame, nil
}

func vp8xAlphaChunk(width, height int) []byte {
	chunk := make([]byte, chunkHeaderSize+vp8xPayloadSize)
	copy(chunk[0:4], "VP8X")
	binary.LittleEndian.PutUint32(chunk[4:8], vp8xPayloadSize)
	chunk[vp8xFlagsOffset] = 0x10
	putUint24LE(chunk[vp8xWidthOffset:], width-1)
	putUint24LE(chunk[vp8xHeightOffset:], height-1)
	return chunk
}

func wrapWebPChunks(chunks []byte) []byte {
	out := make([]byte, riffHeaderSize+len(chunks))
	copy(out[0:4], "RIFF")
	binary.LittleEndian.PutUint32(out[4:8], uint32(4+len(chunks)))
	copy(out[8:12], "WEBP")
	copy(out[12:], chunks)
	return out
}

func readLE24(b []byte) int {
	return int(b[0]) | int(b[1])<<8 | int(b[2])<<16
}

func encodeAnimatedFramesToMP4(width, height int, bg colorNRGBA, frames []animWebPFrame) ([]byte, error) {
	canvas := image.NewNRGBA(image.Rect(0, 0, width, height))
	fillNRGBA(canvas, bg)

	outFile, err := os.CreateTemp("", "sticker-output-*.mp4")
	if err != nil {
		return nil, err
	}
	outPath := outFile.Name()
	outFile.Close()
	defer os.Remove(outPath)

	cmd := exec.Command(
		"ffmpeg",
		"-y",
		"-f", "rawvideo",
		"-pix_fmt", "rgba",
		"-s", fmt.Sprintf("%dx%d", width, height),
		"-r", fmt.Sprintf("%d", incomingStickerFPS),
		"-i", "pipe:0",
		"-an",
		"-vf", "format=yuv420p,scale=trunc(iw/2)*2:trunc(ih/2)*2",
		"-c:v", "libx264",
		"-pix_fmt", "yuv420p",
		"-preset", "veryfast",
		"-crf", "28",
		"-movflags", "+faststart",
		outPath,
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	writeErr := writeCompositedFrames(stdin, canvas, frames)
	closeErr := stdin.Close()
	waitErr := cmd.Wait()
	if writeErr != nil {
		return nil, writeErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if waitErr != nil {
		log.Error().Err(waitErr).Str("stderr", stderr.String()).Msg("ffmpeg failed converting animated sticker to mp4")
		return nil, waitErr
	}
	return os.ReadFile(outPath)
}

func writeCompositedFrames(stdin interface{ Write([]byte) (int, error) }, canvas *image.NRGBA, frames []animWebPFrame) error {
	var prevRect image.Rectangle
	prevDispose := false
	rowBytes := canvas.Bounds().Dx() * 4

	for _, frame := range frames {
		if prevDispose {
			clearNRGBARect(canvas, prevRect)
		}
		dst := image.Rect(frame.x, frame.y, frame.x+frame.img.Bounds().Dx(), frame.y+frame.img.Bounds().Dy())
		op := draw.Over
		if frame.noBlend {
			op = draw.Src
		}
		draw.Draw(canvas, dst, frame.img, frame.img.Bounds().Min, op)
		prevRect = dst
		prevDispose = frame.disposeToBackground

		repeats := frame.durationMs * incomingStickerFPS / 1000
		if repeats < 1 {
			repeats = 1
		}
		for i := 0; i < repeats; i++ {
			for y := 0; y < canvas.Bounds().Dy(); y++ {
				start := y * canvas.Stride
				if _, err := stdin.Write(canvas.Pix[start : start+rowBytes]); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func fillNRGBA(img *image.NRGBA, c colorNRGBA) {
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i] = c.R
		img.Pix[i+1] = c.G
		img.Pix[i+2] = c.B
		img.Pix[i+3] = c.A
	}
}

func clearNRGBARect(img *image.NRGBA, rect image.Rectangle) {
	rect = rect.Intersect(img.Bounds())
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			i := img.PixOffset(x, y)
			img.Pix[i] = 0
			img.Pix[i+1] = 0
			img.Pix[i+2] = 0
			img.Pix[i+3] = 0
		}
	}
}
