# Task Specification: TSK-104 (App Bindings, Event Bus & Startup Wiring)

**Workstream:** `WS-BND`
**Task ID:** `TSK-104`
**File Scope:** `app.go`, `main.go`, `wails.json`
**Dependencies:** `TSK-101`, `TSK-102`, `TSK-103`
**Status:** `TODO`

---

## 1. Objective

Expose the ported backend through Wails bindings and the runtime event bus, implementing
the normative contract below. This is the serialization point where
WS-MIG/WS-YTM/WS-PLR work becomes visible to the frontend.

---

## 2. Normative Contract (§3.1 of implementation-plan)

**Auth / session**
| Binding | Returns | Notes |
|---|---|---|
| `GetAccount()` | `{Name, HasSession, Source}` | Restored-session snapshot at startup |
| `ImportSession(raw string)` | error | Reuses `ytm.ParseToken` (InnerTune exports, cookies.txt, JSON bundles) |
| `SignOut()` | error | Clears session store |

**Catalog (wraps decoupled `internal/ytm` service, TSK-102)**
| Binding | Returns |
|---|---|
| `GetHomeFeed()` | `ytm.HomeFeedResult` |
| `Search(query string)` | `[]model.Track` |
| `GetAlbum(id string)` / `GetArtist(id)` / `GetPlaylist(id)` | detail structs |
| `StartRadio(videoID string)` | — (fills queue via PlayerService) |

**Player (ports TSK-103 service)**
`GetPlaybackState()` → initial snapshot (track, status, position/duration, volume, queue,
index, repeat, shuffle) — **bindings are one-shot; the frontend must not poll**, event
subscriptions carry all later updates.
Controls: `Play(track)`, `TogglePlayPause()`, `Pause()`, `Resume()`, `Seek(position)`,
`SetVolume(v)`, `Next()`, `Previous()`, `PlayQueueIndex(n)`, `AppendToQueue(track)`,
`MoveQueueItem(from, to)`, `RemoveQueueItem(n)`, `ClearQueue()`, `SetRepeat(mode)`,
`SetShuffle(bool)`.

**Events (backend → frontend via `runtime.EventsEmit`)**
| Event | Payload | Cadence |
|---|---|---|
| `player:progress` | `{positionMS int, durationMS int}` | every 250 ms while playing |
| `player:status` | `{status string, track model.Track}` | track change / play-pause / buffering |
| `player:queue` | `{queue []model.Track, index int}` | any queue mutation |
| `player:error` | `{message string}` | stream failures |
| `auth:changed` | `{name string, hasSession bool}` | sign-in/out |
| `toast` | `{kind string, message string}` | replaces `appui.ShowToast*` |

---

## 3. Startup Wiring (`main.go`)

Mirrors the original `main.go` order:
1. `session.Open(session.DefaultPath())`; on `Load()` success → `ytm...SetAuth(bundle)`,
   seed account in snapshot.
2. Construct `PlayerService` (TSK-103) with ctx.
3. Fire home-feed sync equivalent (predictive pull so the first `GetHomeFeed` is warm).
4. `wails.Run` with `OnStartup` capturing ctx for event emission; bind `App` struct.
5. Config: title "Chordly – YouTube Music", size 1280×820 (match the original).

---

## 4. Serialization Notes

1. `model.Track.Duration` is `time.Duration` — bindings rely on the pre-baked
   `DurationStr` for display and millisecond integers in event payloads for progress math.
2. All binding method names above are frozen once TSK-201/TSK-202 land; contract changes
   require updating `implementation-plan.md` §3.1 + this note in the same change set.
3. `app.go` stays additive afterwards — later tasks extend it only through coordinated
   additive blocks (see PROGRESS.md Rule 2).
