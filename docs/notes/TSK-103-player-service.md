# Task Specification: TSK-103 (PlayerService Port — Pipeline, Queue, Events)

**Workstream:** `WS-PLR`
**Task ID:** `TSK-103`
**File Scope:** `player/*.go`
**Dependencies:** `TSK-101`
**Status:** `TODO`
**Package:** `go-chordly/player`

---

## 1. Objective

Replace the two orchestrators that lived inside framework-coupled packages in the
original app — `appui/playback.go` (audio engine construction + run loop) and the queue
logic functions inside `internal/state/state.go` — with one backend-idiomatic service.
It exposes an imperative API for the `App` bindings (TSK-104) and pushes state to the
frontend with Wails events.

---

## 2. Source Port Map

### 2.1 From `../chord/appui/playback.go` (414 LOC)
- `StartPlaybackEngine()` internals → constructor `New(ctx, deps)`.
- Per-track pipeline construction:
  `ytm.DefaultService.PrepareTrackStream` → `audio.SniffContainer` → decoder selection
  (pure-Go Opus/WAV from byte 0; ffmpeg subprocess for MP4/AAC and any offset seek) →
  `audio.OpenCommandSink()` → `audio.NewDeviceWithReader(sink, decoder, cfg)`.
- Seek handling: pipeline rebuild at offset (`startAt(track, offset, paused)`) with
  **generation counters** — a stale `startAt` result on an older generation is ignored.
- 250 ms `tick()` goroutine: syncs position and **auto-advances to next track on device
  stop** (must remain a Go goroutine — JS timers stall under webview throttling).
- yt-dlp fallback resolution is inside `internal/ytm/player.go` already; PlayerService
  only surfaces failures as `player:error` events.
- Watch of external signals (`state.CurrentTrack`, `state.PlaybackStatus`, `state.Volume`,
  `SeekRequest`, `IsScrubbing`) → replaced by direct method calls from App bindings.

### 2.2 From `../chord/internal/state/state.go`
Port (verbatim where possible, swapping `Signal[T]` stores for plain fields guarded by a
mutex): `TogglePlayPause`, `NextTrack`, `PreviousTrack`, `PlayQueueIndex`, `MoveQueueItem`
/`Up`/`Down`, `RemoveQueueItem`, `ClearQueue`, `AppendToQueue`, shuffle/repeat semantics,
and `checkContinuation` (OnQueueNeedsContinuation hook → `ytm` radio continuation
support lands with TSK-102's decoupled continuation holder).

---

## 3. API Sketch

```go
type Service interface {
    Play(ctx context.Context, track model.Track) error     // radio-seed optional
    PlayQueueIndex(i int) error
    TogglePlayPause() / Pause() / Resume()
    Seek(position time.Duration) error
    SetVolume(v float64) / Next() / Previous()
    AppendToQueue(track model.Track) / MoveQueueItem(from, to int) /
    RemoveQueueItem(i int) / ClearQueue()
    SetRepeat(m model.RepeatMode) / SetShuffle(bool)
    Snapshot() Snapshot            // initial sync payload for GetPlaybackState
}
```

Events emitted (payload shapes are normative — see TSK-104):
`player:progress` every 250 ms while playing; `player:status` on track change /
play-pause / buffering; `player:queue` on any mutation; `player:error` on failures.

---

## 4. Constraints & Pitfalls

1. **No gogpu imports.** The service must be pure Go + `wails/v2/runtime` for events.
2. **Threading:** device pump + tick goroutine mutate state; guard fields with a mutex
   instead of recreating signal callbacks.
3. **No UI coupling:** the frontend never reads internal audio state directly — only
   through events and `Snapshot()`.
4. Keep the `CHORDLY_AUDIO_SINK` / `CHORDLY_FFMPEG` / `CHORDLY_YTDLP` env overrides and
   stream-URL probe ordering exactly as in the original.

---

## 5. Verification

- `go test ./player/...` — port/adjust `../chord/appui/playback_test.go` logic where
  non-UI; queue-logic tests become plain unit tests against the service.
- `go build ./...` green.
- Integration smoke (with TSK-104): a play → pause → seek → next sequence reaches the
  frontend via devtools `EventsOn` during `wails dev`.
