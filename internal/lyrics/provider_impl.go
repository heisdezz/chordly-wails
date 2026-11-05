package lyrics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// lrcRedProvider implements the lrc.red lyrics API.
type lrcRedProvider struct {
	client *http.Client
}

// NewlrcRedProvider creates a new lrc.red lyrics provider.
func NewlrcRedProvider() *lrcRedProvider {
	return &lrcRedProvider{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (p *lrcRedProvider) Name() string {
	return "lrc.red"
}

func (p *lrcRedProvider) Priority() int {
	return 5 // Medium priority
}

// FetchLyrics implements the LyricsProvider interface for lrc.red.
// lrc.red API: GET https://lrc.red/api/get?artist=...&track=...&format=json
func (p *lrcRedProvider) FetchLyrics(ctx context.Context, trackName, artistName, albumName string, durationSec int) (*LyricsDocument, error) {
	if strings.TrimSpace(trackName) == "" {
		return nil, fmt.Errorf("trackName is required")
	}

	q := url.Values{}
	q.Set("track", trackName)
	if artistName != "" {
		q.Set("artist", artistName)
	}
	if albumName != "" {
		q.Set("album", albumName)
	}
	if durationSec > 0 {
		q.Set("duration", strconv.Itoa(durationSec))
	}

	reqURL := fmt.Sprintf("https://lrc.red/api/get?%s", q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("User-Agent", "Chordly/1.0 (lyrics provider)")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lrc.red request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // Not found, fallback to next provider
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("lrc.red responded with status %d", resp.StatusCode)
	}

	var data struct {
		Lyrics string `json:"lyrics"`
		Roman  string `json:"roman"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode json response failed: %w", err)
	}

	if data.Lyrics == "" {
		return nil, nil // Not found, fallback to next provider
	}

	// Parse the LRC lyrics
	doc, err := ParseLRCString(data.Lyrics)
	if err != nil {
		return nil, fmt.Errorf("failed to parse lrc.red lyrics: %w", err)
	}

	return doc, nil
}

// binilyticsProvider implements the BiniLyrics lyrics API.
type binilyticsProvider struct {
	client *http.Client
}

// NewbinilyticsProvider creates a new BiniLyrics lyrics provider.
func NewbinilyticsProvider() *binilyticsProvider {
	return &binilyticsProvider{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (p *binilyticsProvider) Name() string {
	return "BiniLyrics"
}

func (p *binilyticsProvider) Priority() int {
	return 5 // Medium priority
}

// FetchLyrics implements the LyricsProvider interface for BiniLyrics.
// BiniLyrics API: GET https://api.bini.lyrics/v1/search?artist=...&track=...
func (p *binilyticsProvider) FetchLyrics(ctx context.Context, trackName, artistName, albumName string, durationSec int) (*LyricsDocument, error) {
	if strings.TrimSpace(trackName) == "" {
		return nil, fmt.Errorf("trackName is required")
	}

	q := url.Values{}
	q.Set("track", trackName)
	if artistName != "" {
		q.Set("artist", artistName)
	}
	if albumName != "" {
		q.Set("album", albumName)
	}

	reqURL := fmt.Sprintf("https://api.bini.lyrics/v1/search?%s", q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("User-Agent", "Chordly/1.0 (lyrics provider)")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("BiniLyrics request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // Not found, fallback to next provider
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("BiniLyrics responded with status %d", resp.StatusCode)
	}

	var data struct {
		Lyrics string `json:"lyrics"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode json response failed: %w", err)
	}

	if data.Lyrics == "" {
		return nil, nil // Not found, fallback to next provider
	}

	// Parse the LRC lyrics
	doc, err := ParseLRCString(data.Lyrics)
	if err != nil {
		return nil, fmt.Errorf("failed to parse BiniLyrics lyrics: %w", err)
	}

	return doc, nil
}

// betterLyricsProvider implements the BetterLyrics lyrics API.
type betterLyricsProvider struct {
	client *http.Client
}

// NewbetterLyricsProvider creates a new BetterLyrics lyrics provider.
func NewbetterLyricsProvider() *betterLyricsProvider {
	return &betterLyricsProvider{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (p *betterLyricsProvider) Name() string {
	return "BetterLyrics"
}

func (p *betterLyricsProvider) Priority() int {
	return 5 // Medium priority
}

// FetchLyrics implements the LyricsProvider interface for BetterLyrics.
// BetterLyrics API: GET https://api.betterlyrics.com/v1/lyrics?artist=...&track=...
func (p *betterLyricsProvider) FetchLyrics(ctx context.Context, trackName, artistName, albumName string, durationSec int) (*LyricsDocument, error) {
	if strings.TrimSpace(trackName) == "" {
		return nil, fmt.Errorf("trackName is required")
	}

	q := url.Values{}
	q.Set("track", trackName)
	if artistName != "" {
		q.Set("artist", artistName)
	}
	if albumName != "" {
		q.Set("album", albumName)
	}

	reqURL := fmt.Sprintf("https://api.betterlyrics.com/v1/lyrics?%s", q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("User-Agent", "Chordly/1.0 (lyrics provider)")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("BetterLyrics request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // Not found, fallback to next provider
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("BetterLyrics responded with status %d", resp.StatusCode)
	}

	var data struct {
		Lrc string `json:"lrc"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode json response failed: %w", err)
	}

	if data.Lrc == "" {
		return nil, nil // Not found, fallback to next provider
	}

	// Parse the LRC lyrics
	doc, err := ParseLRCString(data.Lrc)
	if err != nil {
		return nil, fmt.Errorf("failed to parse BetterLyrics lyrics: %w", err)
	}

	return doc, nil
}

// paxSenixProvider implements the PaxSenix lyrics API.
type paxSenixProvider struct {
	client *http.Client
}

// NewpaxSenixProvider creates a new PaxSenix lyrics provider.
func NewpaxSenixProvider() *paxSenixProvider {
	return &paxSenixProvider{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (p *paxSenixProvider) Name() string {
	return "PaxSenix"
}

func (p *paxSenixProvider) Priority() int {
	return 5 // Medium priority
}

// FetchLyrics implements the LyricsProvider interface for PaxSenix.
// PaxSenix API: GET https://api.pax.senix.com/lyrics?artist=...&track=...
func (p *paxSenixProvider) FetchLyrics(ctx context.Context, trackName, artistName, albumName string, durationSec int) (*LyricsDocument, error) {
	if strings.TrimSpace(trackName) == "" {
		return nil, fmt.Errorf("trackName is required")
	}

	q := url.Values{}
	q.Set("track", trackName)
	if artistName != "" {
		q.Set("artist", artistName)
	}
	if albumName != "" {
		q.Set("album", albumName)
	}

	reqURL := fmt.Sprintf("https://api.pax.senix.com/lyrics?%s", q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("User-Agent", "Chordly/1.0 (lyrics provider)")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("PaxSenix request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // Not found, fallback to next provider
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("PaxSenix responded with status %d", resp.StatusCode)
	}

	var data struct {
		Lrc string `json:"lrc"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode json response failed: %w", err)
	}

	if data.Lrc == "" {
		return nil, nil // Not found, fallback to next provider
	}

	// Parse the LRC lyrics
	doc, err := ParseLRCString(data.Lrc)
	if err != nil {
		return nil, fmt.Errorf("failed to parse PaxSenix lyrics: %w", err)
	}

	return doc, nil
}

// lyricsPlusProvider implements the LyricsPlus lyrics API.
type lyricsPlusProvider struct {
	client *http.Client
}

// NewLyricsPlusProvider creates a new LyricsPlus lyrics provider.
func NewLyricsPlusProvider() *lyricsPlusProvider {
	return &lyricsPlusProvider{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (p *lyricsPlusProvider) Name() string {
	return "LyricsPlus"
}

func (p *lyricsPlusProvider) Priority() int {
	return 5 // Medium priority
}

// FetchLyrics implements the LyricsProvider interface for LyricsPlus.
// LyricsPlus API: GET https://api.lyricsplus.com/v1/search?artist=...&track=...
func (p *lyricsPlusProvider) FetchLyrics(ctx context.Context, trackName, artistName, albumName string, durationSec int) (*LyricsDocument, error) {
	if strings.TrimSpace(trackName) == "" {
		return nil, fmt.Errorf("trackName is required")
	}

	q := url.Values{}
	q.Set("track", trackName)
	if artistName != "" {
		q.Set("artist", artistName)
	}
	if albumName != "" {
		q.Set("album", albumName)
	}

	reqURL := fmt.Sprintf("https://api.lyricsplus.com/v1/search?%s", q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("User-Agent", "Chordly/1.0 (lyrics provider)")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("LyricsPlus request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // Not found, fallback to next provider
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("LyricsPlus responded with status %d", resp.StatusCode)
	}

	var data struct {
		Lrc string `json:"lrc"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode json response failed: %w", err)
	}

	if data.Lrc == "" {
		return nil, nil // Not found, fallback to next provider
	}

	// Parse the LRC lyrics
	doc, err := ParseLRCString(data.Lrc)
	if err != nil {
		return nil, fmt.Errorf("failed to parse LyricsPlus lyrics: %w", err)
	}

	return doc, nil
}

// simpMusicProvider implements the KuGou (SimpMusic) lyrics API.
type simpMusicProvider struct {
	client *http.Client
}

// NewSimpMusicProvider creates a new SimpMusic (KuGou) lyrics provider.
func NewSimpMusicProvider() *simpMusicProvider {
	return &simpMusicProvider{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (p *simpMusicProvider) Name() string {
	return "SimpMusic"
}

func (p *simpMusicProvider) Priority() int {
	return 5 // Medium priority
}

// FetchLyrics implements the LyricsProvider interface for SimpMusic (KuGou).
// KuGou API: GET https://api.kuwo.cn/lyric/api/lyric?type=kg&id=...&format=json
// Note: KuGou typically uses track ID, but we'll search by name as fallback
func (p *simpMusicProvider) FetchLyrics(ctx context.Context, trackName, artistName, albumName string, durationSec int) (*LyricsDocument, error) {
	if strings.TrimSpace(trackName) == "" {
		return nil, fmt.Errorf("trackName is required")
	}

	// KuGou/search API
	q := url.Values{}
	q.Set("key", trackName)
	if artistName != "" {
		q.Set("artist", artistName)
	}
	if albumName != "" {
		q.Set("album", albumName)
	}

	reqURL := fmt.Sprintf("https://api.kuwo.cn/lyric/api/lyric?type=kg&%s", q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("User-Agent", "Chordly/1.0 (lyrics provider)")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("SimpMusic/KuGou request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // Not found, fallback to next provider
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("SimpMusic/KuGou responded with status %d", resp.StatusCode)
	}

	var data struct {
		Lrc string `json:"lrc"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode json response failed: %w", err)
	}

	if data.Lrc == "" {
		return nil, nil // Not found, fallback to next provider
	}

	// Parse the LRC lyrics
	doc, err := ParseLRCString(data.Lrc)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SimpMusic lyrics: %w", err)
	}

	return doc, nil
}

// unisonProvider implements the Unison lyrics API.
type unisonProvider struct {
	client *http.Client
}

// NewUnisonProvider creates a new Unison lyrics provider.
func NewUnisonProvider() *unisonProvider {
	return &unisonProvider{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (p *unisonProvider) Name() string {
	return "Unison"
}

func (p *unisonProvider) Priority() int {
	return 5 // Medium priority
}

// FetchLyrics implements the LyricsProvider interface for Unison.
// Unison API: GET https://api.unison-lyrics.com/v1/lyrics?artist=...&track=...
func (p *unisonProvider) FetchLyrics(ctx context.Context, trackName, artistName, albumName string, durationSec int) (*LyricsDocument, error) {
	if strings.TrimSpace(trackName) == "" {
		return nil, fmt.Errorf("trackName is required")
	}

	q := url.Values{}
	q.Set("track", trackName)
	if artistName != "" {
		q.Set("artist", artistName)
	}
	if albumName != "" {
		q.Set("album", albumName)
	}

	reqURL := fmt.Sprintf("https://api.unison-lyrics.com/v1/lyrics?%s", q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("User-Agent", "Chordly/1.0 (lyrics provider)")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Unison request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // Not found, fallback to next provider
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Unison responded with status %d", resp.StatusCode)
	}

	var data struct {
		Lrc string `json:"lrc"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode json response failed: %w", err)
	}

	if data.Lrc == "" {
		return nil, nil // Not found, fallback to next provider
	}

	// Parse the LRC lyrics
	doc, err := ParseLRCString(data.Lrc)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Unison lyrics: %w", err)
	}

	return doc, nil
}

// megalobizProvider implements the Megalobiz lyrics API.
type megalobizProvider struct {
	client *http.Client
}

// NewMegalobizProvider creates a new Megalobiz lyrics provider.
func NewMegalobizProvider() *megalobizProvider {
	return &megalobizProvider{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (p *megalobizProvider) Name() string {
	return "Megalobiz"
}

func (p *megalobizProvider) Priority() int {
	return 5 // Medium priority
}

// FetchLyrics implements the LyricsProvider interface for Megalobiz.
// Megalobiz API: GET https://api.megalobiz.com/v1/lyrics?artist=...&track=...
func (p *megalobizProvider) FetchLyrics(ctx context.Context, trackName, artistName, albumName string, durationSec int) (*LyricsDocument, error) {
	if strings.TrimSpace(trackName) == "" {
		return nil, fmt.Errorf("trackName is required")
	}

	q := url.Values{}
	q.Set("track", trackName)
	if artistName != "" {
		q.Set("artist", artistName)
	}
	if albumName != "" {
		q.Set("album", albumName)
	}

	reqURL := fmt.Sprintf("https://api.megalobiz.com/v1/lyrics?%s", q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("User-Agent", "Chordly/1.0 (lyrics provider)")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Megalobiz request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // Not found, fallback to next provider
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Megalobiz responded with status %d", resp.StatusCode)
	}

	var data struct {
		Lrc string `json:"lrc"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode json response failed: %w", err)
	}

	if data.Lrc == "" {
		return nil, nil // Not found, fallback to next provider
	}

	// Parse the LRC lyrics
	doc, err := ParseLRCString(data.Lrc)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Megalobiz lyrics: %w", err)
	}

	return doc, nil
}

// kuGouProvider implements the KuGou lyrics API (different from SimpMusic).
type kuGouProvider struct {
	client *http.Client
}

// NewKuGouProvider creates a new KuGou lyrics provider.
func NewKuGouProvider() *kuGouProvider {
	return &kuGouProvider{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (p *kuGouProvider) Name() string {
	return "KuGou"
}

func (p *kuGouProvider) Priority() int {
	return 5 // Medium priority
}

// FetchLyrics implements the LyricsProvider interface for KuGou.
// KuGou API: GET https://www.kuwo.cn/api/search/lrc?...
func (p *kuGouProvider) FetchLyrics(ctx context.Context, trackName, artistName, albumName string, durationSec int) (*LyricsDocument, error) {
	if strings.TrimSpace(trackName) == "" {
		return nil, fmt.Errorf("trackName is required")
	}

	// KuGou search and lyrics API
	q := url.Values{}
	q.Set("q", trackName)
	if artistName != "" {
		q.Set("artist", artistName)
	}

	reqURL := fmt.Sprintf("https://www.kuwo.cn/api/search/lrc?%s", q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("User-Agent", "Chordly/1.0 (lyrics provider)")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("KuGou request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // Not found, fallback to next provider
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("KuGou responded with status %d", resp.StatusCode)
	}

	var data struct {
		Lrc string `json:"lrc"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode json response failed: %w", err)
	}

	if data.Lrc == "" {
		return nil, nil // Not found, fallback to next provider
	}

	// Parse the LRC lyrics
	doc, err := ParseLRCString(data.Lrc)
	if err != nil {
		return nil, fmt.Errorf("failed to parse KuGou lyrics: %w", err)
	}

	return doc, nil
}

// musixmatchProvider implements the Musixmatch lyrics API.
type musixmatchProvider struct {
	client *http.Client
}

// NewMusixmatchProvider creates a new Musixmatch lyrics provider.
func NewMusixmatchProvider() *musixmatchProvider {
	return &musixmatchProvider{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (p *musixmatchProvider) Name() string {
	return "Musixmatch"
}

func (p *musixmatchProvider) Priority() int {
	return 8 // High priority - well-established API
}

// FetchLyrics implements the LyricsProvider interface for Musixmatch.
// Musixmatch API: Uses public endpoints via r.jina.ai proxy
func (p *musixmatchProvider) FetchLyrics(ctx context.Context, trackName, artistName, albumName string, durationSec int) (*LyricsDocument, error) {
	if strings.TrimSpace(trackName) == "" {
		return nil, fmt.Errorf("trackName is required")
	}

	// Musixmatch public search API via jina.ai proxy
	searchURL := fmt.Sprintf("https://r.jina.ai/http://api.musixmatch.com/ws/1.1/track.search.get?track_name=%s&artist_name=%s&page_size=1&format=json",
		url.QueryEscape(trackName), url.QueryEscape(artistName))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("User-Agent", "Chordly/1.0 (lyrics provider)")
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Musixmatch request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil // Not found, fallback to next provider
	}

	var data struct {
		Message string `json:"message"`
		Track   struct {
			LyricsID   int    `json:"lyrics_id"`
			TrackName  string `json:"track_name"`
			ArtistName string `json:"artist_name"`
		} `json:"track"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode json response failed: %w", err)
	}

	// If we found a track, get the lyrics
	if data.Track.LyricsID > 0 {
		lyricsReqURL := fmt.Sprintf("https://r.jina.ai/http://api.musixmatch.com/ws/1.1/track.lyrics.get?track_id=%d&format=json", data.Track.LyricsID)
		lyricsReq, err := http.NewRequestWithContext(ctx, http.MethodGet, lyricsReqURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create lyrics request failed: %w", err)
		}
		lyricsReq.Header.Set("User-Agent", "Chordly/1.0 (lyrics provider)")

		lyricsResp, err := p.client.Do(lyricsReq)
		if err != nil {
			return nil, fmt.Errorf("Musixmatch lyrics request failed: %w", err)
		}
		defer lyricsResp.Body.Close()

		if lyricsResp.StatusCode != http.StatusOK {
			return nil, nil // Not found, fallback to next provider
		}

		var lyricsData struct {
			Lyrics struct {
				LrcBody string `json:"lyrics_body"`
			} `json:"lyrics"`
		}

		if err := json.NewDecoder(lyricsResp.Body).Decode(&lyricsData); err != nil {
			return nil, fmt.Errorf("decode lyrics json response failed: %w", err)
		}

		if lyricsData.Lyrics.LrcBody == "" {
			return nil, nil // Not found, fallback to next provider
		}

		// Parse the LRC lyrics
		doc, err := ParseLRCString(lyricsData.Lyrics.LrcBody)
		if err != nil {
			return nil, fmt.Errorf("failed to parse Musixmatch lyrics: %w", err)
		}

		return doc, nil
	}

	return nil, nil // Not found, fallback to next provider
}

// geniusProvider implements the Genius lyrics API.
type geniusProvider struct {
	client *http.Client
}

// NewGeniusProvider creates a new Genius lyrics provider.
func NewGeniusProvider() *geniusProvider {
	return &geniusProvider{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (p *geniusProvider) Name() string {
	return "Genius"
}

func (p *geniusProvider) Priority() int {
	return 8 // High priority - well-established API
}

// FetchLyrics implements the LyricsProvider interface for Genius.
// Genius API: Uses public endpoints via r.jina.ai proxy
func (p *geniusProvider) FetchLyrics(ctx context.Context, trackName, artistName, albumName string, durationSec int) (*LyricsDocument, error) {
	if strings.TrimSpace(trackName) == "" {
		return nil, fmt.Errorf("trackName is required")
	}

	// Genius search API via jina.ai proxy
	searchURL := fmt.Sprintf("https://r.jina.ai/https://genius.com/api/search?q=%s+%s",
		url.QueryEscape(trackName), url.QueryEscape(artistName))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("User-Agent", "Chordly/1.0 (lyrics provider)")
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Genius request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil // Not found, fallback to next provider
	}

	var data struct {
		Response struct {
			Hits []struct {
				Result struct {
					FullTitle string `json:"full_title"`
					Path      string `json:"path"`
					URL       string `json:"url"`
				} `json:"result"`
			} `json:"hits"`
		} `json:"response"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode json response failed: %w", err)
	}

	if len(data.Response.Hits) == 0 {
		return nil, nil // Not found, fallback to next provider
	}

	// Fetch lyrics via jina.ai proxy of the Genius page
	lyricsProxyURL := fmt.Sprintf("https://r.jina.ai/https://genius.com%s", data.Response.Hits[0].Result.Path)

	lyricsReq, err := http.NewRequestWithContext(ctx, http.MethodGet, lyricsProxyURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create lyrics request failed: %w", err)
	}
	lyricsReq.Header.Set("User-Agent", "Chordly/1.0 (lyrics provider)")

	lyricsResp, err := p.client.Do(lyricsReq)
	if err != nil {
		return nil, fmt.Errorf("Genius lyrics request failed: %w", err)
	}
	defer lyricsResp.Body.Close()

	if lyricsResp.StatusCode != http.StatusOK {
		return nil, nil // Not found, fallback to next provider
	}

	// For now, return nil - Genius requires API token for full lyrics access
	// In a future enhancement, add proper Genius API key support
	return nil, nil // TODO: Implement with Genius API key
}
