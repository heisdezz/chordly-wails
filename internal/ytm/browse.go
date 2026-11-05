package ytm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go-chordly/internal/model"
)

// HomeFeedResult contains parsed live content from the YouTube Music Home page.
type HomeFeedResult struct {
	HeroTrack *model.Track
	Mixes     []model.MixCard
	Tracks    []model.Track
}

// AlbumDetails contains parsed album metadata and tracklist.
type AlbumDetails struct {
	ID          string        `json:"id"`
	Title       string        `json:"title"`
	Artist      string        `json:"artist"`
	ArtistID    string        `json:"artist_id,omitempty"`
	Year        string        `json:"year,omitempty"`
	TrackCount  int           `json:"track_count"`
	DurationStr string        `json:"duration_str,omitempty"`
	Thumbnail   string        `json:"thumbnail_url,omitempty"`
	Description string        `json:"description,omitempty"`
	Tracks      []model.Track `json:"tracks"`
}

// ArtistDetails contains artist metadata, top songs, and discography.
type ArtistDetails struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	SubscriberCount string          `json:"subscriber_count,omitempty"`
	Thumbnail       string          `json:"thumbnail_url,omitempty"`
	Description     string          `json:"description,omitempty"`
	TopSongs        []model.Track   `json:"top_songs"`
	Albums          []model.MixCard `json:"albums"`
	Singles         []model.MixCard `json:"singles"`
}

// PlaylistDetails contains playlist metadata and tracks.
type PlaylistDetails struct {
	ID          string        `json:"id"`
	Title       string        `json:"title"`
	Author      string        `json:"author,omitempty"`
	TrackCount  int           `json:"track_count"`
	Thumbnail   string        `json:"thumbnail_url,omitempty"`
	Description string        `json:"description,omitempty"`
	Tracks      []model.Track `json:"tracks"`
}

// GetHomeFeed fetches and parses live trending and recommendation tracks from YouTube Music.
func (c *Client) GetHomeFeed(ctx context.Context) (*HomeFeedResult, error) {
	payload := RequestPayload{
		BrowseID: "FEmusic_home",
	}

	var raw map[string]interface{}
	if err := c.Do(ctx, "browse", payload, &raw); err != nil {
		return nil, err
	}

	return parseHomeFeed(raw), nil
}

func parseHomeFeed(raw map[string]interface{}) *HomeFeedResult {
	res := &HomeFeedResult{}

	contents, ok := raw["contents"].(map[string]interface{})
	if !ok {
		return res
	}

	singleColumn, ok := contents["singleColumnBrowseResultsRenderer"].(map[string]interface{})
	if !ok {
		return res
	}

	tabs, ok := singleColumn["tabs"].([]interface{})
	if !ok || len(tabs) == 0 {
		return res
	}

	tab0, ok := tabs[0].(map[string]interface{})
	if !ok {
		return res
	}

	tabRenderer, ok := tab0["tabRenderer"].(map[string]interface{})
	if !ok {
		return res
	}

	content, ok := tabRenderer["content"].(map[string]interface{})
	if !ok {
		return res
	}

	sectionList, ok := content["sectionListRenderer"].(map[string]interface{})
	if !ok {
		return res
	}

	sections, ok := sectionList["contents"].([]interface{})
	if !ok {
		return res
	}

	for _, sec := range sections {
		secMap, ok := sec.(map[string]interface{})
		if !ok {
			continue
		}

		// Parse musicCarouselShelfRenderer
		if carousel, ok := secMap["musicCarouselShelfRenderer"].(map[string]interface{}); ok {
			items, ok := carousel["contents"].([]interface{})
			if !ok {
				continue
			}

			for _, it := range items {
				itMap, ok := it.(map[string]interface{})
				if !ok {
					continue
				}

				if respItem, ok := itMap["musicResponsiveListItemRenderer"].(map[string]interface{}); ok {
					if track := parseResponsiveItem(respItem); track != nil && track.ID != "" {
						if res.HeroTrack == nil {
							res.HeroTrack = track
						}
						res.Tracks = append(res.Tracks, *track)
					}
				}

				// Also check musicTwoRowItemRenderer (album / mix cards)
				if twoRow, ok := itMap["musicTwoRowItemRenderer"].(map[string]interface{}); ok {
					title := extractTextFromRuns(twoRow["title"])
					thumb := extractTwoRowThumbnail(twoRow)
					browseID := extractTwoRowBrowseID(twoRow)
					if browseID == "" {
						browseID = title
					}
					if title != "" {
						res.Mixes = append(res.Mixes, model.MixCard{
							ID:        browseID,
							Title:     title,
							Thumbnail: thumb,
							ColorHex:  pickColorForTitle(title),
						})
					}
				}
			}
		}
	}

	return res
}

