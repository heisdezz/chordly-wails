package ytm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// StreamInfo caches resolved audio stream metadata.
type StreamInfo struct {
	VideoID   string
	URL       string
	MimeType  string
	ExpiresAt time.Time
}

// PlayerService handles resolving playable audio stream URLs.
//
// Resolution strategy (per InnerTune services plan Module 4):
//  1. Query `/youtubei/v1/player` with the ANDROID_MUSIC client, which serves
//     direct `url` fields on `streamingData.adaptiveFormats` without requiring
//     signature deciphering.
//  2. Select the highest-fidelity audio-only format: Opus/webm (itag 251)
//     first, AAC/mp4 (itag 140) as fallback.
//  3. Cache the resolved URL in memory; URLs typically stay valid for ~6h, so
//     a 4h expiry is used for safety.
type PlayerService struct {
	client *Client
	mu     sync.RWMutex
	cache  map[string]*StreamInfo

	// ytDlpPath is an optional fallback extractor. It is only set for the
	// production service; injected test clients leave it empty.
	ytDlpPath string

	// validateURL probes the CDN before returning a URL. Tests leave it nil.
	validateURL func(context.Context, string) error

	// refreshVisitor mints anonymous visitor data before resolving. Disabled
	// for injected test clients.
	refreshVisitor bool
}

var DefaultPlayerService = NewPlayerService()

// playableClients mirrors yt-dlp's spoofed clients. YouTube gates different
// videos per client, so resolver tries them in order until one returns a
// playable, byte-verified stream.
var playableClients = []playerClientConfig{
	androidMusicConfig,
	{
		Name:        "TVHTML5",
		Version:     "5.20260707",
		DeviceMake:  "Samsung",
		DeviceModel: "UKS9800",
		OSName:      "Tizen",
		OSVersion:   "2.4.0",
		UserAgent:   "Mozilla/5.0 (SMART-TV; Linux; Tizen 2.4.0) AppleWebKit/538.1 (KHTML, like Gecko) Version/2.4.0 TV Safari/538.1",
		Host:        "www",
	},
	{
		Name:        "VISIONOS",
		Version:     "1.02",
		DeviceMake:  "Apple",
		DeviceModel: "RealityDevice17,1",
		OSName:      "visionOS",
		OSVersion:   "26.5.23O471",
		UserAgent:   "Mozilla/5.0 (Macintosh; Intel Mac OS X 15_7_3) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.0 Safari/605.1.15",
		Host:        "www",
	},
	{
		Name:        "ANDROID_VR",
		Version:     "1.65.10",
		DeviceMake:  "Oculus",
		DeviceModel: "Quest 3",
		OSName:      "Android",
		OSVersion:   "12L",
		AndroidSDK:  32,
		UserAgent:   "com.google.android.apps.youtube.vr.oculus/1.65.10 (Linux; U; Android 12L; eureka-user Build/SQ3A.220605.009.A1) gzip",
		Host:        "www",
	},
	{
		Name:        "IOS",
		Version:     "21.26.4",
		DeviceMake:  "Apple",
		DeviceModel: "iPhone16,2",
		OSName:      "iPhone",
		OSVersion:   "18.3.2.22D82",
		UserAgent:   "com.google.ios.youtube/21.26.4 (iPhone16,2; U; CPU iOS 18_3_2 like Mac OS X;)",
		Host:        "www",
	},
	{
		Name:      "MWEB",
		Version:   "2.20260708.05.00",
		UserAgent: "Mozilla/5.0 (iPad; CPU OS 16_7_10 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.6 Mobile/15E148 Safari/604.1,gzip(gfe)",
		Host:      "www",
	},
	{
		Name:    "WEB_REMIX",
		Version: "1.20260707.12.00",
		Host:    "music",
	},
}

func NewPlayerService() *PlayerService {
	return &PlayerService{
		client:         NewClient(),
		cache:          make(map[string]*StreamInfo),
		ytDlpPath:      findYTDLP(),
		validateURL:    validateStreamURL,
		refreshVisitor: true,
	}
}

// NewPlayerServiceWithClient allows tests and the Service facade to inject a
// client (e.g. pointed at a stub transport).
func NewPlayerServiceWithClient(c *Client) *PlayerService {
	return &PlayerService{
		client: c,
		cache:  make(map[string]*StreamInfo),
	}
}

// SetAuth applies an imported session to the player's InnerTube client.
// Player requests otherwise go out anonymously and gated tracks return
// LOGIN_REQUIRED even when the rest of the app is signed in.
func (p *PlayerService) SetAuth(bundle *TokenBundle) {
	if p.client != nil {
		p.client.SetAuth(bundle)
	}
}

