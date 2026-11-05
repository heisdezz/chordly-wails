# Chordly → Wails Port: Implementation Plan

> Porting `/run/media/destiny/Ventoy/dr_projects/chord` (a GoGPU/WebGPU desktop YouTube Music
> client) into `go-chordly` (Wails v2). This document is the authoritative plan; keep it updated
> as phases land.
>
> Task coordination & claims live in [`PROGRESS.md`](PROGRESS.md); per-task specs in
> [`notes/`](notes/README.md); target architecture in [`architecture.md`](architecture.md).

## 1. Why this works

The original app is cleanly layered. Only the **presentation layer** (`appui/`, ~9,900 LOC of
GoGPU widget trees) is framework-specific. Everything below it is framework-agnostic Go and
transplants into a Wails backend with minimal surgery.

| Original package | Contents | Wails fate |
|---|---|---|
| `internal/ytm` | InnerTube client (`WEB_REMIX`), search/browse/player/radio, `PlayerService` stream-URL resolution, token parsing | ✅ Reuse as-is (~2,300 LOC) |
| `internal/ytm/service.go` | `DefaultService` facade; push-style methods write into `internal/state` signals from goroutines | ⚠️ Decouple: return values / callbacks instead of signal writes |
| `internal/session` | Token persistence (`$XDG_CONFIG_HOME/chordly/session.json`, atomic save/load/clear) | ✅ Reuse as-is |
| `internal/model` | `Track`, `PlaybackState`, `RepeatMode`, `MixCard` (JSON-tagged) | ✅ Reuse as-is (they serialize straight through Wails bindings) |
| `internal/cache` | SQLite (modernc, no CGO): favorites, playlists, history, settings, track meta; `DiskCache` | ✅ Reuse as-is |
| `internal/lyrics` | LRCLIB client, LRC parser, `ActiveLineIndex(playhead)` | ✅ Reuse as-is |
| `internal/audio` | Pure-Go `Device` (48 kHz stereo, sink via `io.Writer`), `CommandSink` (`pw-play`/`paplay`/`aplay`), Opus/WAV decoders, ffmpeg fallback, `RingReader` | ✅ Reuse as-is |
| `appui/playback.go` | Playback orchestration: builds the stream pipeline per track, subscribes to signals, 250 ms playhead tick, seek-regeneration | ↻ Port into a `PlayerService` in the backend |
| `internal/state` | Global `Signal[T]` vars (`CurrentTrack`, `PlaybackStatus`, `Queue`, …) + pure queue logic (`NextTrack`, `PlayQueueIndex`, …) | ↻ Queue logic → `PlayerService`; signals → Wails events |
| `internal/theme` | Palette presets, dominant-color extraction, blend/lerp | ↻ Port derivation logic; consume as CSS variables |
| `internal/plugin` | MPRIS/Discord/scrobbler/WASM host (fully built, unreachable in original) | ⏸ Later phase |
| `router/router.go` | Segment-tree router with history stack | ❌ Replaced by TanStack Router |
| `appui/*` views & shell | All widget trees (`home_view.go`, `search_view.go`, `bottom_player.go`, …) | ❌ Rebuilt as SolidJS components |

## 2. Stack decisions

| Decision | Rationale |
|---|---|
| **Wails v2** (v2.17, already scaffolded) | Native window + webview, Go bindings auto-generated to `frontend/wailsjs`, runtime event bus |
| **SolidJS** (`vite-plugin-solid`) | Fine-grained signals mirror the old `Signal[T]` reactive model — the smallest conceptual gap from `state.*` signals |
| **TanStack Router** (`@tanstack/solid-router`) | Type-safe file-based routes with params (`/album/$id`), matches the old route table |
| **DaisyUI 5 + Tailwind 4** | Class-only component library — no framework dependency; covers nav rail, cards, player, toasts out of the box |
| **Go audio pipeline stays in the backend** | The pure-Go Opus pipeline, ffmpeg fallback, seek-regeneration and volume control are built and tested; the frontend renders UI only |

### Runtime dependencies (document in README)
- `ffmpeg` on PATH (fallback decoder for mp4/AAC containers + offset seeks)
- `pw-play` / `paplay` / `aplay` (PipeWire → Pulse → ALSA order; env `CHORDLY_AUDIO_SINK` override)
- Optional: `yt-dlp` for stream-resolution fallback (`CHORDLY_YTDLP`)

## 3. Target architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        Wails (webview)                          │
│  SolidJS + TanStack Router + DaisyUI                            │
│  - Shell: NavRail / TopBar / page viewport / QueuePanel         │
│  - BottomPlayer (persistent)                                    │
│  - Stores bridged from Wails events (signals)                   │
└───────────────┬─────────────────────────────────▲───────────────┘
                │  bindings (App methods)         │ runtime.EventsEmit
