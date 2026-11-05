package lyrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	// ErrLyricsNotFound is returned when LRCLIB has no lyrics for the query.
	ErrLyricsNotFound = errors.New("lyrics not found")
)

// LRCLIBResponse matches the JSON schema returned by https://lrclib.net/api/get
type LRCLIBResponse struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	TrackName    string  `json:"trackName"`
	ArtistName   string  `json:"artistName"`
	AlbumName    string  `json:"albumName"`
	Duration     float64 `json:"duration"`
	Instrumental bool    `json:"instrumental"`
	PlainLyrics  string  `json:"plainLyrics"`
	SyncedLyrics string  `json:"syncedLyrics"`
}

// Client fetches and caches synchronized lyrics from LRCLIB.
type Client struct {
	baseURL    string
	httpClient *http.Client
	userAgent  string

	mu    sync.RWMutex
	cache map[string]*LyricsDocument
}

// Option configures Client behavior.
type Option func(*Client)

// WithBaseURL overrides the default LRCLIB base URL (useful for testing).
func WithBaseURL(rawURL string) Option {
	return func(c *Client) {
		c.baseURL = strings.TrimRight(rawURL, "/")
	}
}

// WithHTTPClient overrides the default HTTP transport.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) {
		if httpClient != nil {
			c.httpClient = httpClient
		}
	}
}

// WithUserAgent customizes the User-Agent header sent to LRCLIB.
func WithUserAgent(ua string) Option {
	return func(c *Client) {
		c.userAgent = ua
	}
}

// NewClient creates a new LRCLIB lyrics client.
func NewClient(opts ...Option) *Client {
	c := &Client{
		baseURL: "https://lrclib.net/api",
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		userAgent: "Chordly/1.0 (https://github.com/chordly/chord)",
		cache:     make(map[string]*LyricsDocument),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// cacheKey builds an in-memory cache lookup key.
func cacheKey(track, artist string, durationSec int) string {
	return strings.ToLower(fmt.Sprintf("%s|%s|%d", strings.TrimSpace(track), strings.TrimSpace(artist), durationSec))
}

// FetchLyrics retrieves synchronized (or plain) lyrics for the given song.
// It queries the LRCLIB /get endpoint with fallback to in-memory caching.
func (c *Client) FetchLyrics(ctx context.Context, trackName, artistName, albumName string, durationSec int) (*LyricsDocument, error) {
	if strings.TrimSpace(trackName) == "" {
		return nil, errors.New("trackName is required")
	}

	key := cacheKey(trackName, artistName, durationSec)

	// Check cache
	c.mu.RLock()
	if cached, ok := c.cache[key]; ok {
		c.mu.RUnlock()
		return cached, nil
	}
	c.mu.RUnlock()

	endpoint := fmt.Sprintf("%s/get", c.baseURL)
	q := url.Values{}
	q.Set("track_name", trackName)
	if artistName != "" {
		q.Set("artist_name", artistName)
	}
	if albumName != "" {
		q.Set("album_name", albumName)
	}
	if durationSec > 0 {
		q.Set("duration", strconv.Itoa(durationSec))
	}

	reqURL := fmt.Sprintf("%s?%s", endpoint, q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}

	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lrclib request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrLyricsNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("lrclib responded with status %d", resp.StatusCode)
	}

	var data LRCLIBResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode json response failed: %w", err)
	}

	var doc *LyricsDocument
	if data.SyncedLyrics != "" {
		doc, err = ParseLRCString(data.SyncedLyrics)
		if err != nil {
			return nil, fmt.Errorf("failed to parse synced lyrics: %w", err)
		}
	} else if data.PlainLyrics != "" {
		// Non-synced lyrics: split into lines without timestamps
		lines := strings.Split(data.PlainLyrics, "\n")
		doc = &LyricsDocument{
			IsSynced: false,
			Lines:    make([]LyricLine, 0, len(lines)),
		}
		for _, l := range lines {
			t := strings.TrimSpace(l)
			if t != "" {
				doc.Lines = append(doc.Lines, LyricLine{Text: t})
			}
		}
	} else {
		return nil, ErrLyricsNotFound
	}

	// Fill metadata if missing from LRC tags
	if doc.Title == "" && (data.TrackName != "" || data.Name != "") {
		if data.TrackName != "" {
			doc.Title = data.TrackName
		} else {
			doc.Title = data.Name
		}
	}
	if doc.Artist == "" && data.ArtistName != "" {
		doc.Artist = data.ArtistName
	}
	if doc.Album == "" && data.AlbumName != "" {
		doc.Album = data.AlbumName
	}

	// Store in cache
	c.mu.Lock()
	c.cache[key] = doc
	c.mu.Unlock()

	return doc, nil
}
