# Chordly Project Notes & Multi-Agent Task Knowledge Base

This directory (`docs/notes/`) holds implementation blueprints, architectural decisions,
schema specifications, and technical reference notes for tasks claimed from
`docs/PROGRESS.md`.

- Notes filename convention: `TSK-<id>-<slug>.md`.
- A task without a dedicated note points at the source file being ported
  (`Reference: ../chord/<path>`) — the port keeps the same behavior, only the binding
  surface changes.
- Statuses stay in sync with the Task Matrix in `docs/PROGRESS.md`; a note is a
  *spec before* the task starts and a *resolution record* after it completes.

---

## 1. Master Task Implementation Matrix

| Task ID      | Workstream    | Task Title                                                 | Documentation Note                                                        | Status        |
| :----------- | :------------ | :--------------------------------------------------------- | :------------------------------------------------------------------------ | :------------ |
| `TSK-001`    | **WS-FE-KIT** | SolidJS + TanStack Router + DaisyUI Toolchain              | Reference: `frontend/package.json`, `implementation-plan.md` §5/F0        | `DONE`        |
| `TSK-101`    | **WS-MIG**    | Transplant Internal Packages & Module Rename               | [`TSK-101-internal-transplant.md`](TSK-101-internal-transplant.md)        | `TODO`        |
| `TSK-102`    | **WS-YTM**    | Decouple ytm Service Push Methods from Signals             | Reference: `../chord/internal/ytm/service.go`                             | `TODO`        |
| `TSK-103`    | **WS-PLR**    | PlayerService Port (Pipeline + Queue + Events)             | [`TSK-103-player-service.md`](TSK-103-player-service.md)                  | `TODO`        |
| `TSK-104`    | **WS-BND**    | App Bindings, Event Bus & Startup Wiring                   | [`TSK-104-bindings-events.md`](TSK-104-bindings-events.md)                | `TODO`        |
| `TSK-201`    | **WS-FE-KIT** | Event Bridge Stores + Typed API Wrappers                   | Reference: `implementation-plan.md` §3.1 + §5/F3                          | `TODO`        |
| `TSK-202`    | **WS-FE-SHELL** | Shell, NavRail, TopBar, BottomPlayer & UI Kit            | [`TSK-202-shell-components.md`](TSK-202-shell-components.md)              | `TODO`        |
| `TSK-203`    | **WS-FE-PGS** | Core Routes: Home, Search, NotFound                        | [`TSK-203-core-routes.md`](TSK-203-core-routes.md)                        | `TODO`        |
| `TSK-204`    | **WS-BND**    | Vertical-Slice Integration & Verification                  | Reference: `PROGRESS.md` Phase 2 definition of done                       | `TODO`        |
| `TSK-301`    | **WS-FE-PGS** | Detail Pages (Album/Artist/Playlist/Mix) + TrackList       | Reference: `../chord/appui/{album,artist,playlist}_view.go`               | `TODO`        |
| `TSK-302`    | **WS-FE-PGS** | Queue Completion (Reorder, Save Playlist, Automix)         | Reference: [`../chord/docs/notes/TSK-220-queue-completion.md`](file:///run/media/destiny/Ventoy/dr_projects/chord/docs/notes/TSK-220-queue-completion.md) | `TODO` |
| `TSK-401`    | **WS-FE-PGS** | Session Import UI (Login / Account / Sign-Out)             | Reference: [`../chord/docs/notes/TSK-214-signin-page-innertune-import.md`](file:///run/media/destiny/Ventoy/dr_projects/chord/docs/notes/TSK-214-signin-page-innertune-import.md) | `TODO` |
| `TSK-402`    | **WS-FE-PGS** | Full-Screen Player + Synced Lyrics                         | Reference: [`../chord/docs/notes/TSK-218-full-screen-player.md`](file:///run/media/destiny/Ventoy/dr_projects/chord/docs/notes/TSK-218-full-screen-player.md) | `TODO` |
| `TSK-403`    | **WS-FE-PGS** | Library Shell (Favorites/Playlists/History/Downloads)      | Reference: [`../chord/docs/notes/TSK-216-library-shell.md`](file:///run/media/destiny/Ventoy/dr_projects/chord/docs/notes/TSK-216-library-shell.md) | `TODO` |
| `TSK-501`    | **WS-THM**    | Palette Engine Port → CSS Variables Branch                 | Reference: `../chord/internal/theme/{presets,extract,dynamic}.go`         | `TODO`        |
| `TSK-502`    | **WS-FE-PGS** | Settings Page                                              | Reference: `../chord/appui/settings_view.go`                              | `TODO`        |
| `TSK-503`    | **WS-FE-KIT** | Keyboard Shortcuts & Motion Kit                            | Reference: [`../chord/docs/notes/TSK-219-motion-loading-kit.md`](file:///run/media/destiny/Ventoy/dr_projects/chord/docs/notes/TSK-219-motion-loading-kit.md) | `TODO` |
| `TSK-601`    | **WS-PLG**    | Linux MPRIS & Media Keys Wiring                            | Reference: [`../chord/docs/notes/TSK-302-mpris-linux-integration.md`](file:///run/media/destiny/Ventoy/dr_projects/chord/docs/notes/TSK-302-mpris-linux-integration.md) | `TODO` |
| `TSK-602`    | **WS-PLG**    | Discord RPC + Scrobblers Wiring                            | Reference: `../chord/internal/plugin/{discord,scrobbler}.go`              | `TODO`        |
| `TSK-603`    | **WS-BND**    | SPA Asset-Server Middleware + WASM Plugin Host             | Reference: `../chord/internal/plugin/host.go`                             | `TODO`        |

---

## 2. Multi-Agent Concurrency Rules Summary

1. **Atomic Claiming:** Always update `docs/PROGRESS.md` to `IN_PROGRESS [Agent-ID]` with a
   start timestamp before creating code files.
2. **File Scope Isolation:** Respect directory boundaries per workstream table in `PROGRESS.md`.
3. **Decoupled Interfaces:** Frontend components touch backend state only through
   `frontend/src/lib/api.ts` wrappers and Wails events. Backend subsystems only interact via
   `internal/model` types.
4. **Verification Gate:** Backend: `go build ./...` && `go test ./...`. Frontend: `bun run build`
   (from `frontend/`). Interactive tasks: `wails dev` smoke check.