┌───────────────┴─────────────────────────────────┴───────────────┐
│                        Go backend                               │
│  App (bound struct)                                             │
│    ├─ catalog: GetHomeFeed, Search, GetAlbum/Artist/Playlist    │
│    ├─ player: PlayerService (ports appui/playback.go +          │
│    │         state queue logic)                                 │
│    └─ session: Import/Load/Clear token bundle                   │
│                                                                  │
│  internal/{ytm, audio, session, cache, lyrics, model} (as-is)   │
└─────────────────────────────────────────────────────────────────┘
```

**Data flow rule (mirrors the original):** reads = bindings (frontend calls Go, gets data);
state churn = events (Go emits, frontend stores react). The only event with high frequency is
`player:progress` (250 ms, aligned with the old tick goroutine).

### 3.1 Binding + event contract (Phase 1 target)

Bound via `App` struct (`wails.Bind`); auto-generated as `frontend/wailsjs/go/main/App`.

**Auth / session**
| Binding | Returns | Notes |
|---|---|---|
| `GetAccount()` | `{Name, HasSession, Source}` | Restored-session snapshot at startup |
| `ImportSession(raw string)` | error | Reuses `ytm.ParseToken` (InnerTune exports, cookies.txt, JSON bundles) |
| `SignOut()` | error | Clears session store |

**Catalog (wraps `ytm.DefaultService`, decoupled)**
| Binding | Returns |
|---|---|
| `GetHomeFeed()` | `ytm.HomeFeedResult` |
| `Search(query string)` | `[]model.Track` |
| `GetAlbum(id string)` / `GetArtist(id)` / `GetPlaylist(id)` | detail structs |
| `StartRadio(videoID string)` | — (fills queue) |

**Player (ports `appui/playback.go` + `internal/state` queue logic)**
| Binding | Notes |
|---|---|
| `GetPlaybackState()` | Initial snapshot: track, status, position/duration, volume, queue, index, repeat, shuffle |
| `Play(track model.Track)`, `TogglePlayPause()`, `Pause()`, `Resume()` | |
| `Seek(position time.Duration)` | Triggers pipeline regeneration (generation counters per original) |
| `SetVolume(v float64)` | Propagates into `Device` |
| `Next()`, `Previous()`, `PlayQueueIndex(n)`, `AppendToQueue(track)`, `MoveQueueItem(from, to)`, `RemoveQueueItem(n)`, `ClearQueue()` | Pure queue logic lifted from `internal/state` |
| `SetRepeat(mode)`, `SetShuffle(bool)` | |

**Events (backend → frontend, `runtime.EventsEmit`)**
| Event | Payload | Cadence |
|---|---|---|
| `player:progress` | `{position ms, duration ms}` | every 250 ms while playing |
| `player:status` | `{status, track}` | on track change / play-pause / buffering |
| `player:queue` | `{queue, index}` | on any queue mutation |
| `player:error` | `{message}` | stream failures |
| `auth:changed` | `{name, hasSession}` | sign-in/out |
| `toast` | `{kind, message}` | replaces `appui.ShowToast*` |

## 4. Backend work breakdown

### B1 — Transplant internals
- Copy `internal/{ytm,audio,session,model,cache,lyrics}` (skip `state`, `theme`, `plugin` for now)
  from `chord/` into `go-chordly/internal/`, keep vendor files needed by them
  (`go-opus`, `simd`, `sqlite`, `uuid`) — no `vendor/` dir in go-chordly, use the module cache.
- Rename module imports `chordly/...` → `go-chordly/...`; bump `go.mod` to a toolchain that
  supports the copied code (original targets go 1.27).
- Drop ytm's synthetic-fallback mock data only if it requires the old state package
  (`service.go` is the sole state consumer — see B2).

### B2 — Decouple `internal/ytm/service.go`
The push methods (`SyncHomeData`, `LiveSearch`, `StartRadioForTrack`,
`FetchRadioContinuation`) write results directly into `internal/state` signals from goroutines
— the single coupling point between ytm and gogpu. Options (in order of preference):
1. Strip the push methods; catalog bindings call the pull APIs
   (`Client.GetHomeFeed`, `Client.Search`, …) and hold results in the new `App` state.
2. Where streaming/pagination semantics matter (radio continuation), keep a small
   continuation-token holder inside `PlayerService`/`App` instead of signals.

### B3 — `PlayerService` (the real port target)
New file `player/player.go` (backend package). Composed from:
- `appui/playback.go`: engine construction (`StartPlaybackEngine` internals), per-track pipeline
  (`PrepareTrackStream` → `SniffContainer` → decoder/ffmpeg selection → `CommandSink` →
  `NewDeviceWithReader`), seek regeneration with generation counters, ffmpeg fallback rules,
  yt-dlp fallback path.
- `internal/state` queue logic: `TogglePlayPause`, `NextTrack`, `PreviousTrack`,
  `PlayQueueIndex`, `MoveQueueItem`, `RemoveQueueItem`, `ClearQueue`, `AppendToQueue`,
  shuffle/repeat/`checkContinuation`.
- New: Wails bridge — swap `state.Signal subscriptions` for event emission
  (`runtime.EventsEmit(ctx, ...)`); the 250 ms `tick()` goroutine emits `player:progress`.

### B4 — `App` struct + main wiring
- Extend `app.go` with the binding methods of §3.1, holding a `*PlayerService`,
  `session.Store`, and `ytm` service.
- `main.go`: on startup — open session store, restore bundle → `SetAuth`, start player, fire
  `SyncHomeData` equivalent via bindings, register MPRIS later.
- Config: title "Chordly – YouTube Music", size 1280×820 (match original), keep asset server.

## 5. Frontend work breakdown

### F0 — Toolchain (Phase 0)
- `frontend/package.json`: add `solid-js`, `vite-plugin-solid`, `@tanstack/solid-router`,
  `@tanstack/solid-router-devtools` (dev), `tailwindcss@^4`, `daisyui@^5`, `@tailwindcss/vite`.
- Vite: replace React-style template with a Solid entry (`main.jsx` → Solid component, tracked
  via `createRoot` in `index.html`).
- Tailwind 4 CSS-first config in `app.css`: `@import "tailwindcss"; @plugin "daisyui";`
- Router: **hash history** initially (Wails asset server doesn't rewrite SPA deep-links in
  production by default). Deliberate, revisit if we add a custom `AssetServer` middleware.

### F1 — Bootstrap color extraction (Phase 0, tiny)
We already ship the framework to derive palettes from album art. Initial implementation:
- `theme` port is deferred to a late phase. DaisyUI `chordly` theme with sensible light/dark
  presets gets us to Phase 2 quickly.
- Derive-accent-from-artwork port reuses `internal/theme/extract.go` logic later (emit
  `theme:palette` event; frontend sets CSS variables).

### F2 — Route tree (mirror of `main.go` route table)

| TanStack route | Original name | Component | Phase |
|---|---|---|---|
| `/` | `home` | `Home` | P2 |
| `/explore` | `explore` | alias of Home | P5 |
| `/library` (…`favorites` / `playlists` / `history` / `downloads`) | `library-*` | `Library` (tabbed) | P4 |
| `/album/$id` | `album-detail` | `Album` | P3 |
| `/artist/$id` | `artist-detail` | `Artist` | P3 |
| `/playlist/$id` | `playlist-detail` | `Playlist` | P3 |
| `/mix/$id` | `mix-detail` | `Mix` | P3 |
| `/search` | `search` | `Search` | P2 |
| `/queue` | `queue` | `Queue` | P3 |
| `/player` | `player` | `Player` (fullscreen view) | P4 |
| `/lyrics` | `lyrics` | `Lyrics` | P4 |
| `/settings` | `settings` | `Settings` | P4 |
| `/login` | `login` | `SignIn` | P4 |
| `/account` | `account` | `Account` | P4 |
| `*` | `not-found` | `NotFound` | P2 |

### F3 — Stores bridging events (replaces `internal/state`)
Solid signals mirror the old signals 1:1. One bridge module (`src/lib/playerStore.ts`):
- `onMount`-style subscriptions: `runtime.EventsOn('player:progress', …)` etc. update setters.
- Bindings are wrapped in small typed helpers re-exported from `src/lib/api.ts`
  (thin wrappers over generated `frontend/wailsjs/go/main/App.js`, so swapping underlying
  transports doesn't touch components).

### F4 — Component inventory (mirrors appui file list)

| Component (DaisyUI/Tailwind) | Mirrors | Phase |
|---|---|---|
| `Shell` (layout grid: rail / topbar / page / queue / player) | `shell.go` + `sidebar.go` | P2 |
| `NavRail` | `sidebar.go` | P2 |
| `TopBar` (search field + account avatar) | `topbar.go`, `textfield.go` | P2 |
| `BottomPlayer` (track info, scrubber, controls, volume, queue toggle) | `bottom_player.go`, `scrubber.go` | P2 |
| `TrackCard`, `ShelfCard`, `MixCard`, `ArtistCard` | `components.go`, `hero.go`, `made_for_you.go` | P2/P3 |
| `TrackList` (incl. context correlations) | `library_view.go`, `album_view.go` | P3 |
| `QueuePanel` (drag-reorder; use `@dnd-kit/solid` or native) | `up_next.go`, `queue_view.go` | P3 |
| `Spinner`, `Skeleton*` | `loading.go` | P2 |
| `Toaster` (`notification`-style DaisyUI alerts) | `toast.go`, `toast API` | P2 |
| `LoginPage` / `ImportSession` flow | `signin_view.go`, `login_page.go` | P4 |
| `Player` fullscreen + `Lyrics` overlay (synced mode maps `ActiveLineIndex(playhead)`) | `player_view.go`, `lyrics_view.go` | P4 |
| Settings page (audio sink picker, cache mgmt, theme toggle) | `settings_view.go` | P4 |

## 6. Phases

### Phase 0 — Scaffold (½ day)
- [ ] F0 toolchain setup; hello-world Solid router page `/` with DaisyUI hero stub
- [ ] Confirm `wails dev` hot reloads Solid HMR and `wails build` produces a bundle

### Phase 1 — Backend core (1–2 days)
- [ ] B1 transplant + import rename; `go vet ./...` clean
- [ ] B2 decouple `service.go`
- [ ] B3 `PlayerService` (play/pause/seek/queue/events)
- [ ] B4 App bindings; extend `GetPlaybackState` snapshot; verify event flow from devtools

### Phase 2 — Vertical slice (2–3 days)
- [ ] F3 stores; F4 Shell + NavRail + TopBar + BottomPlayer + skeletons + toaster
- [ ] Routes: `/`, `/search`, `*`
- [ ] Home feed rendered from real InnerTube data; click track → plays audio from backend
- [ ] Search → results grid; add-to-queue flows from search cards
- [ ] Playhead scrubber + volume working through bindings

**Definition of done:** you can launch `wails dev`, search a song, play it, scrub, adjust
volume, see track status through the bottom player, with zero gogpu code.

### Phase 3 — Browse & queue (2–3 days)
- [ ] `TrackList` markup for album/artist/playlist/mix pages, pagination / continuation
- [ ] `/queue` route + `QueuePanel` with drag reorder
- [ ] Radio ("start from this track") + continuation loading

### Phase 4 — Sessions, library, extras (3–4 days)
- [ ] `/login` (token import copy-paste / file), `/account`, sign-out
- [ ] `/lyrics` + synced overlay; `/player` fullscreen
- [ ] Library tabs on cache DB (favorites, playlists, history, downloads via DiskCache)
- [ ] `/settings` page; playback persistence patterns
- [ ] Auth-driven UI guards: hide settings toggle vs. show account chip

### Phase 5 — Themes & polish (2–3 days)
- [ ] Port `internal/theme` derivation (emit CSS variables + `theme:palette` events)
- [ ] Artwork-driven accent; reduced-motion setting; keyboard shortcuts (space, arrows, /)

### Phase 6 — Later / optional (unchanged scope from original `plan-2`)
- [ ] MPRIS + media keys (`internal/plugin/mpris_linux.go` — already pure Go, host trivially)
- [ ] Discord RPC, scrobblers, WASM plugin host
- [ ] Frontend history rewrite if we add SPA middleware in the asset server

## 7. Risks & notes

1. **TrackStream fallbacks rely on binaries.** `PlayerService.ResolveStreamURL` already
   probes multiple InnerTube clients; the ffmpeg/yt-dlp fallbacks silently degrade. Surface
   better errors via `player:error`.
2. **Timing behavior at 250 ms.** The old tick loop syncs the `Playhead` signal AND
   auto-advances to `NextTrack` on device stop. In the port this must be a Go goroutine
   (not JS timers) or queue advancement stalls when the window is throttled. Keep it in-broker.
3. **`modernc.org/sqlite` + `wazero` + `go-opus` are CGO-free** — pass-through go.sum work for
   the module bump only.
4. **Wails bindings drop non-JSON types**: `time.Duration` in `model.Track` — keep the
   pre-baked `DurationStr` string in the wire format and use `GetPlaybackState` snapshot
   values (ms ints) for progress math.
5. **No vendoring in go-chordly.** The internal packages were vendored in chord; here we pull
   the module cache. Pin versions from chord's `go.mod` when possible.
6. **Original dead code not ported**: `Pages/Project/Chord` template remnants, rarely used
   appui internals (`textfit.go`, `textfit.go` sizing) skip — browser handles text fitting.
7. **Session token import is generic** — `ytm.ParseToken` already supports InnerTune exports,
   cookies.txt, raw headers; `/login` reuses it unchanged — no parser work to port.

## 8. Out of scope (for this port)
- Windows/macOS packaging polish (defer builds; wails cross-compile exists when needed)
- `internal/plugin` activation wiring (MPRIS stays dark until Phase 6)
- Mobile targets (Wails v3 concern)
