package model

import "time"

// PlaybackState represents the current state of audio playback.
type PlaybackState int

const (
	StateStopped PlaybackState = iota
	StatePlaying
	StatePaused
	StateBuffering
)

// RepeatMode represents the queue repeating behavior.
type RepeatMode int

const (
	RepeatOff RepeatMode = iota
	RepeatAll
	RepeatOne
)

// Track represents an audio track / song.
type Track struct {
	ID          string        `json:"id"`
	Title       string        `json:"title"`
	Artist      string        `json:"artist"`
	ArtistID    string        `json:"artist_id,omitempty"`
	Album       string        `json:"album,omitempty"`
	Duration    time.Duration `json:"duration"`
	DurationStr string        `json:"duration_str"`
	Thumbnail   string        `json:"thumbnail_url"`
	StreamURL   string        `json:"stream_url,omitempty"`
	Listeners   string        `json:"listeners,omitempty"`
	IsLiked     bool          `json:"is_liked"`
}

// MixCard represents a curated playlist or mix.
type MixCard struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Subtitle  string `json:"subtitle,omitempty"`
	Thumbnail string `json:"thumbnail"`
	ColorHex  uint32 `json:"color_hex"`
}
