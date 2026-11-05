package ytm

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go-chordly/internal/model"
)

// RadioQueueResult contains the parsed tracks from YouTube Music's automix radio.
type RadioQueueResult struct {
	Tracks            []model.Track
	ContinuationToken string
}

// GetRadio retrieves an automix radio queue for a given seed song.
func (c *Client) GetRadio(ctx context.Context, videoID string) (*RadioQueueResult, error) {
	if videoID == "" {
		return nil, errors.New("empty videoID")
	}

	payload := RequestPayload{
		VideoID:     videoID,
		PlaylistID:  fmt.Sprintf("RDAMVM%s", videoID),
		IsAudioOnly: true,
	}

	var raw map[string]interface{}
	if err := c.Do(ctx, "next", payload, &raw); err != nil {
		return nil, fmt.Errorf("next radio call failed: %w", err)
	}

	return parseRadioResponse(raw), nil
}

// GetRadioContinuation fetches the next batch of recommended songs using a continuation token.
func (c *Client) GetRadioContinuation(ctx context.Context, continuationToken string) (*RadioQueueResult, error) {
	if continuationToken == "" {
		return nil, errors.New("empty continuation token")
	}

	payload := RequestPayload{
		Continuation: continuationToken,
		IsAudioOnly:  true,
	}

	var raw map[string]interface{}
	if err := c.Do(ctx, "next", payload, &raw); err != nil {
		return nil, fmt.Errorf("next continuation call failed: %w", err)
	}

	return parseContinuationResponse(raw), nil
}

func parseRadioResponse(raw map[string]interface{}) *RadioQueueResult {
	res := &RadioQueueResult{}

	contents, ok := raw["contents"].(map[string]interface{})
	if !ok {
		return res
	}

	singleCol, ok := contents["singleColumnMusicWatchNextResultsRenderer"].(map[string]interface{})
	if !ok {
		return res
	}

	tabbed, ok := singleCol["tabbedRenderer"].(map[string]interface{})
	if !ok {
		return res
	}

	watchNext, ok := tabbed["watchNextTabbedResultsRenderer"].(map[string]interface{})
	if !ok {
		return res
	}

	tabs, ok := watchNext["tabs"].([]interface{})
	if !ok {
		return res
	}

	for _, t := range tabs {
		tMap, ok := t.(map[string]interface{})
		if !ok {
			continue
		}
		renderer, ok := tMap["tabRenderer"].(map[string]interface{})
		if !ok {
			continue
		}

		title, _ := renderer["title"].(string)
		if title == "Up next" || title == "" {
			tabContent, ok := renderer["content"].(map[string]interface{})
			if !ok {
				continue
			}
			queueRend, ok := tabContent["musicQueueRenderer"].(map[string]interface{})
			if !ok {
				continue
			}
			qContent, ok := queueRend["content"].(map[string]interface{})
			if !ok {
				continue
			}
			panel, ok := qContent["playlistPanelRenderer"].(map[string]interface{})
			if !ok {
				continue
			}

			// Parse tracks
			if panelItems, ok := panel["contents"].([]interface{}); ok {
				for _, item := range panelItems {
					if itemMap, ok := item.(map[string]interface{}); ok {
						if track := parsePlaylistPanelVideo(itemMap); track != nil && track.ID != "" {
							res.Tracks = append(res.Tracks, *track)
						}
					}
				}
			}

			// Parse continuation
			res.ContinuationToken = extractContinuationToken(panel)
			break
		}
	}

	return res
}

func parseContinuationResponse(raw map[string]interface{}) *RadioQueueResult {
	res := &RadioQueueResult{}

	continuationContents, ok := raw["continuationContents"].(map[string]interface{})
	if !ok {
		return res
	}

	panelCont, ok := continuationContents["playlistPanelContinuation"].(map[string]interface{})
	if !ok {
		return res
	}

	if items, ok := panelCont["contents"].([]interface{}); ok {
		for _, item := range items {
			if itemMap, ok := item.(map[string]interface{}); ok {
				if track := parsePlaylistPanelVideo(itemMap); track != nil && track.ID != "" {
					res.Tracks = append(res.Tracks, *track)
				}
			}
		}
	}

	res.ContinuationToken = extractContinuationToken(panelCont)
	return res
}

func parsePlaylistPanelVideo(item map[string]interface{}) *model.Track {
	videoRenderer, ok := item["playlistPanelVideoRenderer"].(map[string]interface{})
	if !ok {
		return nil
	}

	videoID, _ := videoRenderer["videoId"].(string)
	if videoID == "" {
		return nil
	}

	title := extractTextFromRuns(videoRenderer["title"])
	byline := extractTextFromRuns(videoRenderer["longBylineText"])
	var artist string
	if parts := strings.Split(byline, " • "); len(parts) > 0 {
		artist = parts[0]
	}

	lengthText := extractTextFromRuns(videoRenderer["lengthText"])

	// Thumbnail
	thumbnailURL := ""
	if thumbMap, ok := videoRenderer["thumbnail"].(map[string]interface{}); ok {
		if thumbs, ok := thumbMap["thumbnails"].([]interface{}); ok && len(thumbs) > 0 {
			if last, ok := thumbs[len(thumbs)-1].(map[string]interface{}); ok {
				thumbnailURL, _ = last["url"].(string)
			}
		}
	}

	return &model.Track{
		ID:          videoID,
		Title:       title,
		Artist:      artist,
		DurationStr: lengthText,
		Duration:    parseDurationString(lengthText),
		Thumbnail:   thumbnailURL,
	}
}

func extractContinuationToken(parent map[string]interface{}) string {
	if contList, ok := parent["continuations"].([]interface{}); ok && len(contList) > 0 {
		if first, ok := contList[0].(map[string]interface{}); ok {
			if nextRadio, ok := first["nextRadioContinuationData"].(map[string]interface{}); ok {
				if tok, ok := nextRadio["continuation"].(string); ok {
					return tok
				}
			}
			if nextCont, ok := first["nextContinuationData"].(map[string]interface{}); ok {
				if tok, ok := nextCont["continuation"].(string); ok {
					return tok
				}
			}
		}
	}
	return ""
}