// ResolveStreamURL retrieves a direct playable audio stream URL for a YouTube video ID.
func (p *PlayerService) ResolveStreamURL(ctx context.Context, videoID string) (string, error) {
	if videoID == "" {
		return "", errors.New("empty videoID")
	}

	// 1. Check in-memory cache
	p.mu.RLock()
	cached, ok := p.cache[videoID]
	p.mu.RUnlock()
	if ok && time.Now().Before(cached.ExpiresAt) {
		return cached.URL, nil
	}

	// 2. Resolve via InnerTube player endpoint
	streamURL, mimeType, err := p.resolveStream(ctx, videoID)
	if err != nil {
		if p.ytDlpPath != "" && shouldFallbackToYTDLP(err) {
			fallbackURL, fallbackMime, fallbackErr := p.resolveWithYTDLP(ctx, videoID)
			if fallbackErr == nil {
				log.Printf("[YTM] InnerTube failed, using yt-dlp stream fallback for %s", videoID)
				streamURL, mimeType, err = fallbackURL, fallbackMime, nil
			} else {
				log.Printf("[YTM] yt-dlp fallback failed for %s: %v", videoID, fallbackErr)
			}
		}
		if err != nil {
			return "", fmt.Errorf("resolve audio stream for %s failed: %w", videoID, err)
		}
	}

	// 3. Store in cache (see header comment for the 4h safety expiry)
	p.mu.Lock()
	p.cache[videoID] = &StreamInfo{
		VideoID:   videoID,
		URL:       streamURL,
		MimeType:  mimeType,
		ExpiresAt: time.Now().Add(4 * time.Hour),
	}
	p.mu.Unlock()

	return streamURL, nil
}

// playerResponse models the subset of `/youtubei/v1/player` needed to select
// an audio stream.
type playerResponse struct {
	StreamingData struct {
		AdaptiveFormats []playerFormat `json:"adaptiveFormats"`
		Formats         []playerFormat `json:"formats"`
	} `json:"streamingData"`
	PlayabilityStatus struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	} `json:"playabilityStatus"`
}

type playerFormat struct {
	Itag         int    `json:"itag"`
	URL          string `json:"url"`
	MimeType     string `json:"mimeType"`
	AudioQuality string `json:"audioQuality"`
}

// resolveStream queries the player endpoint and returns the chosen URL plus
// its mime type. Several spoofed clients are tried because YouTube gates
// different videos per client; the result is probed for actual bytes before
// being handed to the audio engine.
//
// When a session is attached it is tried first, then the same client rotation
// is retried anonymously. Imported cookies are sometimes rejected outright
// (LOGIN_REQUIRED or 400 INVALID_ARGUMENT) while the anonymous clients still
// serve a playable stream, so a signed-in user must not be worse off than a
// guest.
func (p *PlayerService) resolveStream(ctx context.Context, videoID string) (string, string, error) {
	client := p.client
	if client == nil {
		client = NewClient()
	}

	payload := RequestPayload{
		VideoID:      videoID,
		ContentCheck: true,
		RacyCheck:    true,
	}

	authenticated := client.HasAuth()
	modes := []bool{false}
	if authenticated {
		modes = []bool{true, false}
	}

	var lastErr error
	for _, useAuth := range modes {
		// A fresh anonymous visitor often clears bot challenges. Minting one is
		// cheap insurance for the guest pass; authenticated requests keep using
		// the session's own visitor data, so the refresh does not leak into them.
		if !useAuth && p.refreshVisitor {
			_ = client.RefreshVisitor(ctx)
		}

		streamURL, mimeType, err := p.tryClients(ctx, client, payload, useAuth)
		if err == nil {
			return streamURL, mimeType, nil
		}
		lastErr = err
		if useAuth {
			log.Printf("[YTM] authenticated clients failed for %s, retrying anonymously: %v", videoID, err)
		}
	}

	if lastErr == nil {
		lastErr = errors.New("no player client produced a stream")
	}
	return "", "", lastErr
}

// tryClients walks the spoofed client rotation once, optionally attaching the
// imported session, and returns the first byte-verified audio stream.
func (p *PlayerService) tryClients(ctx context.Context, client *Client, payload RequestPayload, useAuth bool) (string, string, error) {
	var lastErr error
	for _, candidate := range playableClients {
		var resp playerResponse
		err := client.doPlayerWithClient(ctx, candidate, payload, &resp, useAuth)
		if err != nil {
			lastErr = err
			continue
		}

		if status := resp.PlayabilityStatus.Status; status != "" && status != "OK" {
			reason := resp.PlayabilityStatus.Reason
			if reason == "" {
				reason = "unknown"
			}
			lastErr = NewPlayabilityError(status, reason)
			continue
		}

		format := SelectAudioFormat(resp.StreamingData.AdaptiveFormats)
		if format == nil {
			format = SelectAudioFormat(resp.StreamingData.Formats)
		}
		if format == nil {
			lastErr = errors.New("no playable audio format in player response")
			continue
		}
		if format.URL == "" {
			lastErr = fmt.Errorf("audio format itag %d has no direct url (signature deciphering required)", format.Itag)
			continue
		}

		if p.validateURL != nil {
			if err := p.validateURL(ctx, format.URL); err != nil {
				lastErr = fmt.Errorf("stream url validation failed for itag %d: %w", format.Itag, err)
				continue
			}
		}

		mode := "guest"
		if useAuth {
			mode = "session"
		}
		log.Printf("[YTM] stream resolved via %s (itag %d, %s)", candidate.Name, format.Itag, mode)
		return format.URL, format.MimeType, nil
	}

	if lastErr == nil {
		lastErr = errors.New("no player client produced a stream")
	}
	return "", "", lastErr
}

