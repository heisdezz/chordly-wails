# Chordly Architecture (Wails Edition)

## 1. System Overview

Chordly is a desktop YouTube Music player combining a **Go backend (Wails v2)** with a
**SolidJS web frontend** (TanStack Router, DaisyUI). The interface and feature set are
ported from the original GoGPU/WebGPU app (modeled after Metrolist / InnerTune: playful
Material aesthetics, persistent bottom mini-player, queue drawer, synced lyrics).

The system keeps clean-layer architecture; the only layer rebuilt from scratch is the
presentation layer. **Reads = bindings. State churn = events.**

```
 ┌─────────────────────────────────────────────────────────────────────┐
 │                        Wails (webview)                              │
 │  SolidJS + TanStack Router + DaisyUI/Tailwind                       │
 │                                                                     │
 │  ┌──────────────┬────────────────────────────────┐                  │
 │  │  NavRail     │  Page viewport (TanStack routes)│                  │
 │  │  Home        │  Home / Search / Album / Artist │                  │
 │  │  Search      │  Playlist / Mix / Library       │                  │
 │  │  Library     │  Queue / Player / Lyrics        │                  │
 │  │  Settings    │  Settings / Login / Account     │                  │
 │  └──────────────┴────────────────────────────────┘                  │
 │  ┌──────────────────────────────────────────────┐                   │
 │  │  Persistent BottomPlayer (info/seek/controls) │ Polls via events  │
 │  └──────────────────────────────────────────────┘                   │
 │                                                                     │
 │  Stores (Solid signals) ← runtime.EventsOn  —  App bindings →       │
 └──────────────────┬─────────────────────────────▲────────────────────┘
                    │ Go method calls             │ EventsEmit
 ┌──────────────────┴─────────────────────────────┴────────────────────┐
 │                          Go backend                                 │
 │                                                                     │
 │  App (bound struct)                                                 │
 │    ├─ catalog: GetHomeFeed / Search / GetAlbum|Artist|Playlist      │
 │    ├─ player: Play / Seek / Next / AppendToQueue / Volume / ...     │
 │    └─ session: GetAccount / ImportSession / SignOut                 │
 │                                                                     │
 │  PlayerService  —  ports appui/playback.go + state queue logic      │
 │    ├─ pipeline: stream URL → RingReader → Opus/ffmpeg → CommandSink │
 │    ├─ 250 ms tick goroutine → player:progress events                │
 │    └─ queue: repeat/shuffle/reorder/continuation                    │
 │                                                                     │
 │  internal/ packages (ported nearly as-is from the original repo)    │
 │    ytm (InnerTube client) · audio · session · cache · lyrics · model│
 └────────────────────────────────────────────────────────────────────┘
```

---

## 2. Core Layers & Responsibilities

### 2.1 Backend: InnerTube Service (`internal/ytm`)
Interface with YouTube Music's private API using the `WEB_REMIX` client context
(spotified clients for guest playback): search, home feed, album/artist/playlist browse,
stream URL resolution, radio queue generation. Auth via `TokenBundle` (Cookie + SID-hash
authorization headers), parsed from InnerTune exports / cookies.txt through
`ytm.ParseToken`. Push-style methods that wrote into the old gogpu signals are
decoupled (TSK-102).

### 2.2 Playback: `PlayerService` (`player/`)
Ports the orchestration in `appui/playback.go`: per-track pipeline construction
(sniff container → pure-Go Opus/WAV demux, else ffmpeg subprocess), seek regeneration
with generation counters, volume, playhead tick (Go goroutine at 250 ms), auto-advance
on device stop, and the queue logic functions previously in `internal/state`
(`TogglePlayPause`, `NextTrack`, `PlayQueueIndex`, reorder, repeat/shuffle,
continuation). Emits Wails events; takes commands via bindings.

### 2.3 Frontend State (Solid stores)
Bridge module mirrors old signals: `currentTrack`, `playbackStatus`, `playhead`,
`volume`, `queue`, `queueIndex`, `account` — updated by `runtime.EventsOn`.
Bindings are wrapped in typed helpers (`src/lib/api.ts`), so components never touch the
generated `frontend/wailsjs` files directly.

### 2.4 Presentation: SolidJS Views (`frontend/src`)
- **Shell layout**: navigation rail, top search bar, page viewport, queue panel, and the
  persistent bottom transport bar (grid + DaisyUI classes).
- **TanStack Router**: type-safe route tree with params (`/album/$id`) replacing the
  original segment-tree router; hash history until the SPA asset-server middleware lands.

### 2.5 Extensibility (`internal/plugin`) — later phase
Existing pure-Go MPRIS / Discord / scrobbler / WASM-host packages remain; they get wired
into `PlayerService` track state when Phase 6 starts.

---

## 3. Directory Layout

```
go-chordly/
├── main.go                       # Wails bootstrap: session restore → player → bindings
├── app.go                        # App bound struct: catalog/player/session bindings
├── player/
│   └── player.go                 # PlayerService (pipelines, queue, events)
├── internal/
│   ├── audio/                    # Pure-Go decoders, ring buffer, CommandSink, device
│   ├── cache/                    # SQLite (favorites/playlists/history) + disk cache
│   ├── lyrics/                   # LRC parser + LRCLIB client
│   ├── model/                    # Track, PlaybackState, RepeatMode, MixCard
│   ├── session/                  # Token bundle persistence (~/.config/chordly)
│   ├── theme/                    # Palette presets + extraction (Phase 5 port)
│   ├── ytm/                      # InnerTube client: browse/search/player/queue/token
│   └── plugin/                   # MPRIS, Discord, scrobblers, WASM host (Phase 6)
├── docs/                         # This doc set + progress board + task notes
└── frontend/
    ├── wailsjs/go/main/App.js    # Auto-generated bindings
    └── src/
        ├── lib/api.ts            # Typed binding wrappers (single import point)
        ├── lib/stores.ts         # Event-bridge Solid stores
        ├── router.tsx            # TanStack Router (hash history)
        ├── components/           # Shell, NavRail, TopBar, BottomPlayer, cards, queue
        ├── routes/               # TanStack file routes (//, /search, /album/$id, …)
        ├── views/                # Page implementations (Home, Search, Album, …)
        └── app.css               # Tailwind 4 + DaisyUI 5 config, theme variables
```

---

## 4. Key invariant

The 250 ms playhead tick and queue auto-advance **must stay in Go**: the tick goroutine
syncs `player:progress` events and advances the queue on device stop. If moved to JS,
auto-advance stalls whenever the webview throttles background timers.
