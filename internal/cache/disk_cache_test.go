package cache

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newTestCache wires a DiskCache into a throwaway directory.
func newTestCache(t *testing.T, quota int64) *DiskCache {
	t.Helper()
	c, err := NewAt(filepath.Join(t.TempDir(), "audio"), quota)
	if err != nil {
		t.Fatalf("NewAt: %v", err)
	}
	return c
}

// seedComplete writes a complete entry directly (writer + commit).
func seedComplete(t *testing.T, c *DiskCache, id, payload string) {
	t.Helper()
	w, err := c.Writer(id)
	if err != nil {
		t.Fatalf("Writer(%s): %v", id, err)
	}
	if _, err := w.Write([]byte(payload)); err != nil {
		t.Fatalf("Write(%s): %v", id, err)
	}
	if err := w.Commit(); err != nil {
		t.Fatalf("Commit(%s): %v", id, err)
	}
}

// setModTime pins a deterministic LRU timestamp on a cache file.
func setModTime(t *testing.T, c *DiskCache, id string, at time.Time) {
	t.Helper()
	p, err := c.pathFor(id)
	if err != nil {
		t.Fatalf("pathFor(%s): %v", id, err)
	}
	if err := os.Chtimes(p, at, at); err != nil {
		t.Fatalf("Chtimes(%s): %v", id, err)
	}
}

func TestLookupMiss(t *testing.T) {
	c := newTestCache(t, 0)

	entry, err := c.Lookup("dQw4w9WgXcQ")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if entry.Status != CacheMiss || entry.CachedBytes != 0 {
		t.Fatalf("expected miss, got %+v", entry)
	}
}

func TestPartialResumeAndCompleteFlow(t *testing.T) {
	c := newTestCache(t, 0)
	const id = "aBcDeFgHiJk"

	w, err := c.Writer(id)
	if err != nil {
		t.Fatalf("Writer: %v", err)
	}
	if _, err := w.Write([]byte("01234567")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	entry, err := c.Lookup(id)
	if err != nil {
		t.Fatalf("Lookup(part): %v", err)
	}
	if entry.Status != CachePartial || entry.CachedBytes != 8 {
		t.Fatalf("expected partial@8, got %+v", entry)
	}

	offset, ok := c.ResumeOffset(id)
	if !ok || offset != 8 {
		t.Fatalf("ResumeOffset = %d, %v; want 8, true", offset, ok)
	}

	// Open must reject partials: playing half a file is not playback.
	if _, err := c.Open(id); err == nil {
		t.Fatal("Open on partial entry should fail")
	}

	// Simulate the resumed tail of the transfer, then finish it.
	if _, err := w.Write([]byte("890123")); err != nil {
		t.Fatalf("Write(resume): %v", err)
	}
	if got := w.BytesWritten(); got != 14 {
		t.Fatalf("BytesWritten = %d; want 14", got)
	}
	if err := w.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	entry, err = c.Lookup(id)
	if err != nil {
		t.Fatalf("Lookup(complete): %v", err)
	}
	if entry.Status != CacheComplete || entry.CachedBytes != 14 {
		t.Fatalf("expected complete@14, got %+v", entry)
	}
	if _, ok := c.ResumeOffset(id); ok {
		t.Fatal("complete entries are not resumable")
	}

	file, err := c.Open(id)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Release(id)
	defer file.Close()

	// Read whole.
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(data, []byte("01234567890123")) {
		t.Fatalf("content mismatch: %q", data)
	}

	// Seek proves the player can use io.ReadSeeker semantics directly.
	if _, err := file.Seek(8, io.SeekStart); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	tail, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("ReadAll(tail): %v", err)
	}
	if string(tail) != "890123" {
		t.Fatalf("tail = %q; want 890123", tail)
	}
}

func TestCommitYieldsWebmFile(t *testing.T) {
	c := newTestCache(t, 0)
	seedComplete(t, c, "xYzAbCdEfGh", "payload")

	if _, err := os.Stat(filepath.Join(c.Dir(), "xYzAbCdEfGh.webm")); err != nil {
		t.Fatalf("expected {id}.webm on disk: %v", err)
	}
	if _, err := os.Stat(filepath.Join(c.Dir(), "xYzAbCdEfGh.part")); !os.IsNotExist(err) {
		t.Fatalf("part file should be gone after commit")
	}
}

