package main

import "testing"

func TestExtractAuthURL(t *testing.T) {
	text := "Open this URL:\nhttps://auth.openai.com/oauth/authorize?response_type=code&state=test-state&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback"
	authURL, state := extractAuthURL(text)
	if authURL == "" {
		t.Fatal("expected auth url to be extracted")
	}
	if state != "test-state" {
		t.Fatalf("expected state test-state, got %q", state)
	}
}

func TestExtractAuthURLSupportsAlternateHost(t *testing.T) {
	text := "If your browser did not open, navigate to this URL to authenticate:\nhttps://chatgpt.com/oauth/authorize?response_type=code&state=abc123&code_challenge=xyz"
	authURL, state := extractAuthURL(text)
	if authURL == "" {
		t.Fatal("expected auth url on alternate host to be extracted")
	}
	if state != "abc123" {
		t.Fatalf("expected state abc123, got %q", state)
	}
}

func TestExtractCallbackURL(t *testing.T) {
	text := "Starting local login server on http://localhost:1455."
	callback := extractCallbackURL(text)
	if callback != "http://localhost:1455/auth/callback" {
		t.Fatalf("unexpected callback url %q", callback)
	}
}
