# Task Specification: TSK-101 (Transplant Internal Packages & Module Rename)

**Workstream:** `WS-MIG`
**Task ID:** `TSK-101`
**File Scope:** `go.mod`, `go.sum`, `internal/**`
**Dependencies:** None
**Status:** `TODO`
**Package target:** `go-chordly/internal/*`

---

## 1. Objective

Bring the framework-agnostic backend packages of the original app into the Wails module so
that every later backend task compiles against them:

| Source (`../chord/`) | Destination | Notes |
| --- | --- | --- |
| `internal/ytm` | `internal/ytm` | Copy **excluding** `service.go` push methods — TSK-102 handles the split (copy service.go only to keep a compile base) |
| `internal/model` | `internal/model` | Verbatim |
| `internal/session` | `internal/session` | Verbatim (path stays `$XDG_CONFIG_HOME/chordly/session.json`) |
| `internal/audio` | `internal/audio` | Verbatim (device, sink, decoder, ffmpeg, buffer) |
| `internal/cache` | `internal/cache` | Verbatim (SQLite + DiskCache) |
| `internal/lyrics` | `internal/lyrics` | Verbatim |

**Not** copied in this task: `internal/state`, `internal/theme`, `internal/plugin`,
`appui/`, `router/` — those belong to later workstreams (WS-THM, WS-PLG) or to the
rewritten frontend.

---

## 2. Mechanical Steps

1. Copy package directories; preserve licenses and test files.
2. Rename all imports `chordly/<path>` → `go-chordly/<path>` (only relevant for
   `internal/session/store.go` which imports `ytm.ParseToken`, and any cross-package
   imports inside `internal/ytm/service.go`).
3. Copy module requirements from `../chord/go.mod` for packages actually retained:
   `tphakala/go-opus` (plus `tphakala/simd`), `modernc.org/sqlite`, `google/uuid`,
   `golang.org/x/image` (cache/lyrics helpers), `godbus/dbus` **not** needed yet (plugin).
   Vendor the module cache — do **not** create a `vendor/` dir.
4. Bump `go.mod` toolchain to match the copied code (original targets go 1.27;
   scaffold currently 1.25).
5. Keep the old repo's `signal`-dependent files out of compilation: the copied
   `internal/state` must not exist; `internal/ytm/service.go` is the only consumer —
   copy `service.go` as-is (it will not compile against the missing `internal/state`)
   so TSK-102 can strip the push methods immediately after landing. If a compile gate is
   needed before TSK-102 claims, temporarily exclude only `service.go` with a rename
   (`service.go.bak`) and note this in the PROGRESS.md row.

---

## 3. Verification

- `go build ./internal/...` compiles with 0 errors.
- `go test ./internal/...` passes (port existing package tests with the sources).
- No file outside File Scope modified.
