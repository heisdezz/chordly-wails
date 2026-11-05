package lyrics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// LyricsProvider defines the interface for fetching lyrics from various sources.
type LyricsProvider interface {
	// Name returns the human-readable name of the provider.
	Name() string
	// Priority returns the default priority for fallback ordering (higher = tried first).
	Priority() int
	// FetchLyrics attempts to retrieve lyrics for the given track.
	// Returns nil, nil if lyrics not found (not an error, so fallback can continue).
	// Returns an error only for network/transport failures.
	FetchLyrics(ctx context.Context, trackName, artistName, albumName string, durationSec int) (*LyricsDocument, error)
}

// providerWrapper wraps a LyricsProvider with priority and caching support.
type providerWrapper struct {
	name     string
	priority int
	client   *http.Client
	mu       sync.RWMutex
	cache    map[string]*LyricsDocument
}

// NewProviderWrapper creates a wrapper for a LyricsProvider with caching.
func NewProviderWrapper(name string, priority int, client *http.Client) *providerWrapper {
	return &providerWrapper{
		name:     name,
		priority: priority,
		client:   client,
		cache:    make(map[string]*LyricsDocument),
	}
}

func (p *providerWrapper) Name() string {
	return p.name
}

func (p *providerWrapper) Priority() int {
	return p.priority
}

// FetchLyrics implements the LyricsProvider interface.
func (p *providerWrapper) FetchLyrics(ctx context.Context, trackName, artistName, albumName string, durationSec int) (*LyricsDocument, error) {
	if strings.TrimSpace(trackName) == "" {
		return nil, fmt.Errorf("trackName is required")
	}

	key := strings.ToLower(fmt.Sprintf("%s|%s|%d", strings.TrimSpace(trackName), strings.TrimSpace(artistName), durationSec))

	// Check cache
	p.mu.RLock()
	if cached, ok := p.cache[key]; ok {
		p.mu.RUnlock()
		return cached, nil
	}
	p.mu.RUnlock()

	endpoint := "https://lrclib.net/api/get"
	q := url.Values{}
	q.Set("track_name", trackName)
	if artistName != "" {
		q.Set("artist_name", artistName)
	}
	if albumName != "" {
		q.Set("album_name", albumName)
	}
	if durationSec > 0 {
		q.Set("duration", fmt.Sprintf("%d", durationSec))
	}

	reqURL := fmt.Sprintf("%s?%s", endpoint, q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("User-Agent", "Chordly/1.0 (lyrics provider fallback)")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("provider request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // "not found" - fallback to next provider
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("provider responded with status %d", resp.StatusCode)
	}

	var data struct {
		SyncedLyrics string `json:"syncedLyrics"`
		PlainLyrics  string `json:"plainLyrics"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode json response failed: %w", err)
	}

	var doc *LyricsDocument
	if data.SyncedLyrics != "" {
		var parseErr error
		doc, parseErr = ParseLRCString(data.SyncedLyrics)
		if parseErr != nil {
			return nil, fmt.Errorf("failed to parse synced lyrics: %w", parseErr)
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
		return nil, nil // "not found" - fallback to next provider
	}

	// Store in cache
	p.mu.Lock()
	p.cache[key] = doc
	p.mu.Unlock()

	return doc, nil
}

// ProviderManager manages multiple lyrics providers with fallback ordering.
type ProviderManager struct {
	providers []LyricsProvider
	mu        sync.Mutex
}

// NewProviderManager creates a new provider manager with the given providers.
func NewProviderManager(providers ...LyricsProvider) *ProviderManager {
	// Sort by priority (descending - higher priority first)
	pm := &ProviderManager{
		providers: providers,
	}
	pm.sortByPriority()
	return pm
}

// sortByPriority sorts providers by priority in descending order (highest first).
func (pm *ProviderManager) sortByPriority() {
	for i := 0; i < len(pm.providers); i++ {
		for j := i + 1; j < len(pm.providers); j++ {
			if pm.providers[i].Priority() < pm.providers[j].Priority() {
				pm.providers[i], pm.providers[j] = pm.providers[j], pm.providers[i]
			}
		}
	}
}

// AddProvider adds a provider to the manager.
func (pm *ProviderManager) AddProvider(provider LyricsProvider) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.providers = append(pm.providers, provider)
	pm.sortByPriority()
}

// Providers returns a copy of the current provider list.
func (pm *ProviderManager) Providers() []LyricsProvider {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	result := make([]LyricsProvider, len(pm.providers))
	copy(result, pm.providers)
	return result
}

// FetchLyrics tries each provider in order until one succeeds or all are exhausted.
// Returns nil, nil if no provider found lyrics.
func (pm *ProviderManager) FetchLyrics(ctx context.Context, trackName, artistName, albumName string, durationSec int) (*LyricsDocument, error) {
	pm.mu.Lock()
	providers := pm.providers
	pm.mu.Unlock()

	for _, provider := range providers {
		doc, err := provider.FetchLyrics(ctx, trackName, artistName, albumName, durationSec)
		if err != nil {
			// Network error - try next provider
			continue
		}
		if doc != nil && len(doc.Lines) > 0 {
			// Found lyrics, return them
			return doc, nil
		}
		// doc is nil or empty - try next provider (this is "not found", not an error)
	}

	// No provider found lyrics
	return nil, nil
}

// GetNextProviderIndex returns the index of the next provider to try after the current one.
// This is useful for cycling/round-robin behavior.
func (pm *ProviderManager) GetNextProviderIndex(current int) int {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if len(pm.providers) <= 1 {
		return 0
	}
	next := (current + 1) % len(pm.providers)
	return next
}
