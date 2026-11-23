package main

import (
	"encoding/binary"
	"net/http"
	"os/exec"
	"testing"
)

func riffWebP(chunks ...[]byte) []byte {
	payload := []byte("WEBP")
	for _, chunk := range chunks {
		payload = append(payload, chunk...)
	}
	out := []byte("RIFF")
	size := make([]byte, 4)
	binary.LittleEndian.PutUint32(size, uint32(len(payload)))
	out = append(out, size...)
	return append(out, payload...)
}

func webpChunk(tag string, data []byte) []byte {
	chunk := []byte(tag)
	size := make([]byte, 4)
	binary.LittleEndian.PutUint32(size, uint32(len(data)))
	chunk = append(chunk, size...)
	return append(chunk, data...)
}

func TestWebPIsAnimated(t *testing.T) {
	still := riffWebP(webpChunk("VP8 ", []byte{0, 1, 2, 3}))
	if webpIsAnimated(still) {
		t.Fatal("still webp detected as animated")
	}

	flags := make([]byte, 10)
	flags[0] = vp8xFlagAnimation
	animated := riffWebP(webpChunk("VP8X", flags), webpChunk("ANIM", []byte{0, 0, 0, 0}))
	if !webpIsAnimated(animated) {
		t.Fatal("animated webp not detected")
	}
}

func TestConvertGIFStickerKeepsAnimation(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}

	gif, err := exec.Command(
		"ffmpeg", "-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=size=64x64:rate=8:duration=1",
		"-f", "gif", "pipe:1",
	).Output()
	if err != nil || len(gif) == 0 {
		t.Fatalf("failed to build sample gif: %v", err)
	}

	out, err := convertVideoStickerToWebP(gif, ".gif")
	if err != nil {
		t.Fatal(err)
	}
	if !webpIsAnimated(out) {
		t.Fatal("gif sticker was flattened to a still image")
	}
	width, height, ok := webpCanvasSize(out)
	if !ok || width != 512 || height != 512 {
		t.Fatalf("canvas = %dx%d ok=%v", width, height, ok)
	}
}

func TestIncomingAnimatedStickerBecomesMP4(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}

	gif, err := exec.Command(
		"ffmpeg", "-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=size=64x64:rate=8:duration=1",
		"-f", "gif", "pipe:1",
	).Output()
	if err != nil || len(gif) == 0 {
		t.Fatalf("failed to build sample gif: %v", err)
	}

	webp, err := convertVideoStickerToWebP(gif, ".gif")
	if err != nil {
		t.Fatal(err)
	}

	data, mimeType, ext := presentIncomingSticker(webp, "image/webp", ".webp", true)
	if mimeType != "video/mp4" || ext != ".mp4" {
		t.Fatalf("animated sticker delivered as %s %s", mimeType, ext)
	}
	if http.DetectContentType(data) != "video/mp4" {
		t.Fatalf("payload detected as %s", http.DetectContentType(data))
	}

	still, mimeType, ext := presentIncomingSticker([]byte("RIFF\x00\x00\x00\x00WEBP"), "image/webp", ".webp", false)
	if mimeType != "image/webp" || ext != ".webp" || string(still) == "" {
		t.Fatalf("static sticker changed to %s %s", mimeType, ext)
	}
}
