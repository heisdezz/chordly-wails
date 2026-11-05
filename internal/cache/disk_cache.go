// Package cache implements Chordly's local persistence: the tiered audio
// chunk disk cache (WS-DB) that lets streaming playback restart instantly
// with zero network traffic, and resume partial downloads from byte offsets.
package cache

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// CacheStatus reports the on-disk state of a cached audio entry.
type CacheStatus int

const (
	// CacheMiss means no partial or complete data exists on disk.
	CacheMiss CacheStatus = iota
	// CachePartial means a download was interrupted and can be resumed.
	CachePartial
	// CacheComplete means the full file is playable directly from disk.
	CacheComplete
)

// Entry describes one cached audio file.
type Entry struct {
	VideoID     string
	Status      CacheStatus
	CachedBytes int64 // bytes present on disk (the resume offset when partial)
}

// DownloadItem describes a completed audio file on disk for the library downloads view.
type DownloadItem struct {
	VideoID string
	Size    int64
	ModTime time.Time
}

// DiskCache stores streamed audio chunks on disk, one file per video.
//
// Lifecycle: a player opens Writer(videoID) and tee's streamed chunks into
// it; on completion Commit renames the .part file to its final .webm name.
// While a file is partial it lives under ".part" and Lookup reports its
// resume offset. Complete files are LRU-tracked by modification time and
// pruned once the configured quota is exceeded.
//
// All methods are safe for concurrent use.
type DiskCache struct {
	dir   string
	quota int64 // maximum total bytes on disk; 0 = unlimited

	mu        sync.Mutex
	inUse     map[string]int   // videoID -> reader count (protected against eviction)
	resumable map[string]int64 // videoID -> committed total size for entries

	// sweepMu serializes EnforceQuota sweeps so overlapping invocations
	// (Commit's async trigger + explicit calls) cannot double-evict while
	// each holds a stale picture of the on-disk totals.
	sweepMu sync.Mutex
}

var (
	defaultCacheMu sync.Mutex
	defaultCache   *DiskCache
)

// GetDefaultCache returns the default singleton DiskCache instance.
func GetDefaultCache() (*DiskCache, error) {
	defaultCacheMu.Lock()
	defer defaultCacheMu.Unlock()
	if defaultCache != nil {
		return defaultCache, nil
	}
	dc, err := New()
	if err != nil {
		return nil, err
	}
	defaultCache = dc
	return defaultCache, nil
}

// SetDefaultCache sets or overrides the default cache singleton (useful in tests).
func SetDefaultCache(c *DiskCache) {
	defaultCacheMu.Lock()
	defer defaultCacheMu.Unlock()
	defaultCache = c
}

// Default cache layout per the plan: ~/.cache/chordly/audio/{videoID}.webm
const (
	cacheDirName         = "chordly"
	audioDirName         = "audio"
	completeExt          = ".webm"
	partialExt           = ".part"
	defaultQuota   int64 = 2 << 30 // 2 GB as suggested by the plan
	minQuotaReport       = 1 << 20 // sizes below 1 MiB are reported as zero
)

// New returns a DiskCache rooted at ~/.cache/chordly/audio with the default
// 2 GB quota. It is safe to call before any playback.
func New() (*DiskCache, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("cache: resolve user cache dir: %w", err)
	}
	return NewAt(filepath.Join(base, cacheDirName, audioDirName), defaultQuota)
}

// NewAt builds a DiskCache in an explicit directory and quota (bytes).
// quota <= 0 disables LRU eviction. Mostly used by tests.
func NewAt(dir string, quota int64) (*DiskCache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("cache: create %s: %w", dir, err)
	}
	return &DiskCache{
		dir:       dir,
		quota:     quota,
		inUse:     make(map[string]int),
		resumable: make(map[string]int64),
	}, nil
}

// Dir returns the cache's on-disk root.
func (c *DiskCache) Dir() string { return c.dir }

// SetQuota adjusts the LRU eviction budget in bytes; <=0 disables eviction.
func (c *DiskCache) SetQuota(quota int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.quota = quota
}

// pathFor resolves the on-disk path, refusing anything that could escape the
// cache directory. YouTube video IDs are [A-Za-z0-9_-]{11}.
func (c *DiskCache) pathFor(videoID string) (string, error) {
	if videoID == "" || len(videoID) > 64 || strings.ContainsAny(videoID, "/\\") || strings.Contains(videoID, "..") {
		return "", fmt.Errorf("cache: invalid video id %q", videoID)
	}
	for _, r := range videoID {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return "", fmt.Errorf("cache: invalid video id %q", videoID)
		}
	}
	return filepath.Join(c.dir, videoID) + completeExt, nil
}

