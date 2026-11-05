// Package ytm provides YouTube Music InnerTube communication, models, and token ingestion.
//
// UI-AUTH-01: Google Sign-In & InnerTune Token Bundle Parser.
// Implements panic-free, fixture-driven parsing of web session credentials from:
//   - InnerTune session exports
//   - Raw Cookie headers (with or without "Cookie:" prefix)
//   - Netscape cookies.txt files
//   - JSON token bundles and cookie arrays
//   - Base64-encoded bundles
package ytm

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

const maxInputSize = 2 * 1024 * 1024 // 2 MiB cap

var (
	// ErrEmpty is returned when input is empty or whitespace only.
	ErrEmpty = errors.New("ytm/token: input is empty")
	// ErrInputTooLarge is returned when input exceeds 2 MiB.
	ErrInputTooLarge = errors.New("ytm/token: input exceeds 2 MiB limit")
	// ErrOAuthTokenUnsupported is returned when an OAuth ya29.* bearer token is passed.
	ErrOAuthTokenUnsupported = errors.New("ytm/token: OAuth access tokens (ya29.*) are not supported; YouTube Music requires web session cookies")
	// ErrUnrecognized is returned when the input format cannot be identified.
	ErrUnrecognized = errors.New("ytm/token: unrecognized token or cookie format")
	// ErrMissingRequiredCookies is returned when neither SAPISID nor __Secure-3PAPISID is present.
	ErrMissingRequiredCookies = errors.New("ytm/token: missing required authentication cookies (SAPISID or __Secure-3PAPISID)")
)

// TokenSource indicates the detected origin format of the token bundle.
type TokenSource string

const (
	TokenSourceInnerTune    TokenSource = "innertune"
	TokenSourceCookiesTxt   TokenSource = "cookies_txt"
	TokenSourceCookieHeader TokenSource = "cookie_header"
	TokenSourceJSON         TokenSource = "json"
	TokenSourceBase64       TokenSource = "base64"
)

// TokenBundle represents a parsed, normalized YouTube Music session credential set.
type TokenBundle struct {
	Cookie             string            `json:"cookie"`
	Authorization      string            `json:"authorization,omitempty"`
	VisitorData        string            `json:"visitor_data,omitempty"`
	SAPISID            string            `json:"sapisid,omitempty"`
	Source             TokenSource       `json:"source"`
	AccountName        string            `json:"account_name,omitempty"`
	Cookies            map[string]string `json:"-"`
	MissingRecommended []string          `json:"-"`
}

// MaskedSAPISID returns a masked representation of the SAPISID (e.g. ••••AB12).
func (b *TokenBundle) MaskedSAPISID() string {
	if b == nil || b.SAPISID == "" {
		return "none"
	}
	if len(b.SAPISID) <= 4 {
		return "••••"
	}
	return "••••" + b.SAPISID[len(b.SAPISID)-4:]
}

// Summary returns a brief human-readable overview of the bundle.
func (b *TokenBundle) Summary() string {
	if b == nil {
		return "No session active"
	}
	src := string(b.Source)
	if src == "" {
		src = "custom"
	}
	return fmt.Sprintf("%d cookies (%s), SAPISID: %s", len(b.Cookies), src, b.MaskedSAPISID())
}

var recommendedCookies = []string{
	"__Secure-3PSID",
	"__Secure-3PSIDTS",
	"SID",
	"HSID",
	"SSID",
	"APISID",
	"LOGIN_INFO",
}

// ParseToken parses, validates, and normalizes a token or cookie string into a TokenBundle.
func ParseToken(raw []byte) (*TokenBundle, error) {
	if len(raw) > maxInputSize {
		return nil, ErrInputTooLarge
	}

	// 1. Trim UTF-8 BOM and leading/trailing whitespace
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, ErrEmpty
	}

	// 2. Reject OAuth tokens explicitly with actionable error message
	if bytes.HasPrefix(trimmed, []byte("ya29.")) {
		return nil, ErrOAuthTokenUnsupported
	}

	// 3. Detect and decode base64 if present
	if looksLikeBase64(trimmed) {
		decoded, err := decodeBase64(trimmed)
		if err == nil && len(decoded) > 0 {
			bundle, err := parseInner(decoded)
			if err == nil {
				if bundle.Source == TokenSourceCookieHeader {
					bundle.Source = TokenSourceBase64
				}
				return bundle, nil
			}
		}
	}

	return parseInner(trimmed)
}