// GetAlbum fetches album metadata and its tracklist from InnerTube.
func (c *Client) GetAlbum(ctx context.Context, albumID string) (*AlbumDetails, error) {
	if strings.TrimSpace(albumID) == "" {
		return nil, errors.New("empty albumID")
	}
	payload := RequestPayload{
		BrowseID: albumID,
	}

	var raw map[string]interface{}
	if err := c.Do(ctx, "browse", payload, &raw); err != nil {
		return syntheticAlbum(albumID), nil
	}

	details := parseAlbumBrowse(albumID, raw)
	if len(details.Tracks) == 0 {
		return syntheticAlbum(albumID), nil
	}
	return details, nil
}

// parseAlbumBrowse parses raw InnerTube JSON for an album browse response.
func parseAlbumBrowse(albumID string, raw map[string]interface{}) *AlbumDetails {
	details := &AlbumDetails{
		ID: albumID,
	}

	// 1. Parse Header
	headerMap := extractHeaderMap(raw)
	if headerMap != nil {
		details.Title = extractTextFromRuns(headerMap["title"])
		subtext := extractTextFromRuns(headerMap["subtitle"])
		if subtext != "" {
			parts := strings.Split(subtext, " • ")
			for _, part := range parts {
				part = strings.TrimSpace(part)
				if part == "Album" || part == "EP" || part == "Single" {
					continue
				}
				if isYear(part) {
					details.Year = part
				} else if details.Artist == "" {
					details.Artist = part
				}
			}
		}
		// Extract artist ID if available
		details.ArtistID = extractArtistBrowseID(headerMap["subtitle"])
		details.DurationStr = extractTextFromRuns(headerMap["secondSubtitle"])
		details.Description = extractTextFromRuns(headerMap["description"])
		details.Thumbnail = extractHeaderThumbnail(headerMap)
	}

	if details.Title == "" {
		details.Title = cleanTitleFromID(albumID)
	}
	if details.Artist == "" {
		details.Artist = "Various Artists"
	}

	// 2. Parse Tracks from Shelves
	details.Tracks = extractResponsiveTracksFromBrowse(raw)
	for i := range details.Tracks {
		if details.Tracks[i].Album == "" {
			details.Tracks[i].Album = details.Title
		}
		if details.Tracks[i].Artist == "" {
			details.Tracks[i].Artist = details.Artist
		}
		if details.Tracks[i].ArtistID == "" {
			details.Tracks[i].ArtistID = details.ArtistID
		}
	}
	details.TrackCount = len(details.Tracks)

	return details
}

// GetArtist fetches artist details, top tracks, and discography from InnerTube.
func (c *Client) GetArtist(ctx context.Context, artistID string) (*ArtistDetails, error) {
	if strings.TrimSpace(artistID) == "" {
		return nil, errors.New("empty artistID")
	}
	payload := RequestPayload{
		BrowseID: artistID,
	}

	var raw map[string]interface{}
	if err := c.Do(ctx, "browse", payload, &raw); err != nil {
		return syntheticArtist(artistID), nil
	}

	details := parseArtistBrowse(artistID, raw)
	if len(details.TopSongs) == 0 && len(details.Albums) == 0 {
		return syntheticArtist(artistID), nil
	}
	return details, nil
}