// SelectAudioFormat picks the best audio-only format from a list of adaptive
// formats. Preference order:
//  1. Opus in WebM (itag 251) — ~160kbps, 48kHz.
//  2. AAC in MP4 (itag 140) — ~128kbps, 44.1kHz.
//  3. Lowest itag audio format (as a last resort).
//
// Non-audio formats (video) are skipped.
func SelectAudioFormat(formats []playerFormat) *playerFormat {
	var opus, aac, fallback *playerFormat
	for i := range formats {
		f := &formats[i]
		if !isAudioMime(f.MimeType) {
			continue
		}
		switch {
		case f.Itag == 251 || (strings.HasPrefix(f.MimeType, "audio/webm") && (f.Itag == 0 || f.Itag == 251)):
			if opus == nil {
				opus = f
			}
		case f.Itag == 140 || strings.HasPrefix(f.MimeType, "audio/mp4"):
			if aac == nil {
				aac = f
			}
		default:
			if fallback == nil {
				fallback = f
			}
		}
	}
	if opus != nil {
		return opus
	}
	if aac != nil {
		return aac
	}
	return fallback
}

func isAudioMime(mime string) bool {
	return strings.HasPrefix(mime, "audio/")
}

// PlayabilityError reports a video that InnerTube refuses to serve audio for
// (age-gated, region-blocked, removed, etc.).
type PlayabilityError struct {
	Status string
	Reason string
}

func (e *PlayabilityError) Error() string {
	return fmt.Sprintf("playability status %s: %s", e.Status, e.Reason)
}

// NewPlayabilityError wraps a non-OK status into a *PlayabilityError.
func NewPlayabilityError(status, reason string) error {
	return &PlayabilityError{Status: status, Reason: reason}
}

var streamProbeClient = &http.Client{Timeout: 10 * time.Second}

// validateStreamURL confirms the CDN actually serves bytes before the player
// is handed the URL. Some clients return a syntactically valid but dead URL.
func validateStreamURL(ctx context.Context, streamURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", "bytes=0-4095")
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36")

	resp, err := streamProbeClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("CDN returned HTTP %d", resp.StatusCode)
	}

	var probe [512]byte
	n, err := resp.Body.Read(probe[:])
	if n == 0 && err != nil {
		return fmt.Errorf("CDN returned no bytes: %w", err)
	}
	return nil
}

// findYTDLP locates an optional yt-dlp binary. CHORDLY_YTDLP overrides PATH.
func findYTDLP() string {
	if configured := os.Getenv("CHORDLY_YTDLP"); configured != "" {
		if info, err := os.Stat(configured); err == nil && !info.IsDir() {
			return configured
		}
	}
	if path, err := exec.LookPath("yt-dlp"); err == nil {
		return path
	}
	return ""
}

// shouldFallbackToYTDLP reports whether an InnerTube failure is likely to be
// solved by the external extractor (sign-in/HTTP gating rather than a bad ID).
func shouldFallbackToYTDLP(err error) bool {
	if err == nil {
		return false
	}
	var playability *PlayabilityError
	if errors.As(err, &playability) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "status 400") ||
		strings.Contains(msg, "status 401") ||
		strings.Contains(msg, "status 403")
}

// resolveWithYTDLP asks yt-dlp for a direct audio URL. No cookie file is
// passed: yt-dlp uses its own client/PO-token handling, and fallback only runs
// after InnerTube has failed.
func (p *PlayerService) resolveWithYTDLP(ctx context.Context, videoID string) (string, string, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, p.ytDlpPath,
		"--no-warnings",
		"--no-playlist",
		"--no-progress",
		"-f", "bestaudio[acodec=opus]/bestaudio",
		"-g",
		"--", "https://music.youtube.com/watch?v="+videoID,
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			return "", "", fmt.Errorf("yt-dlp: %w", err)
		}
		return "", "", fmt.Errorf("yt-dlp: %w: %s", err, detail)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return "", "", errors.New("yt-dlp returned no stream URL")
	}

	streamURL := strings.TrimSpace(lines[0])
	mimeType := "audio/webm"
	if strings.Contains(streamURL, "mime=audio%2Fmp4") || strings.Contains(streamURL, "mime=audio/mp4") {
		mimeType = "audio/mp4"
	}
	if p.validateURL != nil {
		if err := p.validateURL(ctx, streamURL); err != nil {
			return "", "", fmt.Errorf("yt-dlp URL validation failed: %w", err)
		}
	}
	return streamURL, mimeType, nil
}
