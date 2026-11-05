# Task Specification: TSK-202 (Shell, Navigation & Player Chrome)

**Workstream:** `WS-FE-SHELL`
**Task ID:** `TSK-202`
**File Scope:** `frontend/src/components/**`, `frontend/src/routes/__root.tsx`
**Dependencies:** `TSK-001` (may mock stores from TSK-201 in the interim)
**Status:** `TODO`

---

## 1. Objective

Rebuild the app chrome from `../chord/appui/{shell,sidebar,topbar,bottom_player,
scrubber,loading,toast,up_next}.go` as SolidJS components with DaisyUI/Tailwind,
driven by the store bridge. The layout is a persistent chrome (rail + top bar + page
viewport + bottom player) with one routed page slot — this is the direct analogue of
the gogpu `NewShell` widget.

---

## 2. Component Port Map

| Component | Ported from | DaisyUI notes |
| --- | --- | --- |
| `Shell` | `appui/shell.go` | CSS grid: `rail / topbar / content` rows + fixed bottom player; queue panel as right column when open |
| `NavRail` | `appui/sidebar.go` | vertical `menu`; items: Home, Search (later additions stacked) |
| `TopBar` | `appui/topbar.go`, `textfield.go` | search input (native `<input>` replaces the hand-rolled gogpu TextField + IME for free) |
| `BottomPlayer` | `bottom_player.go` | track info, play/pause/next/prev, repeat/shuffle toggles, queue button |
| `Scrubber` | `scrubber.go` | range input styled + `durationMS` from `player:progress`; replaces `SeekRequest`/`IsScrubbing` signals |
| `VolumeSlider` | `scrubber.go` | range input → `SetVolume` binding |
| `Spinner` / `Skeleton*` | `loading.go` | DaisyUI `loading` / `skeleton` classes |
| `Toaster` | `toast.go` | DaisyUI alerts; subscribes to `toast` event; API parity with `ShowInfo/Success/Warning/Error` |

Constraints: navigation uses the router (no global `AppRouter` signal). The Shrinking
text widget (`textfit.go`) is not ported — CSS `clamp()` + browser layout replace it.

---

## 3. State Contract

- Components read only from the TSK-201 store bridge (`frontend/src/lib/stores.ts`),
  never from `frontend/wailsjs` or `EventsOn` directly.
- While TSK-201 is in flight this task may ship `stores.ts` with **mocked in-memory
  values behind the same export surface**; runtime wiring flips when TSK-201 lands.
  Coordination rule: only TSK-201's owner may edit `lib/stores.ts` after handoff.

---

## 4. Verification

- `bun run build` green (from `frontend/`).
- `wails dev` smoke: chrome renders around a placeholder route; bottom player shows
  mocked track state; toasts fire from a temporary dev button (removed by TSK-203+).