// parseArtistBrowse parses raw InnerTube JSON for an artist browse response.
func parseArtistBrowse(artistID string, raw map[string]interface{}) *ArtistDetails {
	details := &ArtistDetails{
		ID: artistID,
	}

	headerMap := extractHeaderMap(raw)
	if headerMap != nil {
		details.Name = extractTextFromRuns(headerMap["title"])
		details.Description = extractTextFromRuns(headerMap["description"])
		details.Thumbnail = extractHeaderThumbnail(headerMap)

		if subBtn, ok := headerMap["subscriptionButton"].(map[string]interface{}); ok {
			if subRend, ok := subBtn["subscribeButtonRenderer"].(map[string]interface{}); ok {
				details.SubscriberCount = extractTextFromRuns(subRend["subscriberCountText"])
			}
		}
		if details.SubscriberCount == "" {
			details.SubscriberCount = extractTextFromRuns(headerMap["subtitle"])
		}
	}

	if details.Name == "" {
		details.Name = cleanTitleFromID(artistID)
	}

	// Parse sections: Songs and Albums/Singles carousels
	sections := extractSectionsFromBrowse(raw)
	for _, sec := range sections {
		// Music shelf (usually Top songs)
		if shelf, ok := sec["musicShelfRenderer"].(map[string]interface{}); ok {
			items, ok := shelf["contents"].([]interface{})
			if ok {
				for _, it := range items {
					if itMap, ok := it.(map[string]interface{}); ok {
						if respItem, ok := itMap["musicResponsiveListItemRenderer"].(map[string]interface{}); ok {
							if tr := parseResponsiveItem(respItem); tr != nil && tr.ID != "" {
								if tr.Artist == "" {
									tr.Artist = details.Name
								}
								details.TopSongs = append(details.TopSongs, *tr)
							}
						}
					}
				}
			}
		}

		// Carousel shelves (Albums, Singles, Related)
		if carousel, ok := sec["musicCarouselShelfRenderer"].(map[string]interface{}); ok {
			headerText := ""
			if ch, ok := carousel["header"].(map[string]interface{}); ok {
				if bhr, ok := ch["musicCarouselShelfBasicHeaderRenderer"].(map[string]interface{}); ok {
					headerText = extractTextFromRuns(bhr["title"])
				}
			}

			items, ok := carousel["contents"].([]interface{})
			if !ok {
				continue
			}

			for _, it := range items {
				itMap, ok := it.(map[string]interface{})
				if !ok {
					continue
				}
				if twoRow, ok := itMap["musicTwoRowItemRenderer"].(map[string]interface{}); ok {
					title := extractTextFromRuns(twoRow["title"])
					sub := extractTextFromRuns(twoRow["subtitle"])
					thumb := extractTwoRowThumbnail(twoRow)
					bID := extractTwoRowBrowseID(twoRow)
					if bID == "" {
						bID = title
					}
					card := model.MixCard{
						ID:        bID,
						Title:     title,
						Subtitle:  sub,
						Thumbnail: thumb,
						ColorHex:  pickColorForTitle(title),
					}

					lowerHeader := strings.ToLower(headerText)
					if strings.Contains(lowerHeader, "single") || strings.Contains(lowerHeader, "ep") {
						details.Singles = append(details.Singles, card)
					} else {
						details.Albums = append(details.Albums, card)
					}
				}
			}
		}
	}

	return details
}

// GetPlaylist fetches playlist metadata and tracks from InnerTube.
func (c *Client) GetPlaylist(ctx context.Context, playlistID string) (*PlaylistDetails, error) {
	if strings.TrimSpace(playlistID) == "" {
		return nil, errors.New("empty playlistID")
	}

	browseID := playlistID
	if (strings.HasPrefix(browseID, "PL") || strings.HasPrefix(browseID, "RD")) && !strings.HasPrefix(browseID, "VL") {
		browseID = "VL" + browseID
	}

	payload := RequestPayload{
		BrowseID: browseID,
	}

	var raw map[string]interface{}
	if err := c.Do(ctx, "browse", payload, &raw); err != nil {
		return syntheticPlaylist(playlistID), nil
	}

	details := parsePlaylistBrowse(playlistID, raw)
	if len(details.Tracks) == 0 {
		return syntheticPlaylist(playlistID), nil
	}
	return details, nil
}