// Lookup inspects the cache without opening anything.
func (c *DiskCache) Lookup(videoID string) (Entry, error) {
	finalPath, err := c.pathFor(videoID)
	if err != nil {
		return Entry{}, err
	}

	if fi, err := os.Stat(finalPath); err == nil {
		c.mu.Lock()
		c.resumable[videoID] = fi.Size()
		c.mu.Unlock()
		_ = os.Chtimes(finalPath, time.Now(), time.Now()) // LRU touch
		return Entry{VideoID: videoID, Status: CacheComplete, CachedBytes: fi.Size()}, nil
	}

	partPath := finalPath + partialExt
	if fi, err := os.Stat(partPath); err == nil && !fi.IsDir() {
		return Entry{VideoID: videoID, Status: CachePartial, CachedBytes: fi.Size()}, nil
	}

	// Stale zero-length partials are as good as absent; drop them.
	if _, statErr := os.Stat(partPath); statErr == nil {
		_ = os.Remove(partPath)
	}
	return Entry{VideoID: videoID, Status: CacheMiss}, nil
}

// ResumeOffset reports the byte offset at which a partial download should be
// continued, plus false when there is nothing to resume.
func (c *DiskCache) ResumeOffset(videoID string) (int64, bool) {
	entry, err := c.Lookup(videoID)
	if err != nil || entry.Status != CachePartial {
		return 0, false
	}
	return entry.CachedBytes, true
}

// Open returns a read-seek-closer for a complete cached file, marking it as
// in-use so LRU eviction cannot remove it underneath the player.
func (c *DiskCache) Open(videoID string) (*os.File, error) {
	finalPath, err := c.pathFor(videoID)
	if err != nil {
		return nil, err
	}

	file, err := os.Open(finalPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotCached
		}
		return nil, fmt.Errorf("cache: open %s: %w", finalPath, err)
	}
	c.mu.Lock()
	c.inUse[videoID]++
	c.mu.Unlock()
	_ = os.Chtimes(finalPath, time.Now(), time.Now()) // LRU touch
	return file, nil
}

// Release drops the in-use pin taken by Open once playback has finished.
// Calling Release more times than Open for the same ID is a no-op.
func (c *DiskCache) Release(videoID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n := c.inUse[videoID]; n > 1 {
		c.inUse[videoID] = n - 1
		return
	}
	delete(c.inUse, videoID)
}

// ErrNotCached indicates a complete copy is not on disk yet.
var ErrNotCached = errors.New("cache: entry not on disk")

// chunkWriter tee's streamed chunks into the partial file on disk.
type chunkWriter struct {
	c       *DiskCache
	videoID string
	file    *os.File
	written int64
}

// Writer opens (or resumes) the partial cache file for videoID and returns a
// writer positioned for appending. The caller continues its HTTP download
// from ResumeOffset(videoID) and writes every received chunk here until it
// knows the transfer is done, then calls Commit.
func (c *DiskCache) Writer(videoID string) (*chunkWriter, error) {
	finalPath, err := c.pathFor(videoID)
	if err != nil {
		return nil, err
	}

	file, err := os.OpenFile(finalPath+partialExt, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("cache: open writer for %s: %w", videoID, err)
	}
	offset, _ := file.Seek(0, 2 /* end */)
	return &chunkWriter{c: c, videoID: videoID, file: file, written: offset}, nil
}

// Write stores one streamed chunk in the partial file.
func (w *chunkWriter) Write(p []byte) (int, error) {
	n, err := w.file.Write(p)
	w.written += int64(n)
	return n, err
}

// BytesWritten reports how many bytes are on disk for this entry.
func (w *chunkWriter) BytesWritten() int64 { return w.written }

// Commit promotes the partial file to its final, complete name.
func (w *chunkWriter) Commit() error {
	if err := w.file.Sync(); err != nil {
		_ = w.file.Close()
		return fmt.Errorf("cache: sync %s: %w", w.videoID, err)
	}
	if err := w.file.Close(); err != nil {
		return fmt.Errorf("cache: close %s: %w", w.videoID, err)
	}
	w.file = nil

	finalPath, err := w.c.pathFor(w.videoID)
	if err != nil {
		return err
	}
	if err := os.Rename(finalPath+partialExt, finalPath); err != nil {
		return fmt.Errorf("cache: commit %s: %w", w.videoID, err)
	}

	w.c.mu.Lock()
	w.c.resumable[w.videoID] = w.written
	w.c.mu.Unlock()

	go w.c.EnforceQuota()
	return nil
}

