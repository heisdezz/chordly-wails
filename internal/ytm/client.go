package ytm

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	baseEndpoint       = "https://music.youtube.com/youtubei/v1"
	webRemixClient     = "WEB_REMIX"
	webRemixVer        = "1.20241030.01.00"
	androidMusicClient = "ANDROID_MUSIC"
	androidMusicVer    = "6.45.52"
	ytmOrigin          = "https://music.youtube.com"
)

// Client handles low-level InnerTube API communication.
type Client struct {
	httpClient *http.Client
	mu         sync.RWMutex
	visitorID  string
	auth       *TokenBundle
	now        func() time.Time
}

// NewClient creates a new InnerTube client.
func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 12 * time.Second,
		},
		now: time.Now,
	}
}

// SetAuth applies an imported YouTube Music session to every subsequent
// request. Passing nil clears authentication.
func (c *Client) SetAuth(bundle *TokenBundle) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if bundle == nil {
		c.auth = nil
		return
	}
	copy := *bundle
	c.auth = &copy
}

// ClearAuth removes the active session.
func (c *Client) ClearAuth() {
	c.SetAuth(nil)
}

// HasAuth reports whether a session is attached.
func (c *Client) HasAuth() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.auth != nil
}

func (c *Client) requestState() (visitor string, auth *TokenBundle) {
	return c.requestStateFor(true)
}

// requestStateFor returns the visitor id and attached session for a request.
// When useAuth is false the session's visitor data is ignored as well, so a
// guest retry looks like a genuinely anonymous client rather than a logged-in
// visitor without credentials.
func (c *Client) requestStateFor(useAuth bool) (visitor string, auth *TokenBundle) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	visitor = c.visitorID
	if c.auth == nil {
		return visitor, nil
	}
	copy := *c.auth
	auth = &copy
	if useAuth && auth.VisitorData != "" {
		visitor = auth.VisitorData
	}
	return visitor, auth
}

func (c *Client) applyAuth(req *http.Request, auth *TokenBundle, visitor, origin string, useAuth bool) {
	if visitor != "" {
		req.Header.Set("X-Goog-Visitor-Id", visitor)
	}
	if !useAuth || auth == nil {
		return
	}
	if auth.Cookie != "" {
		req.Header.Set("Cookie", auth.Cookie)
	}

	authorization := auth.Authorization
	if auth.SAPISID != "" {
		now := time.Now
		if c.now != nil {
			now = c.now
		}
		authorization = authorizationHeader(auth, now().Unix(), origin)
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}

	req.Header.Set("X-Goog-AuthUser", "0")
	req.Header.Set("Origin", origin)
	req.Header.Set("X-Origin", origin)
	req.Header.Set("X-Youtube-Bootstrap-Logged-In", "true")
}

// authorizationHeader mirrors yt-dlp: send every SID hash the account exposes.
// Some accounts/clients reject a lone SAPISIDHASH when 1P/3P cookies exist.
func authorizationHeader(bundle *TokenBundle, timestamp int64, origin string) string {
	parts := make([]string, 0, 3)
	add := func(scheme, sid string) {
		if sid == "" {
			return
		}
		parts = append(parts, sidAuthorization(scheme, sid, timestamp, origin))
	}
	add("SAPISIDHASH", bundle.SAPISID)
	add("SAPISID1PHASH", bundle.Cookies["__Secure-1PAPISID"])
	add("SAPISID3PHASH", bundle.Cookies["__Secure-3PAPISID"])
	return strings.Join(parts, " ")
}

// sidAuthorization builds one SID hash scheme value.
func sidAuthorization(scheme, sapisid string, timestamp int64, origin string) string {
	payload := fmt.Sprintf("%d %s %s", timestamp, sapisid, origin)
	sum := sha1.Sum([]byte(payload))
	return fmt.Sprintf("%s %d_%x", scheme, timestamp, sum)
}

// sapisidHash builds the standard Authorization value used by tests and the
// non-1P/3P fallback.
func sapisidHash(sapisid string, timestamp int64) string {
	return sidAuthorization("SAPISIDHASH", sapisid, timestamp, ytmOrigin)
}

// RequestPayload represents the standard YouTube InnerTube request body.
type RequestPayload struct {
	Context      RequestContext `json:"context"`
	Query        string         `json:"query,omitempty"`
	BrowseID     string         `json:"browseId,omitempty"`
	VideoID      string         `json:"videoId,omitempty"`
	PlaylistID   string         `json:"playlistId,omitempty"`
	Continuation string         `json:"continuation,omitempty"`
	Params       string         `json:"params,omitempty"`
	IsAudioOnly  bool           `json:"isAudioOnly,omitempty"`
	ContentCheck bool           `json:"contentCheckOk,omitempty"`
	RacyCheck    bool           `json:"racyCheckOk,omitempty"`
}

type RequestContext struct {
	Client ContextClient `json:"client"`
}