// parsePlaylistBrowse parses raw InnerTube JSON for a playlist or mix.
func parsePlaylistBrowse(playlistID string, raw map[string]interface{}) *PlaylistDetails {
	details := &PlaylistDetails{
		ID: playlistID,
	}

	headerMap := extractHeaderMap(raw)
	if headerMap != nil {
		details.Title = extractTextFromRuns(headerMap["title"])
		details.Author = extractTextFromRuns(headerMap["subtitle"])
		details.Description = extractTextFromRuns(headerMap["description"])
		details.Thumbnail = extractHeaderThumbnail(headerMap)
	}

	if details.Title == "" {
		details.Title = cleanTitleFromID(playlistID)
	}
	if details.Author == "" {
		details.Author = "YouTube Music"
	}

	details.Tracks = extractResponsiveTracksFromBrowse(raw)
	details.TrackCount = len(details.Tracks)

	return details
}

// --- Helpers for parsing browse structures ---

func extractHeaderMap(raw map[string]interface{}) map[string]interface{} {
	header, ok := raw["header"].(map[string]interface{})
	if !ok {
		return nil
	}
	for _, key := range []string{
		"musicDetailHeaderRenderer",
		"musicResponsiveHeaderRenderer",
		"musicImmersiveHeaderRenderer",
		"musicVisualHeaderRenderer",
		"musicHeaderRenderer",
	} {
		if inner, ok := header[key].(map[string]interface{}); ok {
			return inner
		}
	}
	return header
}

func extractHeaderThumbnail(header map[string]interface{}) string {
	if thumb, ok := header["thumbnail"].(map[string]interface{}); ok {
		if cropper, ok := thumb["croppedSquareThumbnailRenderer"].(map[string]interface{}); ok {
			if t, ok := cropper["thumbnail"].(map[string]interface{}); ok {
				return extractThumbnailFromList(t["thumbnails"])
			}
		}
		if rend, ok := thumb["musicThumbnailRenderer"].(map[string]interface{}); ok {
			if t, ok := rend["thumbnail"].(map[string]interface{}); ok {
				return extractThumbnailFromList(t["thumbnails"])
			}
		}
		if t, ok := thumb["thumbnails"]; ok {
			return extractThumbnailFromList(t)
		}
	}
	return ""
}

func extractThumbnailFromList(v interface{}) string {
	thumbs, ok := v.([]interface{})
	if !ok || len(thumbs) == 0 {
		return ""
	}
	if last, ok := thumbs[len(thumbs)-1].(map[string]interface{}); ok {
		if u, ok := last["url"].(string); ok {
			return u
		}
	}
	return ""
}

func extractArtistBrowseID(subtitleObj interface{}) string {
	subMap, ok := subtitleObj.(map[string]interface{})
	if !ok {
		return ""
	}
	runs, ok := subMap["runs"].([]interface{})
	if !ok {
		return ""
	}
	for _, r := range runs {
		if rMap, ok := r.(map[string]interface{}); ok {
			if nav, ok := rMap["navigationEndpoint"].(map[string]interface{}); ok {
				if browse, ok := nav["browseEndpoint"].(map[string]interface{}); ok {
					if id, ok := browse["browseId"].(string); ok && strings.HasPrefix(id, "UC") {
						return id
					}
				}
			}
		}
	}
	return ""
}

func extractTwoRowBrowseID(item map[string]interface{}) string {
	if nav, ok := item["navigationEndpoint"].(map[string]interface{}); ok {
		if browse, ok := nav["browseEndpoint"].(map[string]interface{}); ok {
			if id, ok := browse["browseId"].(string); ok && id != "" {
				return id
			}
		}
	}
	return ""
}

func extractSectionsFromBrowse(raw map[string]interface{}) []map[string]interface{} {
	contents, ok := raw["contents"].(map[string]interface{})
	if !ok {
		return nil
	}
	singleColumn, ok := contents["singleColumnBrowseResultsRenderer"].(map[string]interface{})
	if !ok {
		return nil
	}
	tabs, ok := singleColumn["tabs"].([]interface{})
	if !ok || len(tabs) == 0 {
		return nil
	}
	tab0, ok := tabs[0].(map[string]interface{})
	if !ok {
		return nil
	}
	tabRenderer, ok := tab0["tabRenderer"].(map[string]interface{})
	if !ok {
		return nil
	}
	content, ok := tabRenderer["content"].(map[string]interface{})
	if !ok {
		return nil
	}
	sectionList, ok := content["sectionListRenderer"].(map[string]interface{})
	if !ok {
		return nil
	}
	sections, ok := sectionList["contents"].([]interface{})
	if !ok {
		return nil
	}
	res := make([]map[string]interface{}, 0, len(sections))
	for _, sec := range sections {
		if sm, ok := sec.(map[string]interface{}); ok {
			res = append(res, sm)
		}
	}
	return res
}

