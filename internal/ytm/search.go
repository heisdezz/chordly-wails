package ytm

import (
	"context"
	"strings"
	"time"

	"go-chordly/internal/model"
)

// Search executes a live query against YouTube Music and returns parsed tracks.
func (c *Client) Search(ctx context.Context, query string) ([]model.Track, error) {
	payload := RequestPayload{
		Query: query,
	}

	var raw map[string]interface{}
	if err := c.Do(ctx, "search", payload, &raw); err != nil {
		return nil, err
	}

	return parseSearchTracks(raw), nil
}

func parseSearchTracks(raw map[string]interface{}) []model.Track {
	var tracks []model.Track

	contents, ok := raw["contents"].(map[string]interface{})
	if !ok {
		return tracks
	}

	tabbed, ok := contents["tabbedSearchResultsRenderer"].(map[string]interface{})
	if !ok {
		return tracks
	}

	tabs, ok := tabbed["tabs"].([]interface{})
	if !ok || len(tabs) == 0 {
		return tracks
	}

	tab0, ok := tabs[0].(map[string]interface{})
	if !ok {
		return tracks
	}

	tabRenderer, ok := tab0["tabRenderer"].(map[string]interface{})
	if !ok {
		return tracks
	}

	tabContent, ok := tabRenderer["content"].(map[string]interface{})
	if !ok {
		return tracks
	}

	secList, ok := tabContent["sectionListRenderer"].(map[string]interface{})
	if !ok {
		return tracks
	}

	secItems, ok := secList["contents"].([]interface{})
	if !ok {
		return tracks
	}

	for _, sec := range secItems {
		secMap, ok := sec.(map[string]interface{})
		if !ok {
			continue
		}

		// Parse itemSectionRenderer
		if isr, ok := secMap["itemSectionRenderer"].(map[string]interface{}); ok {
			subItems, ok := isr["contents"].([]interface{})
			if !ok || len(subItems) == 0 {
				continue
			}

			for _, sub := range subItems {
				subMap, ok := sub.(map[string]interface{})
				if !ok {
					continue
				}

				if item, ok := subMap["musicResponsiveListItemRenderer"].(map[string]interface{}); ok {
					if track := parseResponsiveItem(item); track != nil && track.ID != "" {
						tracks = append(tracks, *track)
					}
				}
			}
		}

		// Also check musicShelfRenderer if present
		if shelf, ok := secMap["musicShelfRenderer"].(map[string]interface{}); ok {
			shelfItems, ok := shelf["contents"].([]interface{})
			if !ok {
				continue
			}
			for _, si := range shelfItems {
				siMap, ok := si.(map[string]interface{})
				if !ok {
					continue
				}
				if item, ok := siMap["musicResponsiveListItemRenderer"].(map[string]interface{}); ok {
					if track := parseResponsiveItem(item); track != nil && track.ID != "" {
						tracks = append(tracks, *track)
					}
				}
			}
		}
	}

	return tracks
}

func parseResponsiveItem(item map[string]interface{}) *model.Track {
	flex, ok := item["flexColumns"].([]interface{})
	if !ok || len(flex) == 0 {
		return nil
	}

	// Title
	var title string
	if col0, ok := flex[0].(map[string]interface{}); ok {
		if inner, ok := col0["musicResponsiveListItemFlexColumnRenderer"].(map[string]interface{}); ok {
			title = extractTextFromRuns(inner["text"])
		}
	}
	if title == "" {
		return nil
	}

	// Subtext / Artist / Duration
	var artist string
	var durationStr string
	if len(flex) > 1 {
		if col1, ok := flex[1].(map[string]interface{}); ok {
			if inner, ok := col1["musicResponsiveListItemFlexColumnRenderer"].(map[string]interface{}); ok {
				subtext := extractTextFromRuns(inner["text"])
				parts := strings.Split(subtext, " • ")
				if len(parts) >= 2 {
					artist = parts[0]
					if strings.EqualFold(artist, "Song") && len(parts) > 1 {
						artist = parts[1]
					}
					durationStr = parts[len(parts)-1]
				} else if len(parts) == 1 {
					artist = parts[0]
				}
			}
		}
	}

	// Video ID
	videoID := extractVideoID(item)
	if videoID == "" {
		return nil
	}

	// Thumbnail
	thumbnailURL := extractThumbnail(item)

	return &model.Track{
		ID:          videoID,
		Title:       title,
		Artist:      artist,
		DurationStr: durationStr,
		Duration:    parseDurationString(durationStr),
		Thumbnail:   thumbnailURL,
	}
}

func extractTextFromRuns(v interface{}) string {
	textMap, ok := v.(map[string]interface{})
	if !ok {
		return ""
	}
	runs, ok := textMap["runs"].([]interface{})
	if !ok {
		return ""
	}
	var sb strings.Builder
	for _, r := range runs {
		if rMap, ok := r.(map[string]interface{}); ok {
			if t, ok := rMap["text"].(string); ok {
				sb.WriteString(t)
			}
		}
	}
	return sb.String()
}

func extractVideoID(item map[string]interface{}) string {
	if nav, ok := item["navigationEndpoint"].(map[string]interface{}); ok {
		if watch, ok := nav["watchEndpoint"].(map[string]interface{}); ok {
			if id, ok := watch["videoId"].(string); ok && id != "" {
				return id
			}
		}
	}

	if overlay, ok := item["overlay"].(map[string]interface{}); ok {
		if thumbOverlay, ok := overlay["musicItemThumbnailOverlayRenderer"].(map[string]interface{}); ok {
			if content, ok := thumbOverlay["content"].(map[string]interface{}); ok {
				if playBtn, ok := content["musicPlayButtonRenderer"].(map[string]interface{}); ok {
					if playNav, ok := playBtn["playNavigationEndpoint"].(map[string]interface{}); ok {
						if watch, ok := playNav["watchEndpoint"].(map[string]interface{}); ok {
							if id, ok := watch["videoId"].(string); ok {
								return id
							}
						}
					}
				}
			}
		}
	}

	if pid, ok := item["playlistItemData"].(map[string]interface{}); ok {
		if id, ok := pid["videoId"].(string); ok {
			return id
		}
	}

	return ""
}

func extractThumbnail(item map[string]interface{}) string {
	if thumb, ok := item["thumbnail"].(map[string]interface{}); ok {
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

func parseDurationString(s string) time.Duration {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) == 2 {
		var m, sec int
		for _, c := range parts[0] {
			if c >= '0' && c <= '9' {
				m = m*10 + int(c-'0')
			}
		}
		for _, c := range parts[1] {
			if c >= '0' && c <= '9' {
				sec = sec*10 + int(c-'0')
			}
		}
		return time.Duration(m)*time.Minute + time.Duration(sec)*time.Second
	}
	return 3 * time.Minute
}
