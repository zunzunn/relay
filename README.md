# Relay

> Real-time collaborative terminal debugging — share your shell session with your team.

**Stage: In Development** | Relay is a CLI tool for sharing a live terminal session over WebSockets. Multiple developers can watch, chat, annotate, and request command approval — all in real time.

---

## What It Does

Relay turns a single terminal session into a shared, collaborative experience.

- **Live terminal sharing** — Anyone can join a room and watch the host's terminal in real time
- **Colored cursors** — Each participant gets a unique color; their cursor position is overlaid on the shared terminal
- **Command approval queue** — Joiners submit commands that the host must approve before they run
- **Text chat** — Side-channel chat without leaving the terminal
- **Numbered markers** — Drop pins on specific lines to highlight areas of interest
- **Session recording + playback** — Record a session to a JSONL file and replay it later

---

## How It Works

```
┌─────────────┐         WebSocket          ┌──────────────────┐
│  relay host │ ◄─────────────────────────►│   relay server  │
│  (PTY/shell)│        :8787 /ws           │   (room hub)    │
└─────────────┘                            └────────┬─────────┘
                                                     │
                              ┌──────────────────────┼──────────────────────┐
                              │                      │                      │
                              ▼                      ▼                      ▼
                       ┌──────────┐           ┌──────────┐           ┌──────────┐
                       │relay join│           │relay join│           │relay join│
                       │ (viewer) │           │ (viewer) │           │ (viewer) │
                       └──────────┘           └──────────┘           └──────────┘
```

### Architecture

- **Host** — Spawns a local pseudo-terminal (PTY) running a shell. All terminal I/O flows through the PTY. The host's terminal is the single source of truth.
- **Server** — A WebSocket hub (`relay server`) that manages rooms, authenticates joiners, routes messages, and coordinates the command approval queue.
- **Joiners** — Connect via WebSocket and receive a live rendered view of the terminal. They are **read-only** by default — they cannot type into the PTY unless the host approves a queued command.
- **Terminal data** — Raw PTY bytes are base64-encoded for safe JSON transport over WebSocket. Joiners maintain a virtual terminal buffer with full ANSI escape sequence support (colors, cursor movement, scrolling).

### Terminal Rendering (Joiners)

Joiners use a **virtual terminal buffer** (2D cell grid + scrollback) that parses incoming ANSI escape sequences from the host's PTY:

- **CUU/CUD/CUF/CUB** — cursor movement
- **CUP / HVP** — absolute positioning
- **ED (2J)** — clear screen
- **EK** — clear line
- **ICH/DCH/DL** — insert/delete characters/lines
- **SGR** — colors and styles (bold, italic, foreground/background true-color)

A split-view renderer composites the terminal output on the left (70%) with a sidebar (30%) showing chat, markers, and the command queue.

### Command Queue Flow

```
Joiner runs:  relay cmd "kill -9 1234"
                    │
                    ▼
           Server receives MsgCommandQueue
                    │
          ┌─────────┴─────────┐
          ▼                   ▼
   Relay to host       Ack to joiner
          │
          ▼
   Host's terminal shows pending command
          │
    ┌─────┴─────┐
    ▼           ▼
relay approve   relay reject
    │           │
    ▼           ▼
 MsgCommandApprove    MsgCommandReject
    │           │
    └─────┬─────┘
          ▼
   Server forwards to joiner
   Command injected to PTY (if approved)
```

---

## Current Stage

**Phase 5 of 5** — All features are implemented. The full stack is built and ready for end-to-end testing.

### Working Today

- `relay server` — WebSocket relay hub on port 8787 ✅
- `relay host` — Spawns a PTY, connects to the server, broadcasts terminal output ✅
- `relay host --record <file>` — Records session to JSONL file ✅
- `relay join` — Interactive joiner with raw terminal + split-view sidebar ✅
- `relay cmd / approve / reject` — Command queue workflow ✅
- `relay chat / mark` — Chat and annotations ✅
- `relay playback [-speed N] <file>` — Replay a recorded session with pause/speed/step controls ✅

---

## Quick Start

```bash
# Terminal 1 — start the server
relay server

# Terminal 2 — host a session
relay host
# Output: Room code: abc123
#         Share with: relay join abc123

# Terminal 2b — record the session
relay host --record /tmp/session.jsonl

# Terminal 3 — join as a viewer
relay join abc123

# Replay a recording later
relay playback -speed 2 /tmp/session.jsonl
```

## Usage

```
relay host                    Host a new terminal session
relay host --record <file>   Record session to JSONL file
relay join <code>            Join an existing session
relay server                  Start the relay server (default :8787)
relay cmd <cmd>              Queue a command for host approval
relay approve <id>           Approve a queued command (host only)
relay reject <id>            Reject a queued command (host only)
relay chat <msg>             Send a chat message
relay mark <n>               Drop marker at line n
relay mark remove             Remove markers
relay playback [-speed N] <f> Replay a recorded session
```

---

## Tech Stack

- **Go 1.25** with `gorilla/websocket`, `creack/pty`, `golang.org/x/term`, `spf13/cobra`
- **No TUI framework** — raw ANSI escape sequences for maximum portability
- **JSONL** for session recording (streaming, one JSON object per line)
- **bcrypt** for optional room passwords
