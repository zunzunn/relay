    # Relay — Build Progress

## Status: In Development (Phase 5 — Recording blocked)

Last updated: 2026-04-18

---

## Phase 1: Foundation ✅ COMPLETE

| File | Status |
|------|--------|
| `go.mod` | ✅ Module + dependencies defined |
| `pkg/relay/message.go` | ✅ All message types (JoinRoom, TerminalData, CursorMove, CommandQueue, ChatMessage, Marker, etc.) |
| `pkg/relay/room.go` | ✅ RoomRegistry, Room, GenerateRoomCode, user management |
| `pkg/relay/queue.go` | ✅ CmdQueue with Enqueue, Approve, Reject, Pending, All |
| `pkg/relay/client.go` | ✅ WSClient with ReadPump, WritePump, SendJSON, Close |

## Phase 2: Host ✅ COMPLETE

| File | Status |
|------|--------|
| `cmd/server/main.go` | ✅ HTTP+WebSocket server on :8787, room management, broadcast routing |
| `pkg/session/pty.go` | ✅ SpawnPTY, ReadLoop (base64), InjectCommand, CursorPoller, resize |
| `cmd/host/main.go` | ✅ `relay host` — PTY + WS bridge, command injection, cursor polling |

## Phase 3: Joiner ✅ COMPLETE

| File | Status |
|------|--------|
| `pkg/session/terminal.go` | ✅ Written — VirtualTerminal, ANSI parser, split-view rendering |
| `pkg/cursor/cursor.go` | ✅ Written — Cursor registry, ANSI badge overlay |
| `cmd/relay/main.go` | ✅ Full implementation — join, cmd, approve, reject, chat, mark subcommands |

## Phase 4: Collaboration ✅ COMPLETE (in cmd/relay)

- `relay cmd` — queue a command for host approval
- `relay approve` / `relay reject` — host manages command queue
- `relay chat` — text chat in sidebar
- `relay mark` / `relay mark remove` — numbered markers

## Phase 5: Recording + Playback ⏳ NOT STARTED

| File | Status |
|------|--------|
| `pkg/record/record.go` | ❌ Does not exist |
| `relay record` subcommand | ❌ Not implemented |
| `relay playback` subcommand | ❌ Not implemented |

---

## Build Status

```
go build ./cmd/server  ✅
go build ./cmd/host    ✅
go build ./cmd/relay   ✅
```

---

## What's Left

1. Create `pkg/record/record.go` — JSONL recorder + playback engine (`relay record` / `relay playback`)
2. Full end-to-end test: `relay server` + `relay host` + `relay join` in three terminals
