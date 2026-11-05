package lyrics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseLRCBasic(t *testing.T) {
	rawLRC := `[ti:Starboy]
[ar:The Weeknd]
[al:Starboy]
[00:01.50]I'm tryna put you in the worst mood, ah
[00:04.80]P1 cleaner than your church shoes, ah
[00:08.10]Milli point two just to hurt you, ah
[00:11.45]All red Lamb' just to tease you, ah
`

	doc, err := ParseLRCString(rawLRC)
	if err != nil {
		t.Fatalf("ParseLRCString failed: %v", err)
	}

	if doc.Title != "Starboy" {
		t.Errorf("expected Title 'Starboy', got '%s'", doc.Title)
	}
	if doc.Artist != "The Weeknd" {
		t.Errorf("expected Artist 'The Weeknd', got '%s'", doc.Artist)
	}
	if doc.Album != "Starboy" {
		t.Errorf("expected Album 'Starboy', got '%s'", doc.Album)
	}
	if !doc.IsSynced {
		t.Errorf("expected IsSynced to be true")
	}
	if len(doc.Lines) != 4 {
		t.Fatalf("expected 4 lines, got %d", len(doc.Lines))
	}

	expectedFirst := 1*time.Second + 500*time.Millisecond
	if doc.Lines[0].Timestamp != expectedFirst {
		t.Errorf("expected line 0 timestamp %v, got %v", expectedFirst, doc.Lines[0].Timestamp)
	}
	if doc.Lines[0].Text != "I'm tryna put you in the worst mood, ah" {
		t.Errorf("unexpected line 0 text: %s", doc.Lines[0].Text)
	}
}

func TestParseLRCMultipleTimestampsAndUnsorted(t *testing.T) {
	rawLRC := `[00:10.00]Chorus line
[00:02.00][00:05.00]Intro beat
`
	doc, err := ParseLRCString(rawLRC)
	if err != nil {
		t.Fatalf("ParseLRCString failed: %v", err)
	}

	if len(doc.Lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(doc.Lines))
	}

	// Should be sorted chronologically
	if doc.Lines[0].Timestamp != 2*time.Second || doc.Lines[0].Text != "Intro beat" {
		t.Errorf("unexpected line 0: %+v", doc.Lines[0])
	}
	if doc.Lines[1].Timestamp != 5*time.Second || doc.Lines[1].Text != "Intro beat" {
		t.Errorf("unexpected line 1: %+v", doc.Lines[1])
	}
	if doc.Lines[2].Timestamp != 10*time.Second || doc.Lines[2].Text != "Chorus line" {
		t.Errorf("unexpected line 2: %+v", doc.Lines[2])
	}
}

func TestActiveLineIndex(t *testing.T) {
	doc := &LyricsDocument{
		Lines: []LyricLine{
			{Timestamp: 5 * time.Second, Text: "Line 1"},
			{Timestamp: 10 * time.Second, Text: "Line 2"},
			{Timestamp: 15 * time.Second, Text: "Line 3"},
		},
	}

	tests := []struct {
		playhead time.Duration
		expected int
	}{
		{playhead: 0 * time.Second, expected: -1},
		{playhead: 4999 * time.Millisecond, expected: -1},
		{playhead: 5 * time.Second, expected: 0},
		{playhead: 7 * time.Second, expected: 0},
		{playhead: 10 * time.Second, expected: 1},
		{playhead: 12 * time.Second, expected: 1},
		{playhead: 15 * time.Second, expected: 2},
		{playhead: 99 * time.Second, expected: 2},
	}

	for _, tc := range tests {
		idx := doc.ActiveLineIndex(tc.playhead)
		if idx != tc.expected {
			t.Errorf("at playhead %v: expected line index %d, got %d", tc.playhead, tc.expected, idx)
		}
	}

	emptyDoc := &LyricsDocument{}
	if emptyDoc.ActiveLineIndex(5*time.Second) != -1 {
		t.Errorf("expected -1 for empty doc")
	}
}

func TestLRCLIBClientFetch(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if r.URL.Path != "/get" {
			http.NotFound(w, r)
			return
		}

		track := r.URL.Query().Get("track_name")
		if track == "Unknown Song" {
			http.Error(w, `{"statusCode":404,"error":"Not Found"}`, http.StatusNotFound)
			return
		}

		resp := LRCLIBResponse{
			ID:           42,
			TrackName:    track,
			ArtistName:   r.URL.Query().Get("artist_name"),
			AlbumName:    "Test Album",
			Duration:     200,
			SyncedLyrics: "[00:01.00] Hello world\n[00:03.50] Testing LRCLIB\n",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(WithBaseURL(server.URL), WithHTTPClient(server.Client()))

	// Test successful fetch
	ctx := context.Background()
	doc, err := client.FetchLyrics(ctx, "Hello", "Adele", "25", 200)
	if err != nil {
		t.Fatalf("FetchLyrics failed: %v", err)
	}

	if !doc.IsSynced || len(doc.Lines) != 2 {
		t.Fatalf("unexpected doc parsed: %+v", doc)
	}
	if doc.Lines[0].Text != "Hello world" {
		t.Errorf("unexpected line 0 text: %s", doc.Lines[0].Text)
	}

	// Test cache hit (server should not receive second request)
	doc2, err := client.FetchLyrics(ctx, "Hello", "Adele", "25", 200)
	if err != nil {
		t.Fatalf("Cached FetchLyrics failed: %v", err)
	}
	if doc2 != doc {
		t.Errorf("expected pointer equality for cached doc")
	}
	if requestCount != 1 {
		t.Errorf("expected 1 HTTP request due to cache, got %d", requestCount)
	}

	// Test 404 Not Found
	_, err = client.FetchLyrics(ctx, "Unknown Song", "Unknown Artist", "", 0)
	if err != ErrLyricsNotFound {
		t.Errorf("expected ErrLyricsNotFound, got %v", err)
	}
}

func TestLRCLIBClientLive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live network test in short mode")
	}

	client := NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// "Blinding Lights" by "The Weeknd" is reliably indexed on LRCLIB
	doc, err := client.FetchLyrics(ctx, "Blinding Lights", "The Weeknd", "After Hours", 200)
	if err != nil {
		t.Logf("Live LRCLIB test skipped (network / API limit): %v", err)
		return
	}

	if len(doc.Lines) == 0 {
		t.Errorf("expected lines from live LRCLIB for Blinding Lights, got 0")
	}
	t.Logf("Fetched %d lines of lyrics for %s - %s (Synced: %v)", len(doc.Lines), doc.Artist, doc.Title, doc.IsSynced)
}
