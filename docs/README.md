# Chordly — Wails Edition (Desktop YouTube Music Player)

Desktop YouTube Music client: **Go backend (Wails v2) + SolidJS frontend** (TanStack Router,
DaisyUI/Tailwind). This is a port of the original GoGPU-UI desktop app
(`/run/media/destiny/Ventoy/dr_projects/chord`); the InnerTube client, audio engine, session
store, SQLite library, and lyrics pipeline are reused nearly verbatim.

---

## Documentation Index

1. [**Multi-Agent Progress & Coordination Board**](PROGRESS.md)
   - Real-time task board with atomic task claims and file lock scopes.
   - Concurrency protocol preventing multi-agent collisions.
   - Workstreams split along frontend/backend/package boundaries
     (`WS-MIG`, `WS-YTM`, `WS-PLR`, `WS-BND`, `WS-FE-KIT`, `WS-FE-SHELL`, `WS-FE-PGS`, `WS-THM`, `WS-PLG`).

2. [**Port Implementation Plan (parent plan)**](implementation-plan.md)
   - Reuse matrix: what ports as-is, what gets re-homed, what gets rebuilt.
   - Target architecture, binding + event contract, route table, phases.

3. [**System Architecture**](architecture.md)
   - Wails-targeted layered architecture, data flow (bindings for reads, events for state churn).

4. [**Task Notes & Specs Directory**](notes/README.md)
   - Per-task implementation blueprints (`TSK-*.md`) for tasks claimed from `PROGRESS.md`.
   - Notes marked `Reference:` point at the source file being ported instead.

---

## Development Roadmap & Execution Phases

```
 Phase 0: Frontend Scaffold
 ├── TSK-001. SolidJS + TanStack Router + DaisyUI toolchain            [DONE]

 Phase 1: Backend Core (transplant + player service)
 ├── TSK-101. Transplant internal/* packages & module imports          [TODO]
 ├── TSK-102. Decouple ytm service push methods from signals           [TODO] (blocked: TSK-101)
 ├── TSK-103. PlayerService port (playback + queue logic)              [TODO] (blocked: TSK-101)
 ├── TSK-104. App bindings, event bus, main wiring                     [TODO] (blocked: TSK-101..103)

 Phase 2: Vertical Slice (playback end-to-end)
 ├── TSK-201. Event bridge stores + typed API wrappers                 [TODO] (blocked: TSK-104)
 ├── TSK-202. Shell, NavRail, TopBar, BottomPlayer, kit                [TODO] (blocked: TSK-001)
 ├── TSK-203. Home, Search, NotFound routes                            [TODO] (blocked: TSK-201+202)
 └── TSK-204. Integration verification (wails dev run-through)         [TODO] (blocked: TSK-203)

 Phase 3: Browse & Queue
 ├── TSK-301. Detail pages (album/artist/playlist/mix) + TrackList     [TODO] (blocked: TSK-203)
 └── TSK-302. Queue panel, drag reorder, radio continuation            [TODO] (blocked: TSK-201)

 Phase 4: Sessions, Lyrics, Library
 ├── TSK-401. Session import UI (login/account/sign-out)               [TODO] (blocked: TSK-204)
 ├── TSK-402. Full-screen player + synced lyrics                       [TODO] (blocked: TSK-204)
 └── TSK-403. Library tabs (favorites/playlists/history/downloads)     [TODO] (blocked: TSK-204)

 Phase 5: Themes & Polish
 ├── TSK-501. Palette engine port → CSS variables branch               [TODO] (blocked: TSK-202)
 ├── TSK-502. Settings page                                            [TODO] (blocked: TSK-501)
 └── TSK-503. Keyboard shortcuts, reduced motion, a11y                 [TODO]

 Phase 6: System Integration (optional)
 ├── TSK-601. Linux MPRIS & media keys                                 [TODO]
 ├── TSK-602. Discord RPC + scrobblers                                 [TODO]
 └── TSK-603. WASM plugin host + SPA asset-server middleware           [TODO]
```

Claim tasks from [PROGRESS.md](PROGRESS.md). Full motivation, stack decisions, and the
binding/event contract live in [implementation-plan.md](implementation-plan.md).