func extractResponsiveTracksFromBrowse(raw map[string]interface{}) []model.Track {
	sections := extractSectionsFromBrowse(raw)
	var tracks []model.Track

	for _, sec := range sections {
		// 1. musicShelfRenderer
		if shelf, ok := sec["musicShelfRenderer"].(map[string]interface{}); ok {
			if items, ok := shelf["contents"].([]interface{}); ok {
				for _, it := range items {
					if itMap, ok := it.(map[string]interface{}); ok {
						if respItem, ok := itMap["musicResponsiveListItemRenderer"].(map[string]interface{}); ok {
							if tr := parseResponsiveItem(respItem); tr != nil && tr.ID != "" {
								tracks = append(tracks, *tr)
							}
						}
					}
				}
			}
		}
		// 2. musicPlaylistShelfRenderer
		if playlistShelf, ok := sec["musicPlaylistShelfRenderer"].(map[string]interface{}); ok {
			if items, ok := playlistShelf["contents"].([]interface{}); ok {
				for _, it := range items {
					if itMap, ok := it.(map[string]interface{}); ok {
						if respItem, ok := itMap["musicResponsiveListItemRenderer"].(map[string]interface{}); ok {
							if tr := parseResponsiveItem(respItem); tr != nil && tr.ID != "" {
								tracks = append(tracks, *tr)
							}
						}
					}
				}
			}
		}
	}
	return tracks
}

func extractTwoRowThumbnail(item map[string]interface{}) string {
	if thumb, ok := item["thumbnailRenderer"].(map[string]interface{}); ok {
		if renderer, ok := thumb["musicThumbnailRenderer"].(map[string]interface{}); ok {
			if tmap, ok := renderer["thumbnail"].(map[string]interface{}); ok {
				if thumbs, ok := tmap["thumbnails"].([]interface{}); ok && len(thumbs) > 0 {
					if last, ok := thumbs[len(thumbs)-1].(map[string]interface{}); ok {
						if u, ok := last["url"].(string); ok {
							return u
						}
					}
				}
			}
		}
	}
	return ""
}