func parseInner(data []byte) (*TokenBundle, error) {
	// JSON bundle or array
	if data[0] == '{' || data[0] == '[' {
		return parseJSON(data)
	}

	// Netscape cookies.txt
	if isNetscapeFormat(data) {
		return parseNetscape(data)
	}

	// Cookie string / header
	return parseCookieHeader(data)
}

// -----------------------------------------------------------------------------
// JSON Adapter
// -----------------------------------------------------------------------------

func parseJSON(data []byte) (*TokenBundle, error) {
	// Try parsing as array of cookie objects: [{name, value, domain}, ...]
	var cookieArray []map[string]any
	if err := json.Unmarshal(data, &cookieArray); err == nil && len(cookieArray) > 0 {
		cookies := make(map[string]string)
		for _, item := range cookieArray {
			name := getMapString(item, "name", "Name")
			val := getMapString(item, "value", "Value")
			if name != "" {
				cookies[name] = val
			}
		}
		if len(cookies) > 0 {
			return buildBundleFromCookies(cookies, TokenSourceJSON)
		}
	}

	// Try parsing as JSON object bundle: {"cookie": "...", "visitorData": "..."}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON syntax: %v", ErrUnrecognized, err)
	}

	rawCookie := getMapString(obj, "cookie", "cookieHeader", "cookies", "session", "token")
	visitorData := getMapString(obj, "visitorData", "visitor_data", "visitorId")
	authHeader := getMapString(obj, "authorization", "auth")
	accountName := getMapString(obj, "accountName", "name", "account_name")
	sourceStr := getMapString(obj, "source")

	source := TokenSourceJSON
	if strings.EqualFold(sourceStr, string(TokenSourceInnerTune)) {
		source = TokenSourceInnerTune
	}

	if rawCookie != "" {
		bundle, err := parseCookieString(rawCookie, source)
		if err != nil {
			return nil, err
		}
		if visitorData != "" {
			bundle.VisitorData = visitorData
		}
		if authHeader != "" {
			bundle.Authorization = authHeader
		}
		if accountName != "" {
			bundle.AccountName = accountName
		}
		return bundle, nil
	}

	// Or individual cookie fields inside the JSON object
	cookies := make(map[string]string)
	for k, v := range obj {
		if s, ok := v.(string); ok {
			cookies[k] = s
		}
	}
	bundle, err := buildBundleFromCookies(cookies, source)
	if err != nil {
		return nil, err
	}
	if visitorData != "" {
		bundle.VisitorData = visitorData
	}
	if accountName != "" {
		bundle.AccountName = accountName
	}
	return bundle, nil
}

func getMapString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		// Exact match
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		// Case-insensitive match
		for mk, mv := range m {
			if strings.EqualFold(mk, k) {
				if s, ok := mv.(string); ok {
					return s
				}
			}
		}
	}
	return ""
}

// -----------------------------------------------------------------------------
// Netscape cookies.txt Adapter
// -----------------------------------------------------------------------------

func isNetscapeFormat(data []byte) bool {
	s := string(data)
	if strings.Contains(s, "Netscape HTTP Cookie File") {
		return true
	}
	// Check for tab-separated rows with domain
	lines := strings.Split(s, "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if len(l) == 0 || strings.HasPrefix(l, "#") && !strings.HasPrefix(l, "#HttpOnly_") {
			continue
		}
		parts := strings.Split(l, "\t")
		if len(parts) >= 7 {
			return true
		}
	}
	return false
}

