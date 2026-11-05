package ytm

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func fixedClock() func() time.Time {
	return func() time.Time { return time.Unix(1700000000, 0) }
}

func TestClientSetAuthInjectsHeaders(t *testing.T) {
	const cookie = "SAPISID=abc123; __Secure-3PSID=session"

	c := fakeClient(t, func(t *testing.T, r *http.Request) *http.Response {
		t.Helper()
		if got := r.Header.Get("Cookie"); got != cookie {
			t.Errorf("Cookie = %q, want %q", got, cookie)
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "SAPISIDHASH 1700000000_") {
			t.Errorf("Authorization = %q, want SAPISIDHASH prefix", auth)
		}
		if got := r.Header.Get("X-Goog-AuthUser"); got != "0" {
			t.Errorf("X-Goog-AuthUser = %q, want 0", got)
		}
		if got := r.Header.Get("Origin"); got != ytmOrigin {
			t.Errorf("Origin = %q, want %q", got, ytmOrigin)
		}
		if got := r.Header.Get("X-Origin"); got != ytmOrigin {
			t.Errorf("X-Origin = %q, want %q", got, ytmOrigin)
		}
		return fakeJSONResponse(t, map[string]any{})
	})
	c.now = fixedClock()
	c.SetAuth(&TokenBundle{Cookie: cookie, SAPISID: "abc123"})

	if err := c.Do(context.Background(), "browse", RequestPayload{}, nil); err != nil {
		t.Fatalf("Do with auth failed: %v", err)
	}
}

func TestClientGuestSendsNoAuthHeaders(t *testing.T) {
	c := fakeClient(t, func(t *testing.T, r *http.Request) *http.Response {
		t.Helper()
		if got := r.Header.Get("Cookie"); got != "" {
			t.Errorf("guest Cookie = %q, want empty", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("guest Authorization = %q, want empty", got)
		}
		return fakeJSONResponse(t, map[string]any{})
	})

	if err := c.Do(context.Background(), "browse", RequestPayload{}, nil); err != nil {
		t.Fatalf("guest Do failed: %v", err)
	}
}

func TestPlayerEndpointUsesAuthHeaders(t *testing.T) {
	const cookie = "SAPISID=abc123"

	c := fakeClient(t, func(t *testing.T, r *http.Request) *http.Response {
		t.Helper()
		if got := r.Header.Get("Cookie"); got != cookie {
			t.Errorf("player Cookie = %q, want %q", got, cookie)
		}
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "SAPISIDHASH ") {
			t.Errorf("player Authorization = %q, want SAPISIDHASH", got)
		}
		return fakeJSONResponse(t, map[string]any{
			"playabilityStatus": map[string]string{"status": "OK"},
		})
	})
	c.now = fixedClock()
	c.SetAuth(&TokenBundle{Cookie: cookie, SAPISID: "abc123"})

	var out playerResponse
	if err := c.doPlayer(context.Background(), RequestPayload{VideoID: "abc"}, &out); err != nil {
		t.Fatalf("doPlayer with auth failed: %v", err)
	}
	if out.PlayabilityStatus.Status != "OK" {
		t.Fatalf("status = %q, want OK", out.PlayabilityStatus.Status)
	}
}

func TestSAPISIDHashFormat(t *testing.T) {
	got := sapisidHash("abc123", 1700000000)
	if !strings.HasPrefix(got, "SAPISIDHASH 1700000000_") {
		t.Fatalf("hash = %q, want SAPISIDHASH <ts>_ prefix", got)
	}
	if got != sapisidHash("abc123", 1700000000) {
		t.Fatal("sapisidHash is not deterministic")
	}
}

func TestServiceSetAuthPropagatesToPlayer(t *testing.T) {
	s := NewService()
	t.Cleanup(s.ClearAuth)

	bundle := &TokenBundle{Cookie: "SAPISID=x", SAPISID: "x"}
	s.SetAuth(bundle)

	if !s.IsLoggedIn() {
		t.Fatal("service reports signed out after SetAuth")
	}
	if s.player == nil || !s.player.client.HasAuth() {
		t.Fatal("player client did not receive the session")
	}
}
