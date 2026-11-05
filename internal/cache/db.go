package cache

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"go-chordly/internal/model"
)

// FavoriteItem represents a saved favorite track or album.
type FavoriteItem struct {
	ID           string    `json:"id"`
	Type         string    `json:"type"` // "track", "album", "artist"
	Title        string    `json:"title"`
	Artist       string    `json:"artist"`
	Album        string    `json:"album"`
	DurationSec  int       `json:"duration_sec"`
	ThumbnailURL string    `json:"thumbnail_url"`
	CreatedAt    time.Time `json:"created_at"`
}

// Playlist represents a user-created playlist.
type Playlist struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	ItemCount   int       `json:"item_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// HistoryEntry represents a previously played track with timestamp.
type HistoryEntry struct {
	ID       int64       `json:"id"`
	Track    model.Track `json:"track"`
	PlayedAt time.Time   `json:"played_at"`
}

// DB provides persistent storage for favorites, playlists, play history, and user settings.
type DB struct {
	db *sql.DB
	mu sync.RWMutex
}

var (
	defaultDBMu sync.Mutex
	defaultDB   *DB
)

// GetDefaultDB returns the lazily initialized singleton DB instance using DefaultDBPath().
func GetDefaultDB() (*DB, error) {
	defaultDBMu.Lock()
	defer defaultDBMu.Unlock()
	if defaultDB != nil {
		return defaultDB, nil
	}
	path, err := DefaultDBPath()
	if err != nil {
		return nil, err
	}
	d, err := OpenDB(path)
	if err != nil {
		return nil, err
	}
	defaultDB = d
	return defaultDB, nil
}

// SetDefaultDB sets or overrides the default database singleton (useful in tests).
func SetDefaultDB(d *DB) {
	defaultDBMu.Lock()
	defer defaultDBMu.Unlock()
	defaultDB = d
}

// DefaultDBPath returns the standard Chordly SQLite database file path (~/.local/share/chordly/library.db).
func DefaultDBPath() (string, error) {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dataHome = filepath.Join(home, ".local", "share")
	}
	dir := filepath.Join(dataHome, "chordly")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "library.db"), nil
}

// OpenDB opens (or creates) the SQLite database and executes schema migrations.
func OpenDB(path string) (*DB, error) {
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}

	dsn := path
	if path != ":memory:" && !strings.Contains(path, "?") {
		dsn = path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}

	// Single writer connection pool for SQLite concurrency safety
	db.SetMaxOpenConns(1)

	storage := &DB{db: db}
	if err := storage.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("init db schema: %w", err)
	}

	return storage, nil
}

// Close closes the database handle.
func (d *DB) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.db.Close()
}

func (d *DB) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS favorites (
		id TEXT PRIMARY KEY,
		type TEXT NOT NULL DEFAULT 'track',
		title TEXT NOT NULL,
		artist TEXT DEFAULT '',
		album TEXT DEFAULT '',
		duration_sec INTEGER DEFAULT 0,
		thumbnail_url TEXT DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_favorites_type ON favorites(type);

	CREATE TABLE IF NOT EXISTS playlists (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		description TEXT DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS playlist_items (
		playlist_id TEXT NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
		position INTEGER NOT NULL,
		track_id TEXT NOT NULL,
		title TEXT NOT NULL,
		artist TEXT DEFAULT '',
		duration_sec INTEGER DEFAULT 0,
		thumbnail_url TEXT DEFAULT '',
		added_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (playlist_id, position)
	);

	CREATE INDEX IF NOT EXISTS idx_playlist_items_track ON playlist_items(track_id);

	CREATE TABLE IF NOT EXISTS playback_history (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		track_id TEXT NOT NULL,
		title TEXT NOT NULL,
		artist TEXT DEFAULT '',
		duration_sec INTEGER DEFAULT 0,
		thumbnail_url TEXT DEFAULT '',
		played_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_playback_history_played_at ON playback_history(played_at DESC);

	CREATE TABLE IF NOT EXISTS settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	`
	_, err := d.db.Exec(schema)
	return err
}

// AddFavorite inserts or updates a favorited item.
func (d *DB) AddFavorite(ctx context.Context, f FavoriteItem) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	itemType := f.Type
	if itemType == "" {
		itemType = "track"
	}

	query := `
	INSERT INTO favorites (id, type, title, artist, album, duration_sec, thumbnail_url, created_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	ON CONFLICT(id) DO UPDATE SET
		title=excluded.title,
		artist=excluded.artist,
		album=excluded.album,
		duration_sec=excluded.duration_sec,
		thumbnail_url=excluded.thumbnail_url;
	`
	_, err := d.db.ExecContext(ctx, query, f.ID, itemType, f.Title, f.Artist, f.Album, f.DurationSec, f.ThumbnailURL)
	return err
}