func TestOpenMissingEntry(t *testing.T) {
	c := newTestCache(t, 0)

	if _, err := c.Open("missingID1"); err != ErrNotCached {
		t.Fatalf("Open missing: got %v, want ErrNotCached", err)
	}
}

func TestLRUEviction(t *testing.T) {
	c := newTestCache(t, 0) // seed with eviction off to avoid commit-time races
	base := time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC)

	seedComplete(t, c, "oldEntry1111", "0123456789")
	setModTime(t, c, "oldEntry1111", base)

	seedComplete(t, c, "midEntry2222", "0123456789")
	setModTime(t, c, "midEntry2222", base.Add(time.Minute))

	seedComplete(t, c, "newEntry3333", "0123456789")
	setModTime(t, c, "newEntry3333", base.Add(2*time.Minute))

	// 3 x 10 = 30 bytes exceeds the 25-byte budget: prune oldest first.
	c.SetQuota(25)
	c.EnforceQuota()

	// 3 x 10 > 30 budget: the oldest entry must be pruned.
	if _, err := os.Stat(filepath.Join(c.Dir(), "oldEntry1111.webm")); !os.IsNotExist(err) {
		t.Fatalf("oldest entry should have been evicted, stat err=%v", err)
	}
	for _, keep := range []string{"midEntry2222", "newEntry3333"} {
		if _, err := os.Stat(filepath.Join(c.Dir(), keep+".webm")); err != nil {
			t.Fatalf("%s should survive eviction: %v", keep, err)
		}
	}
}

func TestEvictionSkipsInUseFiles(t *testing.T) {
	c := newTestCache(t, 0)
	base := time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC)

	seedComplete(t, c, "pinMe001111", "0123456789")
	setModTime(t, c, "pinMe001111", base)

	seedComplete(t, c, "other111111", "0123456789")
	setModTime(t, c, "other111111", base.Add(time.Minute))

	seedComplete(t, c, "fresh222222", "0123456789")
	setModTime(t, c, "fresh222222", base.Add(2*time.Minute))

	// Pin the oldest file open: eviction must skip it and take the next one.
	file, err := c.Open("pinMe001111")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() {
		file.Close()
		c.Release("pinMe001111")
	}()

	// 3 x 10 = 30 bytes exceeds the 25-byte budget; the pinned entry is
	// skipped and the next-oldest pays the price.
	c.SetQuota(25)
	c.EnforceQuota()

	if _, err := os.Stat(filepath.Join(c.Dir(), "pinMe001111.webm")); err != nil {
		t.Fatalf("pinned file must survive eviction: %v", err)
	}
	if _, err := os.Stat(filepath.Join(c.Dir(), "other111111.webm")); !os.IsNotExist(err) {
		t.Fatalf("next-oldest entry should have been evicted instead, err=%v", err)
	}
}

