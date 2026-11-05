# Task Specification: TSK-203 (Core Routes: Home, Search, NotFound)

**Workstream:** `WS-FE-PGS`
**Task ID:** `TSK-203`
**File Scope:** `frontend/src/routes/**` (except `__root.tsx`), `frontend/src/views/**`
**Dependencies:** `TSK-201`, `TSK-202`
**Status:** `TODO`

---

## 1. Objective

First live-data routes, completing the vertical slice defined in PROGRESS.md Phase 2:
home feed with real InnerTube data, search with track results, not-found catch-all —
with a working click-to-play loop into the backend audio pipeline.

---

## 2. Component Port Map

| View / Component | Ported from | Notes |
| --- | --- | --- |
| `Home` (`/`) | `appui/home_view.go`, `hero.go`, `made_for_you.go`, `new_releases.go` | hero banner from `HomeFeedResult.HeroTrack`; shelves render `MixCard`s and track grids from `GetHomeFeed()` |
| `Search` (`/search`) | `appui/search_view.go` (974 LOC) | results list from `Search(query)`; query from TopBar submission routed to `/search?q=`; filter pills later |
| `NotFound` (`*`) | `appui/pages.go` | minimal |
| `TrackCard` / `ShelfCard` / `MixCard` | `appui/components.go` | shared card primitives; live in `src/components/` (scope: coordinate with WS-FE-SHELL if same file needed) |
| Play affordances | `components.go` (+ `NewPillPlayButton`) | click → `api.Play(track)`; context-add → `api.AppendToQueue(track)` |

Route skeleton: TanStack router with params matches the original route table
(`implementation-plan.md` §5/F2). `/explore` alias lands with Phase 5 polish, skip here.

---

## 3. Behavior Notes

- Loading: use TSK-202 `Skeleton*` while bindings resolve; empty/error states mirror
  the original's fallback copy (the ytm synthetic-album fallback data is *not* ported —
  render a real error card instead).
- The `FEmusic_search`-based search is songs-only initially; categorized filter tokens
  arrive when detail pages land (`TSK-301`).
- No polling loops: initial fetch via binding + event-driven store updates only.

---

## 4. Verification

- `bun run build` green.
- Accepts Phase 2 definition of done together with TSK-204:
  `wails dev` → search a song → play → scrub → volume → bottom player reflects live
  state; zero gogpu code, zero polling.