// RemoveFavorite removes a favorite by ID.
func (d *DB) RemoveFavorite(ctx context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.db.ExecContext(ctx, `DELETE FROM favorites WHERE id = ?`, id)
	return err
}

// IsFavorite checks whether an item is favorited.
func (d *DB) IsFavorite(ctx context.Context, id string) (bool, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var count int
	err := d.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM favorites WHERE id = ?`, id).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// ListFavorites retrieves all favorites filtered by type (e.g. "track" or "" for all).
func (d *DB) ListFavorites(ctx context.Context, itemType string) ([]FavoriteItem, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var rows *sql.Rows
	var err error
	if itemType != "" {
		rows, err = d.db.QueryContext(ctx, `SELECT id, type, title, artist, album, duration_sec, thumbnail_url, created_at FROM favorites WHERE type = ? ORDER BY created_at DESC`, itemType)
	} else {
		rows, err = d.db.QueryContext(ctx, `SELECT id, type, title, artist, album, duration_sec, thumbnail_url, created_at FROM favorites ORDER BY created_at DESC`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []FavoriteItem
	for rows.Next() {
		var item FavoriteItem
		if err := rows.Scan(&item.ID, &item.Type, &item.Title, &item.Artist, &item.Album, &item.DurationSec, &item.ThumbnailURL, &item.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, item)
	}
	return list, rows.Err()
}

// CreatePlaylist inserts a new custom playlist and returns it.
func (d *DB) CreatePlaylist(ctx context.Context, name, description string) (*Playlist, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	id := uuid.New().String()
	now := time.Now().UTC()
	query := `INSERT INTO playlists (id, name, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`
	_, err := d.db.ExecContext(ctx, query, id, name, description, now, now)
	if err != nil {
		return nil, err
	}

	return &Playlist{
		ID:          id,
		Name:        name,
		Description: description,
		ItemCount:   0,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// DeletePlaylist removes a playlist and cascades deletion to its items.
func (d *DB) DeletePlaylist(ctx context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.db.ExecContext(ctx, `DELETE FROM playlists WHERE id = ?`, id)
	return err
}

// ListPlaylists lists all playlists with their item counts.
func (d *DB) ListPlaylists(ctx context.Context) ([]Playlist, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := `
	SELECT p.id, p.name, p.description, COUNT(i.position) AS item_count, p.created_at, p.updated_at
	FROM playlists p
	LEFT JOIN playlist_items i ON p.id = i.playlist_id
	GROUP BY p.id
	ORDER BY p.updated_at DESC
	`
	rows, err := d.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Playlist
	for rows.Next() {
		var p Playlist
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.ItemCount, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, p)
	}
	return list, rows.Err()
}

// AddTrackToPlaylist appends a track to the end of a playlist.
func (d *DB) AddTrackToPlaylist(ctx context.Context, playlistID string, track model.Track) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	var nextPos int
	err := d.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), -1) + 1 FROM playlist_items WHERE playlist_id = ?`, playlistID).Scan(&nextPos)
	if err != nil {
		return err
	}

	durationSec := int(track.Duration.Seconds())
	query := `
	INSERT INTO playlist_items (playlist_id, position, track_id, title, artist, duration_sec, thumbnail_url, added_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP);
	`
	_, err = d.db.ExecContext(ctx, query, playlistID, nextPos, track.ID, track.Title, track.Artist, durationSec, track.Thumbnail)
	if err != nil {
		return err
	}

	_, _ = d.db.ExecContext(ctx, `UPDATE playlists SET updated_at = CURRENT_TIMESTAMP WHERE id = ?`, playlistID)
	return nil
}