// Abort discards the partial file (used on fatal download errors).
func (w *chunkWriter) Abort() error {
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}
	finalPath, err := w.c.pathFor(w.videoID)
	if err != nil {
		return err
	}
	return os.Remove(finalPath + partialExt)
}

// Discard removes every trace of one entry from the cache.
func (c *DiskCache) Discard(videoID string) error {
	finalPath, err := c.pathFor(videoID)
	if err != nil {
		return err
	}

	c.mu.Lock()
	_, inUse := c.inUse[videoID]
	c.mu.Unlock()
	if inUse {
		return fmt.Errorf("cache: %s is in use and cannot be discarded", videoID)
	}

	firstErr := os.Remove(finalPath)
	if e := os.Remove(finalPath + partialExt); e != nil && !os.IsNotExist(e) && firstErr == nil {
		firstErr = e
	}
	if firstErr != nil && os.IsNotExist(firstErr) {
		return nil // nothing cached: already clean
	}
	return firstErr
}

// TotalSize walks the cache directory and reports the bytes on disk.
// Anything below 1 MiB reports as zero to keep settings renders tidy.
func (c *DiskCache) TotalSize() int64 {
	var total int64
	_ = filepath.WalkDir(c.dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, statErr := d.Info(); statErr == nil {
			total += info.Size()
		}
		return nil
	})
	if total < minQuotaReport {
		return 0
	}
	return total
}

// ListDownloaded returns all completed audio entries currently cached on disk,
// sorted by modification time descending (newest first).
func (c *DiskCache) ListDownloaded() ([]DownloadItem, error) {
	c.mu.Lock()
	dir := c.dir
	c.mu.Unlock()

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cache: list downloaded: %w", err)
	}

	var items []DownloadItem
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, completeExt) {
			videoID := strings.TrimSuffix(name, completeExt)
			info, statErr := entry.Info()
			if statErr != nil {
				continue
			}
			items = append(items, DownloadItem{
				VideoID: videoID,
				Size:    info.Size(),
				ModTime: info.ModTime(),
			})
		}
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].ModTime.After(items[j].ModTime)
	})

	return items, nil
}

// EnforceQuota prunes least-recently-used complete files until the cache fits
// within the configured budget. Entries pinned by Open are never evicted and
// in-flight partial downloads are left alone.
func (c *DiskCache) EnforceQuota() {
	c.mu.Lock()
	quota := c.quota
	c.mu.Unlock()
	if quota <= 0 {
		return
	}

	// One sweep at a time: a second sweep re-walks the directory after the
	// first finishes, so it always acts on fresh totals.
	c.sweepMu.Lock()
	defer c.sweepMu.Unlock()

	var files []cacheFile
	var partialBytes int64
	_ = filepath.WalkDir(c.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil {
			return nil
		}
		switch {
		case strings.HasSuffix(d.Name(), completeExt):
			files = append(files, cacheFile{path: path, size: info.Size(), modTime: info.ModTime()})
		case strings.HasSuffix(d.Name(), partialExt):
			partialBytes += info.Size()
		}
		return nil
	})

	sort.Slice(files, func(i, j int) bool { return files[i].modTime.Before(files[j].modTime) })

	c.mu.Lock()
	pinned := make(map[string]bool, len(c.inUse))
	for id := range c.inUse {
		pinned[id] = true
	}
	c.mu.Unlock()

	total := partialBytes
	for _, f := range files {
		total += f.size
	}

	for _, f := range files {
		if total <= quota {
			return
		}
		id := strings.TrimSuffix(filepath.Base(f.path), completeExt)
		if pinned[id] {
			continue
		}
		if err := os.Remove(f.path); err == nil {
			total -= f.size

			c.mu.Lock()
			delete(c.resumable, id)
			c.mu.Unlock()
		}
	}
}

// cacheFile describes one complete file on disk during eviction sweeps.
type cacheFile struct {
	path    string
	size    int64
	modTime time.Time
}

// Clear empties the whole cache directory. Files pinned by Open survive.
func (c *DiskCache) Clear() error {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return fmt.Errorf("cache: clear: %w", err)
	}

	c.mu.Lock()
	pinned := make(map[string]bool, len(c.inUse))
	for id := range c.inUse {
		pinned[id] = true
	}
	c.mu.Unlock()

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.TrimSuffix(strings.TrimSuffix(entry.Name(), partialExt), completeExt)
		if pinned[name] {
			continue
		}
		_ = os.Remove(filepath.Join(c.dir, entry.Name()))
	}
	return nil
}
