package main

import (
	"errors"
	"testing"
)

func TestIsProxyDialError(t *testing.T) {
	proxyTimeout := errors.New(`failed to dial whatsapp web websocket: failed to WebSocket dial: failed to send handshake request: Get "https://web.whatsapp.com/ws/chat": socks connect tcp 144.225.2.147:12324->web.whatsapp.com:443: dial tcp 144.225.2.147:12324: connect: connection timed out`)
	httpProxyRefused := errors.New(`Get "https://web.whatsapp.com/ws/chat": proxyconnect tcp: dial tcp 10.0.0.1:8080: connect: connection refused`)
	whatsappDown := errors.New(`failed to dial whatsapp web websocket: dial tcp 157.240.0.1:443: connect: connection timed out`)
	authFailure := errors.New(`noise handshake failed: authentication failed`)

	if !isProxyDialError(proxyTimeout) {
		t.Fatal("expected SOCKS proxy timeout to be treated as a proxy dial error")
	}
	if !isProxyDialError(httpProxyRefused) {
		t.Fatal("expected HTTP proxy refusal to be treated as a proxy dial error")
	}
	if isProxyDialError(whatsappDown) {
		t.Fatal("direct WhatsApp timeout must not be treated as a proxy dial error")
	}
	if isProxyDialError(authFailure) {
		t.Fatal("handshake failure must not be treated as a proxy dial error")
	}
	if isProxyDialError(nil) {
		t.Fatal("nil error must not be treated as a proxy dial error")
	}
}