func TestAbortDropsPartial(t *testing.T) {
	c := newTestCache(t, 0)

	w, err := c.Writer("abortMe1234")
	if err != nil {
		t.Fatalf("Writer: %v", err)
	}
	if _, err := w.Write([]byte("junk")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Abort(); err != nil {
		t.Fatalf("Abort: %v", err)
	}

	entry, err := c.Lookup("abortMe1234")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if entry.Status != CacheMiss {
		t.Fatalf("expected miss after abort, got %+v", entry)
	}
}

func TestDiscardRemovesEntry(t *testing.T) {
	c := newTestCache(t, 0)
	seedComplete(t, c, "goneSoon111", "data")

	if err := c.Discard("goneSoon111"); err != nil {
		t.Fatalf("Discard: %v", err)
	}
	if _, err := c.Lookup("goneSoon111"); err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	entry, _ := c.Lookup("goneSoon111")
	if entry.Status != CacheMiss {
		t.Fatalf("expected miss after discard, got %+v", entry)
	}
}

func TestDiscardRejectsInUse(t *testing.T) {
	c := newTestCache(t, 0)
	seedComplete(t, c, "busyReader1", "data")

	file, err := c.Open("busyReader1")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer file.Close()
	defer c.Release("busyReader1")

	if err := c.Discard("busyReader1"); err == nil {
		t.Fatal("Discard on an in-use entry must fail")
	}
}

func TestClearEmptiesCache(t *testing.T) {
	c := newTestCache(t, 0)
	seedComplete(t, c, "clearMe1111", "one")
	seedComplete(t, c, "clearMe2222", "two")

	if err := c.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	if got := c.TotalSize(); got != 0 {
		t.Fatalf("TotalSize after clear = %d; want 0", got)
	}
}

func TestTotalSizeReportsPayload(t *testing.T) {
	c := newTestCache(t, 0)
	payload := bytes.Repeat([]byte("x"), 1<<20+512) // above the reporting floor
	seedComplete(t, c, "sizeProbe01", string(payload))

	if got := c.TotalSize(); got < int64(len(payload)) {
		t.Fatalf("TotalSize = %d; want >= %d", got, len(payload))
	}
}

func TestRejectsUnsafeIDs(t *testing.T) {
	c := newTestCache(t, 0)

	for _, id := range []string{"", "../evil", "sub/dir", `back\slash`, "has space", "id.dot"} {
		if _, err := c.Lookup(id); err == nil {
			t.Fatalf("Lookup(%q) should fail", id)
		}
	}
}

func TestSetQuotaZeroDisablesEviction(t *testing.T) {
	c := newTestCache(t, 5)
	c.SetQuota(0) // unlimited

	c.mu.Lock()
	quota := c.quota
	c.mu.Unlock()
	if quota != 0 {
		t.Fatalf("quota = %d; want 0", quota)
	}
}

func TestWriterResumeAppendsNotOverwrites(t *testing.T) {
	c := newTestCache(t, 0)
	const id = "appendProbe1"

	w1, err := c.Writer(id)
	if err != nil {
		t.Fatalf("Writer: %v", err)
	}
	if _, err := w1.Write([]byte("AAAA")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Simulate an app crash: never commit, just close and reopen.
	if err := w1.file.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	w2, err := c.Writer(id)
	if err != nil {
		t.Fatalf("Writer(reopen): %v", err)
	}
	if got := w2.BytesWritten(); got != 4 {
		t.Fatalf("reopen offset = %d; want 4", got)
	}
	if _, err := w2.Write([]byte("BBBB")); err != nil {
		t.Fatalf("Write(resumed): %v", err)
	}
	if err := w2.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(c.Dir(), id+".webm"))
	if string(data) != "AAAABBBB" {
		t.Fatalf("resume overwrote data: %q", data)
	}
}

func TestListDownloadedAndSingleton(t *testing.T) {
	c := newTestCache(t, 0)
	SetDefaultCache(c)
	got, err := GetDefaultCache()
	if err != nil || got != c {
		t.Fatalf("GetDefaultCache failed: %v, got: %v", err, got)
	}

	// Empty cache list
	items, err := c.ListDownloaded()
	if err != nil {
		t.Fatalf("ListDownloaded empty error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected 0 downloaded items, got %d", len(items))
	}

	// Add 2 complete entries
	seedComplete(t, c, "trackAlpha01", "alpha content bytes")
	seedComplete(t, c, "trackBeta002", "beta content bytes longer")

	items, err = c.ListDownloaded()
	if err != nil {
		t.Fatalf("ListDownloaded error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 downloaded items, got %d", len(items))
	}

	var foundAlpha, foundBeta bool
	for _, item := range items {
		if item.VideoID == "trackAlpha01" {
			foundAlpha = true
			if item.Size != int64(len("alpha content bytes")) {
				t.Errorf("unexpected size for alpha: %d", item.Size)
			}
		}
		if item.VideoID == "trackBeta002" {
			foundBeta = true
			if item.Size != int64(len("beta content bytes longer")) {
				t.Errorf("unexpected size for beta: %d", item.Size)
			}
		}
	}
	if !foundAlpha || !foundBeta {
		t.Errorf("missing downloaded items: alpha=%v beta=%v", foundAlpha, foundBeta)
	}
}
