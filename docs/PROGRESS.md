# Multi-Agent Progress & Coordination Board (Wails Port)

This document coordinates concurrent agent contributions on the **Chordly → Wails port**
without collisions, race conditions, or merge conflicts.

Parent plan: [`implementation-plan.md`](implementation-plan.md) · Per-task specs: [`notes/`](notes/README.md)

---

## 1. Multi-Agent Concurrency Protocol

To allow multiple autonomous agents to work concurrently on the codebase:

### Rule 1: Atomic Task Claiming

Before creating or editing any files, an agent **MUST**:

1. Inspect the **Task Matrix** below.
2. Select an unblocked task in status `TODO`.
3. Update the task row:
   - Change status to `IN_PROGRESS`.
   - Add your identifier (e.g. `Agent-FE-Shell`, `Agent-Backend`, or your session ID).
   - Note the start timestamp (UTC).
4. Verify that no other task in `IN_PROGRESS` shares the same files in **File Scope**.

### Rule 2: Strict File Boundary Isolation

- Agents **ONLY** create and edit files listed in their task's **File Scope**.
- **Shared Extension Points** (e.g. `app.go`, `docs/README.md` roadmap tree):
  - Only claims that explicitly own an integration task (`TSK-104`, `TSK-204`, phase
    integrations) may edit them.
  - Every other workstream must expose standalone constructors / modules and register
    through the designated integration task.
- `app.go` additions are **additive binding method blocks only** — never rename or mutate
  existing signatures.