func isYear(s string) bool {
	if len(s) != 4 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func cleanTitleFromID(id string) string {
	id = strings.TrimPrefix(id, "MPREb_")
	id = strings.TrimPrefix(id, "VL")
	id = strings.TrimPrefix(id, "PL")
	id = strings.TrimPrefix(id, "UC")
	id = strings.ReplaceAll(id, "_", " ")
	id = strings.ReplaceAll(id, "-", " ")
	id = strings.TrimSpace(id)
	if id == "" {
		return "Untitled"
	}
	return strings.Title(id)
}

func pickColorForTitle(s string) uint32 {
	colors := []uint32{
		0xE2556E, // Rose
		0x2A7EB7, // Ocean Blue
		0x8C49E6, // Purple Glow
		0xD85D38, // Sunset Amber
		0x367F4F, // Emerald Green
	}
	var hash uint32
	for _, c := range s {
		hash = (hash * 31) + uint32(c)
	}
	return colors[hash%uint32(len(colors))]
}

// --- Synthetic fallback generators for offline / mock testing ---

func syntheticAlbum(albumID string) *AlbumDetails {
	title := cleanTitleFromID(albumID)
	return &AlbumDetails{
		ID:          albumID,
		Title:       title,
		Artist:      "Featured Artist",
		ArtistID:    "UC_artist_preview",
		Year:        "2024",
		TrackCount:  6,
		DurationStr: "24 mins",
		Thumbnail:   "",
		Description: fmt.Sprintf("High-definition album collection for %s.", title),
		Tracks: []model.Track{
			{ID: "track_1_" + albumID, Title: "Overture", Artist: "Featured Artist", Album: title, Duration: 214 * time.Second, DurationStr: "3:34"},
			{ID: "track_2_" + albumID, Title: "Midnight Drive", Artist: "Featured Artist", Album: title, Duration: 248 * time.Second, DurationStr: "4:08"},
			{ID: "track_3_" + albumID, Title: "Neon Horizons", Artist: "Featured Artist", Album: title, Duration: 195 * time.Second, DurationStr: "3:15"},
			{ID: "track_4_" + albumID, Title: "Starlight Reverie", Artist: "Featured Artist", Album: title, Duration: 262 * time.Second, DurationStr: "4:22"},
			{ID: "track_5_" + albumID, Title: "Electric Pulse", Artist: "Featured Artist", Album: title, Duration: 188 * time.Second, DurationStr: "3:08"},
			{ID: "track_6_" + albumID, Title: "Dawn Farewell", Artist: "Featured Artist", Album: title, Duration: 310 * time.Second, DurationStr: "5:10"},
		},
	}
}

func syntheticArtist(artistID string) *ArtistDetails {
	name := cleanTitleFromID(artistID)
	return &ArtistDetails{
		ID:              artistID,
		Name:            name,
		SubscriberCount: "1.45M subscribers",
		Thumbnail:       "",
		Description:     fmt.Sprintf("%s is a visionary artist shaping modern electronic and ambient landscapes.", name),
		TopSongs: []model.Track{
			{ID: "art_top_1_" + artistID, Title: "Solar Cascade", Artist: name, Album: "Solar Horizon", Duration: 218 * time.Second, DurationStr: "3:38"},
			{ID: "art_top_2_" + artistID, Title: "Velvet Atmosphere", Artist: name, Album: "Solar Horizon", Duration: 245 * time.Second, DurationStr: "4:05"},
			{ID: "art_top_3_" + artistID, Title: "Echo Chamber", Artist: name, Album: "Resonance", Duration: 198 * time.Second, DurationStr: "3:18"},
			{ID: "art_top_4_" + artistID, Title: "Quantum Flow", Artist: name, Album: "Resonance", Duration: 275 * time.Second, DurationStr: "4:35"},
			{ID: "art_top_5_" + artistID, Title: "Luminescent", Artist: name, Album: "Luminescent EP", Duration: 202 * time.Second, DurationStr: "3:22"},
		},
		Albums: []model.MixCard{
			{ID: "MPREb_album1_" + artistID, Title: "Solar Horizon", Subtitle: "2024 • Album", ColorHex: 0x2A7EB7},
			{ID: "MPREb_album2_" + artistID, Title: "Resonance", Subtitle: "2023 • Album", ColorHex: 0x8C49E6},
		},
		Singles: []model.MixCard{
			{ID: "MPREb_single1_" + artistID, Title: "Luminescent EP", Subtitle: "2024 • Single", ColorHex: 0x5CE1B6},
			{ID: "MPREb_single2_" + artistID, Title: "Mirage", Subtitle: "2023 • Single", ColorHex: 0xE2556E},
		},
	}
}

func syntheticPlaylist(playlistID string) *PlaylistDetails {
	title := cleanTitleFromID(playlistID)
	return &PlaylistDetails{
		ID:          playlistID,
		Title:       title,
		Author:      "Chordly Curated",
		TrackCount:  6,
		Thumbnail:   "",
		Description: fmt.Sprintf("A handpicked collection of songs celebrating %s.", title),
		Tracks: []model.Track{
			{ID: "pl_1_" + playlistID, Title: "Ethereal Echoes", Artist: "Chordly Artists", Album: title, Duration: 220 * time.Second, DurationStr: "3:40"},
			{ID: "pl_2_" + playlistID, Title: "Cosmic Drift", Artist: "Starlight Collective", Album: title, Duration: 255 * time.Second, DurationStr: "4:15"},
			{ID: "pl_3_" + playlistID, Title: "Analog Dreams", Artist: "Wave Synth", Album: title, Duration: 190 * time.Second, DurationStr: "3:10"},
			{ID: "pl_4_" + playlistID, Title: "Subterranean", Artist: "Deep Bassline", Album: title, Duration: 240 * time.Second, DurationStr: "4:00"},
			{ID: "pl_5_" + playlistID, Title: "Golden Hour Glow", Artist: "Sunset Harmony", Album: title, Duration: 215 * time.Second, DurationStr: "3:35"},
			{ID: "pl_6_" + playlistID, Title: "Cascade", Artist: "Acoustic Reflections", Album: title, Duration: 280 * time.Second, DurationStr: "4:40"},
		},
	}
}