// GetPlaylistTracks retrieves all tracks in a playlist in sequential position order.
func (d *DB) GetPlaylistTracks(ctx context.Context, playlistID string) ([]model.Track, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := `
	SELECT track_id, title, artist, duration_sec, thumbnail_url
	FROM playlist_items
	WHERE playlist_id = ?
	ORDER BY position ASC
	`
	rows, err := d.db.QueryContext(ctx, query, playlistID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tracks []model.Track
	for rows.Next() {
		var id, title, artist, thumb string
		var durSec int
		if err := rows.Scan(&id, &title, &artist, &durSec, &thumb); err != nil {
			return nil, err
		}
		tracks = append(tracks, model.Track{
			ID:          id,
			Title:       title,
			Artist:      artist,
			Duration:    time.Duration(durSec) * time.Second,
			DurationStr: fmt.Sprintf("%d:%02d", durSec/60, durSec%60),
			Thumbnail:   thumb,
		})
	}
	return tracks, rows.Err()
}

// RecordPlay appends a track play event to playback history.
func (d *DB) RecordPlay(ctx context.Context, track model.Track) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	durationSec := int(track.Duration.Seconds())
	query := `
	INSERT INTO playback_history (track_id, title, artist, duration_sec, thumbnail_url, played_at)
	VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP);
	`
	_, err := d.db.ExecContext(ctx, query, track.ID, track.Title, track.Artist, durationSec, track.Thumbnail)
	return err
}

// GetRecentHistory retrieves recently played tracks up to limit.
func (d *DB) GetRecentHistory(ctx context.Context, limit int) ([]HistoryEntry, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if limit <= 0 {
		limit = 50
	}

	query := `
	SELECT id, track_id, title, artist, duration_sec, thumbnail_url, played_at
	FROM playback_history
	ORDER BY played_at DESC, id DESC
	LIMIT ?
	`
	rows, err := d.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []HistoryEntry
	for rows.Next() {
		var e HistoryEntry
		var id string
		var title, artist, thumb string
		var durSec int
		if err := rows.Scan(&e.ID, &id, &title, &artist, &durSec, &thumb, &e.PlayedAt); err != nil {
			return nil, err
		}
		e.Track = model.Track{
			ID:          id,
			Title:       title,
			Artist:      artist,
			Duration:    time.Duration(durSec) * time.Second,
			DurationStr: fmt.Sprintf("%d:%02d", durSec/60, durSec%60),
			Thumbnail:   thumb,
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// ClearHistory deletes all entries from playback history.
func (d *DB) ClearHistory(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.db.ExecContext(ctx, `DELETE FROM playback_history`)
	return err
}

// SetSetting stores or updates a configuration setting key-value pair.
func (d *DB) SetSetting(ctx context.Context, key, val string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	query := `
	INSERT INTO settings (key, value, updated_at)
	VALUES (?, ?, CURRENT_TIMESTAMP)
	ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=CURRENT_TIMESTAMP;
	`
	_, err := d.db.ExecContext(ctx, query, key, val)
	return err
}

// GetSetting retrieves a configuration setting by key, or returns defaultValue if not found.
func (d *DB) GetSetting(ctx context.Context, key, defaultValue string) (string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var val string
	err := d.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&val)
	if err == sql.ErrNoRows {
		return defaultValue, nil
	}
	if err != nil {
		return "", err
	}
	return val, nil
}

// GetTrackMeta searches favorites and playback history for track metadata by trackID.
func (d *DB) GetTrackMeta(ctx context.Context, trackID string) (model.Track, bool, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	// Check favorites first
	var title, artist, thumb string
	var durSec int
	err := d.db.QueryRowContext(ctx, `SELECT title, artist, duration_sec, thumbnail_url FROM favorites WHERE id = ? LIMIT 1`, trackID).Scan(&title, &artist, &durSec, &thumb)
	if err == nil {
		return model.Track{
			ID:          trackID,
			Title:       title,
			Artist:      artist,
			Duration:    time.Duration(durSec) * time.Second,
			DurationStr: fmt.Sprintf("%d:%02d", durSec/60, durSec%60),
			Thumbnail:   thumb,
		}, true, nil
	} else if err != sql.ErrNoRows {
		return model.Track{}, false, err
	}

	// Fallback to playback_history
	err = d.db.QueryRowContext(ctx, `SELECT title, artist, duration_sec, thumbnail_url FROM playback_history WHERE track_id = ? ORDER BY id DESC LIMIT 1`, trackID).Scan(&title, &artist, &durSec, &thumb)
	if err == nil {
		return model.Track{
			ID:          trackID,
			Title:       title,
			Artist:      artist,
			Duration:    time.Duration(durSec) * time.Second,
			DurationStr: fmt.Sprintf("%d:%02d", durSec/60, durSec%60),
			Thumbnail:   thumb,
		}, true, nil
	} else if err != sql.ErrNoRows {
		return model.Track{}, false, err
	}

	return model.Track{ID: trackID}, false, nil
}
