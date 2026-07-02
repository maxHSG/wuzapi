package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/rs/zerolog/log"
	"go.mau.fi/whatsmeow/types"
)

// Gets the current passkey pairing state (request options or confirmation code).
func (s *server) GetPasskey() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		txtid := r.Context().Value("userinfo").(Values).Get("Id")

		if clientManager.GetWhatsmeowClient(txtid) == nil {
			s.Respond(w, r, http.StatusInternalServerError, errors.New("no session"))
			return
		}
		if !clientManager.GetWhatsmeowClient(txtid).IsConnected() {
			s.Respond(w, r, http.StatusInternalServerError, errors.New("not connected"))
			return
		}
		if clientManager.GetWhatsmeowClient(txtid).IsLoggedIn() {
			s.Respond(w, r, http.StatusBadRequest, errors.New("already logged in"))
			return
		}

		state := clientManager.GetPasskeyState(txtid)
		response := map[string]interface{}{
			"status": state.Status,
		}
		switch state.Status {
		case PasskeyStatusRequest:
			response["publicKey"] = state.Request
		case PasskeyStatusConfirmation:
			response["code"] = state.ConfirmationCode
			response["skipHandoffUX"] = state.SkipHandoffUX
		case PasskeyStatusError:
			response["error"] = state.Error
		}

		responseJSON, err := json.Marshal(response)
		if err != nil {
			s.Respond(w, r, http.StatusInternalServerError, err)
			return
		}
		s.Respond(w, r, http.StatusOK, string(responseJSON))
	}
}

// Sends a WebAuthn passkey response to complete the pairing challenge.
func (s *server) SendPasskeyResponse() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		txtid := r.Context().Value("userinfo").(Values).Get("Id")

		client := clientManager.GetWhatsmeowClient(txtid)
		if client == nil {
			s.Respond(w, r, http.StatusInternalServerError, errors.New("no session"))
			return
		}
		if !client.IsConnected() {
			s.Respond(w, r, http.StatusInternalServerError, errors.New("not connected"))
			return
		}
		if client.IsLoggedIn() {
			s.Respond(w, r, http.StatusBadRequest, errors.New("already logged in"))
			return
		}

		state := clientManager.GetPasskeyState(txtid)
		if state.Status != PasskeyStatusRequest {
			s.Respond(w, r, http.StatusBadRequest, errors.New("no pending passkey request"))
			return
		}

		var passkeyResp types.WebAuthnResponse
		if err := json.NewDecoder(r.Body).Decode(&passkeyResp); err != nil {
			s.Respond(w, r, http.StatusBadRequest, errors.New("could not decode passkey response payload"))
			return
		}

		if err := client.SendPasskeyResponse(context.Background(), &passkeyResp); err != nil {
			log.Error().Err(err).Msg("Failed to send passkey response")
			clientManager.SetPasskeyError(txtid, err)
			s.Respond(w, r, http.StatusInternalServerError, err)
			return
		}

		clientManager.ClearPasskeyState(txtid)

		response := map[string]interface{}{"details": "Passkey response sent"}
		responseJSON, err := json.Marshal(response)
		if err != nil {
			s.Respond(w, r, http.StatusInternalServerError, err)
			return
		}
		s.Respond(w, r, http.StatusOK, string(responseJSON))
	}
}

// Confirms the passkey pairing code shown on the phone.
func (s *server) SendPasskeyConfirmation() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		txtid := r.Context().Value("userinfo").(Values).Get("Id")

		client := clientManager.GetWhatsmeowClient(txtid)
		if client == nil {
			s.Respond(w, r, http.StatusInternalServerError, errors.New("no session"))
			return
		}
		if !client.IsConnected() {
			s.Respond(w, r, http.StatusInternalServerError, errors.New("not connected"))
			return
		}
		if client.IsLoggedIn() {
			s.Respond(w, r, http.StatusBadRequest, errors.New("already logged in"))
			return
		}

		state := clientManager.GetPasskeyState(txtid)
		if state.Status != PasskeyStatusConfirmation {
			s.Respond(w, r, http.StatusBadRequest, errors.New("no pending passkey confirmation"))
			return
		}

		if err := client.SendPasskeyConfirmation(context.Background()); err != nil {
			log.Error().Err(err).Msg("Failed to send passkey confirmation")
			clientManager.SetPasskeyError(txtid, err)
			s.Respond(w, r, http.StatusInternalServerError, err)
			return
		}

		clientManager.ClearPasskeyState(txtid)

		response := map[string]interface{}{"details": "Passkey confirmation sent"}
		responseJSON, err := json.Marshal(response)
		if err != nil {
			s.Respond(w, r, http.StatusInternalServerError, err)
			return
		}
		s.Respond(w, r, http.StatusOK, string(responseJSON))
	}
}
