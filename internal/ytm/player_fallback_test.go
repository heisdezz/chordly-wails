package ytm

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAuthorizationHeaderIncludesAllSIDHashes(t *testing.T) {
	bundle := &TokenBundle{
		SAPISID: "sid",
		Cookies: map[string]string{
			"SAPISID":           "sid",
			"__Secure-1PAPISID": "sid1p",
			"__Secure-3PAPISID": "sid3p",
		},
	}

	got := authorizationHeader(bundle, 1700000000, ytmOrigin)
	for _, scheme := range []string{"SAPISIDHASH", "SAPISID1PHASH", "SAPISID3PHASH"} {
		if !strings.Contains(got, scheme+" 1700000000_") {
			t.Errorf("authorization header missing %s: %q", scheme, got)
		}
	}
}

func TestShouldFallbackToYTDLP(t *testing.T) {
	if !shouldFallbackToYTDLP(NewPlayabilityError("LOGIN_REQUIRED", "Please sign in")) {
		t.Error("playability errors should trigger the extractor fallback")
	}
	if !shouldFallbackToYTDLP(errors.New("endpoint player failed with status 400: bad request")) {
		t.Error("HTTP 400 should trigger the extractor fallback")
	}
	if shouldFallbackToYTDLP(nil) {
		t.Error("nil error should not trigger fallback")
	}
	if shouldFallbackToYTDLP(errors.New("empty videoID")) {
		t.Error("unrelated errors should not trigger fallback")
	}
}

func TestResolveStreamURLUsesYTDLPFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fallback fixture is POSIX-only")
	}

	script := filepath.Join(t.TempDir(), "yt-dlp")
	body := "#!/bin/sh\necho 'https://fallback.example/audio.webm?mime=audio%2Fwebm'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake yt-dlp: %v", err)
	}

	svc := NewPlayerServiceWithClient(fakeClient(t, func(t *testing.T, r *http.Request) *http.Response {
		return fakeJSONResponse(t, map[string]any{
			"playabilityStatus": map[string]any{
				"status": "LOGIN_REQUIRED",
				"reason": "Please sign in",
			},
		})
	}))
	svc.ytDlpPath = script

	url, err := svc.ResolveStreamURL(context.Background(), "gated")
	if err != nil {
		t.Fatalf("ResolveStreamURL fallback: %v", err)
	}
	if url != "https://fallback.example/audio.webm?mime=audio%2Fwebm" {
		t.Fatalf("fallback url = %q", url)
	}

	svc.mu.RLock()
	entry := svc.cache["gated"]
	svc.mu.RUnlock()
	if entry == nil || entry.MimeType != "audio/webm" {
		t.Fatalf("fallback cache entry = %+v", entry)
	}
}
