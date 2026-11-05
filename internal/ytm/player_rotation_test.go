package ytm

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestResolveStreamURLRotatesPlayerClients(t *testing.T) {
	var clients []string

	svc := NewPlayerServiceWithClient(fakeClient(t, func(t *testing.T, r *http.Request) *http.Response {
		var payload RequestPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		name := payload.Context.Client.ClientName
		clients = append(clients, name)

		if name == androidMusicClient {
			return fakeJSONResponse(t, map[string]any{
				"playabilityStatus": map[string]any{
					"status": "LOGIN_REQUIRED",
					"reason": "Please sign in",
				},
			})
		}

		return fakeJSONResponse(t, map[string]any{
			"playabilityStatus": map[string]any{"status": "OK"},
			"streamingData": map[string]any{
				"adaptiveFormats": []any{
					map[string]any{"itag": 251, "url": "https://cdn.example/audio.webm", "mimeType": "audio/webm"},
				},
			},
		})
	}))

	url, err := svc.ResolveStreamURL(context.Background(), "rotate1")
	if err != nil {
		t.Fatalf("ResolveStreamURL: %v", err)
	}
	if url != "https://cdn.example/audio.webm" {
		t.Fatalf("url = %q", url)
	}
	if len(clients) < 2 {
		t.Fatalf("expected at least two client attempts, got %v", clients)
	}
	if clients[0] != androidMusicClient {
		t.Errorf("first client = %q, want %q", clients[0], androidMusicClient)
	}
	if clients[1] != "TVHTML5" {
		t.Errorf("second client = %q, want TVHTML5", clients[1])
	}
}

// A stale or rejected imported session must not make playback worse than a
// guest: when every authenticated attempt fails, the rotation is retried
// without credentials.
func TestResolveStreamURLRetriesAnonymouslyWhenSessionFails(t *testing.T) {
	var authAttempts, guestAttempts int

	client := fakeClient(t, func(t *testing.T, r *http.Request) *http.Response {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			authAttempts++
			return fakeJSONResponse(t, map[string]any{
				"playabilityStatus": map[string]any{
					"status": "LOGIN_REQUIRED",
					"reason": "Please sign in",
				},
			})
		}

		guestAttempts++
		if got := r.Header.Get("X-Goog-Visitor-Id"); got == "session-visitor" {
			t.Errorf("guest retry leaked session visitor data %q", got)
		}
		return fakeJSONResponse(t, map[string]any{
			"playabilityStatus": map[string]any{"status": "OK"},
			"streamingData": map[string]any{
				"adaptiveFormats": []any{
					map[string]any{"itag": 251, "url": "https://cdn.example/guest.webm", "mimeType": "audio/webm"},
				},
			},
		})
	})
	client.SetAuth(&TokenBundle{
		Cookie:      "SAPISID=deadbeef",
		SAPISID:     "deadbeef",
		VisitorData: "session-visitor",
	})

	svc := NewPlayerServiceWithClient(client)
	url, err := svc.ResolveStreamURL(context.Background(), "guest-retry")
	if err != nil {
		t.Fatalf("ResolveStreamURL: %v", err)
	}
	if url != "https://cdn.example/guest.webm" {
		t.Fatalf("url = %q", url)
	}
	if authAttempts != len(playableClients) {
		t.Errorf("authenticated attempts = %d, want %d", authAttempts, len(playableClients))
	}
	if guestAttempts == 0 {
		t.Error("expected an anonymous retry pass")
	}
}
