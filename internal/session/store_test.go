package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-chordly/internal/ytm"
)

func TestStoreSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "session.json")
	store := Open(path)

	bundle := &ytm.TokenBundle{
		Cookie:      "SID=sid_123; SAPISID=sapisid_abc; __Secure-3PSID=sec_123",
		SAPISID:     "sapisid_abc",
		VisitorData: "CgtVisitorData",
		AccountName: "Alex Listener",
		Source:      ytm.TokenSourceInnerTune,
	}
	if err := store.Save(bundle); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat session file: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("session file mode = %o, want 600", mode)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat session dir: %v", err)
	}
	if mode := dirInfo.Mode().Perm(); mode != 0o700 {
		t.Errorf("session dir mode = %o, want 700", mode)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.SAPISID != "sapisid_abc" {
		t.Errorf("SAPISID = %q, want sapisid_abc", loaded.SAPISID)
	}
	if loaded.AccountName != "Alex Listener" {
		t.Errorf("AccountName = %q", loaded.AccountName)
	}
	if loaded.VisitorData != "CgtVisitorData" {
		t.Errorf("VisitorData = %q", loaded.VisitorData)
	}
	if loaded.Source != ytm.TokenSourceInnerTune {
		t.Errorf("Source = %q", loaded.Source)
	}
	if !strings.Contains(loaded.Cookie, "SAPISID=sapisid_abc") {
		t.Errorf("Cookie = %q, want cookie header with SAPISID", loaded.Cookie)
	}
	if loaded.Cookies["SID"] != "sid_123" {
		t.Errorf("Cookies[SID] = %q, want sid_123", loaded.Cookies["SID"])
	}
}

func TestStoreLoadMissing(t *testing.T) {
	store := Open(filepath.Join(t.TempDir(), "absent.json"))
	if _, err := store.Load(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load missing = %v, want ErrNotFound", err)
	}
}

func TestStoreClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	store := Open(path)
	if err := store.Save(&ytm.TokenBundle{SAPISID: "abc"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, err := store.Load(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load after Clear = %v, want ErrNotFound", err)
	}
	// Clearing again is a no-op.
	if err := store.Clear(); err != nil {
		t.Fatalf("second Clear: %v", err)
	}
}

func TestStoreCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path).Load(); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("Load corrupt = %v, want decode error", err)
	}
}

func TestStoreNoPath(t *testing.T) {
	store := Open("")
	if err := store.Save(&ytm.TokenBundle{SAPISID: "abc"}); !errors.Is(err, ErrNoPath) {
		t.Errorf("Save = %v, want ErrNoPath", err)
	}
	if _, err := store.Load(); !errors.Is(err, ErrNotFound) {
		t.Errorf("Load = %v, want ErrNotFound", err)
	}
}