func parseNetscape(data []byte) (*TokenBundle, error) {
	lines := strings.Split(string(data), "\n")
	cookies := make(map[string]string)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) == 0 || strings.HasPrefix(line, "# ") {
			continue
		}
		if strings.HasPrefix(line, "#HttpOnly_") {
			line = strings.TrimPrefix(line, "#HttpOnly_")
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 7 {
			continue
		}

		domain := strings.ToLower(parts[0])
		if !strings.Contains(domain, "youtube.com") && !strings.Contains(domain, "google.com") {
			continue
		}

		name := strings.TrimSpace(parts[5])
		val := strings.TrimSpace(parts[6])
		if name != "" {
			cookies[name] = val
		}
	}

	if len(cookies) == 0 {
		return nil, fmt.Errorf("%w: no valid YouTube/Google cookies found in cookies.txt", ErrUnrecognized)
	}

	return buildBundleFromCookies(cookies, TokenSourceCookiesTxt)
}

// -----------------------------------------------------------------------------
// Cookie Header Adapter
// -----------------------------------------------------------------------------

func parseCookieHeader(data []byte) (*TokenBundle, error) {
	s := string(data)
	// Strip "Cookie:" or "cookie:" header prefix if present
	if idx := strings.Index(strings.ToLower(s), "cookie:"); idx != -1 {
		s = s[idx+len("cookie:"):]
	}

	source := TokenSourceCookieHeader
	// If it contains signature of InnerTune export
	if strings.Contains(s, "__Secure-3PSID") && strings.Contains(s, "SAPISID") {
		source = TokenSourceInnerTune
	}

	return parseCookieString(s, source)
}

func parseCookieString(s string, source TokenSource) (*TokenBundle, error) {
	header := http.Header{}
	header.Add("Cookie", strings.TrimSpace(s))
	req := http.Request{Header: header}
	parsedCookies := req.Cookies()

	cookies := make(map[string]string)
	for _, c := range parsedCookies {
		if c.Name != "" {
			cookies[c.Name] = c.Value
		}
	}

	// Fallback manual semicolon splitting in case net/http rejected special characters
	if len(cookies) == 0 {
		parts := strings.Split(s, ";")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if idx := strings.Index(part, "="); idx != -1 {
				k := strings.TrimSpace(part[:idx])
				v := strings.TrimSpace(part[idx+1:])
				if k != "" {
					cookies[k] = v
				}
			}
		}
	}

	if len(cookies) == 0 {
		return nil, ErrUnrecognized
	}

	return buildBundleFromCookies(cookies, source)
}

// -----------------------------------------------------------------------------
// Validation & Normalization
// -----------------------------------------------------------------------------

func buildBundleFromCookies(cookies map[string]string, source TokenSource) (*TokenBundle, error) {
	// 1. Check required cookies: SAPISID or __Secure-3PAPISID
	sapisid := cookies["SAPISID"]
	secureSapisid := cookies["__Secure-3PAPISID"]

	if sapisid == "" && secureSapisid != "" {
		sapisid = secureSapisid
		cookies["SAPISID"] = sapisid
	}

	if sapisid == "" {
		return nil, ErrMissingRequiredCookies
	}

	// 2. Check recommended cookies
	var missingRecommended []string
	for _, req := range recommendedCookies {
		if _, ok := cookies[req]; !ok {
			missingRecommended = append(missingRecommended, req)
		}
	}

	// 3. Assemble normalized Cookie header string
	var sb strings.Builder
	first := true
	for k, v := range cookies {
		if !first {
			sb.WriteString("; ")
		}
		sb.WriteString(k)
		sb.WriteString("=")
		sb.WriteString(v)
		first = false
	}

	return &TokenBundle{
		Cookie:             sb.String(),
		SAPISID:            sapisid,
		Source:             source,
		Cookies:            cookies,
		MissingRecommended: missingRecommended,
	}, nil
}

// -----------------------------------------------------------------------------
// Base64 Detection Helper
// -----------------------------------------------------------------------------

func looksLikeBase64(data []byte) bool {
	if len(data) < 16 || len(data)%4 != 0 {
		return false
	}
	for _, b := range data {
		if !((b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '+' || b == '/' || b == '=') {
			return false
		}
	}
	return true
}

func decodeBase64(data []byte) ([]byte, error) {
	decoded := make([]byte, base64.StdEncoding.DecodedLen(len(data)))
	n, err := base64.StdEncoding.Decode(decoded, data)
	if err != nil {
		return nil, err
	}
	return decoded[:n], nil
}