- `docs/PROGRESS.md` rows are edited **only** by the claiming/completing agent of that row
  (status edits are inherently atomic claims; keep them to the task's own row).

### Rule 3: Interface-Driven Decoupling

- Frontend communicates with the backend **only** through the binding methods and events
  named in [`implementation-plan.md` §3.1](implementation-plan.md#31-binding--event-contract-phase-1-target)
  and [`notes/TSK-104-bindings-events.md`](notes/TSK-104-bindings-events.md).
- Frontend modules import the generated `frontend/wailsjs/go/main/App.js` only through
  `frontend/src/lib/api.ts` wrappers (`WS-FE-KIT` scope) — never directly from components.
- Backend subsystems interact via `internal/model` types; no direct hard dependencies
  between two in-progress modules.

### Rule 4: Verification Gate Before Completion

Before marking a task `DONE`:

1. Backend tasks: `go build ./...` and `go test ./...` must pass with 0 errors.
2. Frontend tasks: `bun run build` (in `frontend/`, matches the existing `bun.lock`) must
   succeed; for interactive tasks, confirm a `wails dev` smoke check.
3. Update the task row to `DONE` with a brief summary (git commit hash if the repo uses git).
4. Unblock any dependent tasks (`BLOCKED [ID]` → `TODO`).

---

## 2. Workstream File Ownership Boundaries

| Workstream    | Package Path                          | Allowed File Scope                                                        | Primary Responsibility                                        |
| ------------- | ------------------------------------- | ------------------------------------------------------------------------- | ------------------------------------------------------------- |
| **WS-MIG**    | `internal/*` (transplanted)           | `go.mod`, `go.sum`, `internal/**` (initial copy)                          | Transplant packages from `../chord`, module rename, go bump    |
| **WS-YTM**    | `internal/ytm`                        | `internal/ytm/*`                                                          | InnerTube API; decouple push methods from old signal state     |
| **WS-PLR**    | `player/`                             | `player/*.go`                                                             | `PlayerService`: audio pipeline, queue logic, event emission   |
| **WS-BND**    | root app                              | `app.go`, `main.go`, `wails.json`, `frontend/wailsjs/**` (generated)       | App bindings, event bus, startup wiring; integration tasks     |
| **WS-FE-KIT** | `frontend` tooling & bridges          | `frontend/package.json`, `vite.config.ts`, `index.html`, `src/app.css`, `src/lib/**`, `src/main.*` | Solid + TanStack + DaisyUI setup; event-bridge stores; api.ts |
| **WS-FE-SHELL** | `frontend/src/components`           | `src/components/**`, `src/routes/__root.tsx`, `src/app-globals.css` (scoped sections) | Shell, NavRail, TopBar, BottomPlayer, loaders, toaster |
| **WS-FE-PGS** | `frontend/src/routes` & page views    | `src/routes/**` (except `__root.tsx`), `src/views/**`                     | Home, Search, detail pages, queue, auth, lyrics, settings      |
| **WS-THM**    | `internal/theme`, `src/theme`         | `internal/theme/**`, `src/theme/**`                                       | Palette presets, artwork color extraction → CSS variables      |
| **WS-PLG**    | `internal/plugin`                     | `internal/plugin/**`                                                      | MPRIS, Discord RPC, scrobblers, WASM host (Phase 6, deferred)  |

> Note: `WS-FE-SHELL` and `WS-FE-PGS` may scaffold against **mocked stores** while
> `TSK-201` is in flight; runtime verification waits for the bridge. This keeps layout
> work parallelizable behind `TSK-201`.

---

## 3. Master Task Matrix

### Legend

- `TODO`: Available to be claimed.
- `IN_PROGRESS [Agent-ID]`: Claimed and actively being developed.
- `BLOCKED [Dependency-ID]`: Waiting for another task to complete.
- `DONE`: Completed, tested, and verified.

---

### Phase 0: Frontend Scaffold

| Task ID   | Workstream  | Description                                                                                                  | File Scope                                                                                                   | Dependencies | Status | Owner |
| --------- | ----------- | ------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------- | ------------ | ------ | ----- |
| `TSK-001` | **WS-FE-KIT** | **Solid + TanStack + DaisyUI Toolchain**: `vite-plugin-solid`, Tailwind 4 CSS-first config + DaisyUI 5 plugin, TanStack Router with hash history, typed route tree stub | `frontend/package.json`, `frontend/vite.config.ts`, `frontend/index.html`, `frontend/src/main.*`, `frontend/src/app.css`, `frontend/src/router.tsx`, `frontend/tsconfig.json` | None | `DONE` (2026-11-05, Agent-Session-FE: solid-js 1.9.18, @tanstack/solid-router 1.170.43, tailwind 4.3.4, daisyui 5.7.50; `bun run build` ✓, `tsc --noEmit` ✓; legacy main.js/style.css removed; hash-history router w/ `/`+`/search` stubs) | Agent-Session-FE |

---

### Phase 1: Backend Core (Transplant + Player Service)

| Task ID   | Workstream  | Description                                                                                                                              | File Scope                                                                                              | Dependencies              | Status | Owner |
| --------- | ----------- | ----------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------- | ------------------------- | ------ | ----- |
| `TSK-101` | **WS-MIG**  | **Transplant Internal Packages**: copy `internal/{ytm,model,session,audio,cache,lyrics}` from `../chord`, rename imports `chordly/...` → `go-chordly/...`, bump `go.mod` toolchain | `go.mod`, `go.sum`, `internal/**`, docs spec: [`TSK-101`](notes/TSK-101-internal-transplant.md)          | None                      | `IN_PROGRESS [Agent-Session-MIG]` 2026-11-05 | Agent-Session-MIG |
| `TSK-102` | **WS-YTM**  | **Decouple ytm service**: strip push methods (`SyncHomeData`, `LiveSearch`, `StartRadioForTrack`, `FetchRadioContinuation`) that write into gogpu `internal/state`; expose pull APIs + continuation holder | `internal/ytm/service.go`, `internal/ytm/service_test.go`                                                | `TSK-101`                 | `TODO` | — |
| `TSK-103` | **WS-PLR**  | **PlayerService Port**: audio pipeline + queue logic + event emission (ports `appui/playback.go` + `internal/state` queue funcs)          | `player/player.go`, `player/player_test.go`, docs spec: [`TSK-103`](notes/TSK-103-player-service.md)     | `TSK-101`                 | `TODO` | — |
| `TSK-104` | **WS-BND**  | **App Bindings & Events**: binding methods per contract §3.1, event emission wiring, startup restoration (session → auth → player)          | `app.go`, `main.go`, docs spec: [`TSK-104`](notes/TSK-104-bindings-events.md)                             | `TSK-101` `TSK-102` `TSK-103` | `TODO` | — |

---

### Phase 2: Vertical Slice (Playback End-To-End)

| Task ID   | Workstream     | Description                                                                                                            | File Scope                                                                                     | Dependencies | Status | Owner |
| --------- | -------------- | ----------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------- | ------------ | ------ | ----- |
| `TSK-201` | **WS-FE-KIT**  | **Event Bridge & API Wrappers**: Solid stores mirroring old signals; typed wrappers over generated bindings; event subscriptions (`player:progress`, `player:status`, `player:queue`, `auth:changed`, `toast`) | `frontend/src/lib/**`                                                 | `TSK-104`    | `TODO` | — |
| `TSK-202` | **WS-FE-SHELL**| **Shell & Player Chrome**: `Shell` grid layout, `NavRail`, `TopBar`, `BottomPlayer` (scrubber/volume/controls), `Spinner`/`Skeleton*`, `Toaster` | `frontend/src/components/**`, `frontend/src/routes/__root.tsx`, docs spec: [`TSK-202`](notes/TSK-202-shell-components.md) | `TSK-001` | `TODO` | — |
| `TSK-203` | **WS-FE-PGS**  | **Core Routes**: `/` (home feed), `/search`, `*` (not-found) rendered from live bindings; track click → play            | `frontend/src/routes/**` (except `__root.tsx`), `frontend/src/views/**`                        | `TSK-201` `TSK-202` | `TODO` | — |
| `TSK-204` | **WS-BND**     | **Vertical-Slice Integration**: `wails dev` run-through; fix seams between frontend/backend; smoke checklist in note     | `app.go`, `main.go`, `docs/notes/TSK-204-vertical-slice-verification.md`                      | `TSK-203`    | `TODO` | — |

**Definition of done (Phase 2):** launch `wails dev`, search a song, play it, scrub, adjust
volume, see live status in the bottom player — with zero gogpu code.

---

### Phase 3: Browse & Queue

| Task ID   | Workstream     | Description                                                                                                      | File Scope                                                                                        | Dependencies | Status | Owner |
| --------- | -------------- | ----------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------- | ------------ | ------ | ----- |
| `TSK-301` | **WS-FE-PGS**  | **Detail Pages**: `/album/$id`, `/artist/$id`, `/playlist/$id`, `/mix/$id`; `TrackList` + card components; continuation pagination | `frontend/src/routes/{album,artist,playlist,mix}/**`, `frontend/src/views/**`, `frontend/src/components/**` (new card files only) | `TSK-203` | `TODO` | — |
| `TSK-302` | **WS-FE-PGS**  | **Queue Completion**: `/queue` + slide-in QueuePanel, drag-reorder, remove/clear, active-row indicator, automix continuation | `frontend/src/routes/queue*`, `frontend/src/components/queue*`, bindings via WS-BND coordination | `TSK-201`    | `TODO` | — |

---

### Phase 4: Sessions, Lyrics, Library

| Task ID   | Workstream     | Description                                                                                                          | File Scope                                                                                          | Dependencies | Status | Owner |
| --------- | -------------- | --------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------- | ------------ | ------ | ----- |
| `TSK-401` | **WS-FE-PGS**  | **Session Import UI**: `/login` (paste/file InnerTune export & cookies.txt), `/account`, sign-out, `auth:changed` wiring | `frontend/src/routes/{login,account}/**`, `frontend/src/views/auth/**`                              | `TSK-204`    | `TODO` | — |
| `TSK-402` | **WS-FE-PGS**  | **Full-Screen Player + Synced Lyrics**: `/player` expand transition, `/lyrics` overlay w/ `ActiveLineIndex(playhead)` + seek-on-click | `frontend/src/routes/{player,lyrics}/**`, `frontend/src/views/**`                                   | `TSK-204`    | `TODO` | — |
| `TSK-403` | **WS-FE-PGS**  | **Library Shell**: favorites/playlists/history/downloads tabs on SQLite cache; binding extensions coordinated through WS-BND | `frontend/src/routes/library/**`, `frontend/src/views/library/**`, `app.go` (additive block, coordinated) | `TSK-204` | `TODO` | — |

---

### Phase 5: Themes & Polish

| Task ID   | Workstream  | Description                                                                                                           | File Scope                                                                  | Dependencies | Status | Owner |
| --------- | ----------- | ---------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------- | ------------ | ------ | ----- |
| `TSK-501` | **WS-THM**  | **Palette Engine Port**: port `internal/theme` presets/extraction (drop gogpu widget deps), emit `theme:palette` event → CSS variables | `internal/theme/**`, `frontend/src/theme/**`, `app.css` (variable section) | `TSK-202`    | `TODO` | — |
| `TSK-502` | **WS-FE-PGS** | **Settings Page**: theme presets, accent picker, audio sink picker, cache mgmt, reduced-motion toggle             | `frontend/src/routes/settings/**`                                             | `TSK-501`    | `TODO` | — |
| `TSK-503` | **WS-FE-KIT** | **Keyboard Shortcuts & Motion**: space/arrows//search, reduced-motion, route transitions                          | `frontend/src/lib/**`, `frontend/src/routes/__root.tsx` (coordinated)         | `TSK-201`    | `TODO` | — |

---

### Phase 6: System Integration (Optional / Deferred)

| Task ID   | Workstream  | Description                                                                                                  | File Scope                                                       | Dependencies | Status | Owner |
| --------- | ----------- | --------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------ | ------------ | ------ | ----- |
| `TSK-601` | **WS-PLG**  | **Linux MPRIS & Media Keys**: wire existing `internal/plugin/mpris_linux.go` into `PlayerService`             | `internal/plugin/**`, `player/player.go` (registration only)       | `TSK-204`    | `TODO` | — |
| `TSK-602` | **WS-PLG**  | **Discord RPC + Scrobblers**: wire existing plugin package into track-change/tick events                       | `internal/plugin/**`, `player/player.go` (registration only)       | `TSK-204`    | `TODO` | — |
| `TSK-603` | **WS-BND**  | **SPA Asset-Server Middleware + WASM Plugin Host**: custom `AssetServer` fallback → drop hash history          | `main.go`, `wails.json`                                             | `TSK-204`    | `TODO` | — |

---

## 4. Integration & Coordination Checkpoints

When an agent completes a task:

1. Ensure that task's tests pass: `go test -v ./<package>/...` (backend) or `bun run build` (frontend).
2. Commit with a message mentioning the Task ID: `git commit -m "[TSK-103] Port playback engine to PlayerService"`.
3. Update this file (`docs/PROGRESS.md`):
   - Move the task to `DONE`.
   - Unblock any dependent tasks (change `BLOCKED [ID]` to `TODO`).

---

## 5. Conflict Resolution Guidelines

- If two agents inadvertently modify the same file, the agent holding the official task claim
  in the matrix takes precedence.
- Configuration constants, event names, and binding signatures must be introduced as
  **additive** declarations rather than mutating existing ones.
- Contract changes (§3.1 binding/event names) require updating both
  `implementation-plan.md` §3.1 and `notes/TSK-104-bindings-events.md` in the same change set.
