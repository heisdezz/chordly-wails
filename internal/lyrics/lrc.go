package lyrics

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// LyricLine represents a single timestamped line in a song.
type LyricLine struct {
	Timestamp time.Duration `json:"timestamp"`
	Text      string        `json:"text"`
}

// LyricsDocument contains the parsed lyrics of a song.
type LyricsDocument struct {
	Title    string      `json:"title,omitempty"`
	Artist   string      `json:"artist,omitempty"`
	Album    string      `json:"album,omitempty"`
	Lines    []LyricLine `json:"lines"`
	IsSynced bool        `json:"is_synced"`
}

// timestampRegex matches standard and extended LRC timestamps: [mm:ss.xx], [mm:ss.xxx], [mm:ss]
var timestampRegex = regexp.MustCompile(`\[(\d{1,2}):(\d{2})(?:[.:](\d{1,3}))?\]`)

// metadataRegex matches ID tags like [ti:Title], [ar:Artist], [al:Album]
var metadataRegex = regexp.MustCompile(`^\[([a-zA-Z]+):([^\]]*)\]`)

// ParseLRC parses LRC-formatted text into a structured LyricsDocument.
func ParseLRC(r io.Reader) (*LyricsDocument, error) {
	doc := &LyricsDocument{
		Lines: make([]LyricLine, 0),
	}

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// Check for metadata tag [tag:value]
		if match := metadataRegex.FindStringSubmatch(line); len(match) == 3 {
			tag := strings.ToLower(match[1])
			val := strings.TrimSpace(match[2])
			switch tag {
			case "ti":
				doc.Title = val
			case "ar":
				doc.Artist = val
			case "al":
				doc.Album = val
			}
			continue
		}

		// Check for timestamps
		timeMatches := timestampRegex.FindAllStringSubmatchIndex(line, -1)
		if len(timeMatches) == 0 {
			continue
		}

		// The text begins after the last timestamp tag
		lastIndex := timeMatches[len(timeMatches)-1][1]
		text := strings.TrimSpace(line[lastIndex:])

		for _, matchIndices := range timeMatches {
			match := line[matchIndices[0]:matchIndices[1]]
			ts, err := parseTimestamp(match)
			if err != nil {
				continue
			}

			doc.Lines = append(doc.Lines, LyricLine{
				Timestamp: ts,
				Text:      text,
			})
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan error: %w", err)
	}

	if len(doc.Lines) > 0 {
		doc.IsSynced = true
		// Sort lines chronologically
		sort.SliceStable(doc.Lines, func(i, j int) bool {
			return doc.Lines[i].Timestamp < doc.Lines[j].Timestamp
		})
	}

	return doc, nil
}

// ParseLRCString parses a string containing LRC lyrics.
func ParseLRCString(content string) (*LyricsDocument, error) {
	return ParseLRC(strings.NewReader(content))
}

// ActiveLineIndex returns the index of the line that should currently be highlighted.
// Returns -1 if playback is before the first line or if there are no lines.
func (doc *LyricsDocument) ActiveLineIndex(playhead time.Duration) int {
	if len(doc.Lines) == 0 || playhead < doc.Lines[0].Timestamp {
		return -1
	}

	// Binary search for the first line whose timestamp is strictly greater than playhead
	idx := sort.Search(len(doc.Lines), func(i int) bool {
		return doc.Lines[i].Timestamp > playhead
	})

	return idx - 1
}

// parseTimestamp converts a tag like "[01:24.50]" into a time.Duration.
func parseTimestamp(tag string) (time.Duration, error) {
	m := timestampRegex.FindStringSubmatch(tag)
	if len(m) < 3 {
		return 0, fmt.Errorf("invalid timestamp format: %s", tag)
	}

	min, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, err
	}
	sec, err := strconv.Atoi(m[2])
	if err != nil {
		return 0, err
	}

	ms := 0
	if len(m) > 3 && m[3] != "" {
		msStr := m[3]
		// Normalize fractional seconds (e.g. "5" -> 500ms, "50" -> 500ms, "500" -> 500ms)
		switch len(msStr) {
		case 1:
			msStr += "00"
		case 2:
			msStr += "0"
		default:
			msStr = msStr[:3]
		}
		ms, _ = strconv.Atoi(msStr)
	}

	total := time.Duration(min)*time.Minute + time.Duration(sec)*time.Second + time.Duration(ms)*time.Millisecond
	return total, nil
}