type ContextClient struct {
	ClientName    string `json:"clientName"`
	ClientVersion string `json:"clientVersion"`
	HL            string `json:"hl"`
	GL            string `json:"gl"`
	VisitorData   string `json:"visitorData,omitempty"`

	DeviceMake        string `json:"deviceMake,omitempty"`
	DeviceModel       string `json:"deviceModel,omitempty"`
	OSName            string `json:"osName,omitempty"`
	OSVersion         string `json:"osVersion,omitempty"`
	AndroidSDKVersion int    `json:"androidSdkVersion,omitempty"`
}

// Do executes a POST request against a YouTube Music InnerTube endpoint.
func (c *Client) Do(ctx context.Context, endpoint string, payload RequestPayload, out interface{}) error {
	visitor, auth := c.requestState()

	payload.Context = RequestContext{
		Client: ContextClient{
			ClientName:    webRemixClient,
			ClientVersion: webRemixVer,
			HL:            "en",
			GL:            "US",
			VisitorData:   visitor,
		},
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload error: %w", err)
	}

	reqURL := fmt.Sprintf("%s/%s", baseEndpoint, endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("create request error: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://music.youtube.com/")
	c.applyAuth(req, auth, visitor, ytmOrigin, true)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("network request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("endpoint %s failed with status %d: %s", endpoint, resp.StatusCode, string(respBytes))
	}

	c.updateVisitor(resp.Header)
	return jsonResponse(resp, out)
}

// playerClientConfig describes one spoofed InnerTube player client. Multiple
// clients are tried because YouTube gates different videos per client.
type playerClientConfig struct {
	Name        string
	Version     string
	UserAgent   string
	DeviceMake  string
	DeviceModel string
	OSName      string
	OSVersion   string
	AndroidSDK  int
	Host        string // "music" (default) or "www"
}

var androidMusicConfig = playerClientConfig{
	Name:       androidMusicClient,
	Version:    androidMusicVer,
	OSName:     "Android",
	OSVersion:  "14",
	AndroidSDK: 34,
	Host:       "music",
}

// doPlayer resolves a `/player` request with the default Android Music client.
func (c *Client) doPlayer(ctx context.Context, payload RequestPayload, out interface{}) error {
	return c.doPlayerWithClient(ctx, androidMusicConfig, payload, out, true)
}

// doPlayerWithClient resolves a `/player` request using a specific spoofed
// client context. useAuth controls whether the imported session is attached.
func (c *Client) doPlayerWithClient(ctx context.Context, cfg playerClientConfig, payload RequestPayload, out interface{}, useAuth bool) error {
	visitor, auth := c.requestStateFor(useAuth)

	payload.Context = RequestContext{
		Client: ContextClient{
			ClientName:        cfg.Name,
			ClientVersion:     cfg.Version,
			HL:                "en",
			GL:                "US",
			VisitorData:       visitor,
			DeviceMake:        cfg.DeviceMake,
			DeviceModel:       cfg.DeviceModel,
			OSName:            cfg.OSName,
			OSVersion:         cfg.OSVersion,
			AndroidSDKVersion: cfg.AndroidSDK,
		},
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload error: %w", err)
	}

	host := ytmOrigin
	if cfg.Host == "www" {
		host = "https://www.youtube.com"
	}
	reqURL := host + "/youtubei/v1/player"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("create request error: %w", err)
	}

	ua := cfg.UserAgent
	if ua == "" {
		ua = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36"
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Referer", host+"/")
	c.applyAuth(req, auth, visitor, host, useAuth)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("network request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("endpoint player failed with status %d: %s", resp.StatusCode, string(respBytes))
	}

	c.updateVisitor(resp.Header)
	return jsonResponse(resp, out)
}

var visitorDataPattern = regexp.MustCompile(`Cg[A-Za-z0-9_-]{40,}`)

// RefreshVisitor mints an anonymous visitor id from YouTube's service-worker
// data. A fresh visitor often clears "confirm you're not a bot" responses.
func (c *Client) RefreshVisitor(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.youtube.com/sw.js_data", nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("visitor mint failed with status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	matches := visitorDataPattern.FindAll(data, -1)
	if len(matches) == 0 {
		return errors.New("visitor mint: no visitorData found")
	}

	best := matches[0]
	for _, match := range matches[1:] {
		if len(match) > len(best) {
			best = match
		}
	}

	c.mu.Lock()
	c.visitorID = string(best)
	c.mu.Unlock()
	return nil
}

// updateVisitor persists the X-Goog-Visitor-Id header for subsequent requests.
func (c *Client) updateVisitor(h http.Header) {
	if v := h.Get("X-Goog-Visitor-Id"); v != "" {
		c.mu.Lock()
		c.visitorID = v
		c.mu.Unlock()
	}
}

func jsonResponse(resp *http.Response, out interface{}) error {
	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response error: %w", err)
	}
	if out != nil {
		if err := json.Unmarshal(respData, out); err != nil {
			return fmt.Errorf("unmarshal response error: %w", err)
		}
	}
	return nil
}
