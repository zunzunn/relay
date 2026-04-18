package host

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/relay-dev/relay/pkg/record"
	"github.com/relay-dev/relay/pkg/relay"
	"github.com/relay-dev/relay/pkg/session"
	"golang.org/x/term"
)

// Logger provides structured logging with [INFO], [WARN], [ERROR] prefixes.
type Logger struct{}

func (Logger) Info(format string, args ...interface{}) {
	log.Printf("[INFO]  "+format, args...)
}

func (Logger) Warn(format string, args ...interface{}) {
	log.Printf("[WARN]  "+format, args...)
}

func (Logger) Error(format string, args ...interface{}) {
	log.Printf("[ERROR] "+format, args...)
}

var logger = Logger{}

var dialer = websocket.DefaultDialer

type pendingCmd struct {
	id      string
	user    string
	command string
	time    time.Time
}

type Config struct {
	ServerAddr string
	Password   string
	RecordPath string
}

type hostState struct {
	queue   map[string]*pendingCmd
	queueMu sync.Mutex
}

func Run(cfg Config) error {
	roomCode := relay.GenerateRoomCode()

	cols, rows, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		cols, rows = 80, 24
	}

	u := url.URL{Scheme: "ws", Host: cfg.ServerAddr, Path: "/ws"}
	q := u.Query()
	q.Set("username", "host")
	q.Set("is_host", "true")
	u.RawQuery = q.Encode()

	conn, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		return fmt.Errorf("connect to relay server at %s: %w", cfg.ServerAddr, err)
	}
	defer conn.Close()

	joinMsg := relay.Message{
		Type: relay.MsgJoinRoom,
		Payload: relay.JoinRoom{
			RoomCode:   roomCode,
			Password:   cfg.Password,
			Username:   "host",
			IsHost:     true,
			TerminalW:  cols,
			TerminalH: rows,
		},
	}
	if err := conn.WriteJSON(joinMsg); err != nil {
		return fmt.Errorf("send join: %w", err)
	}

	var roomJoined relay.Message
	if err := conn.ReadJSON(&roomJoined); err != nil {
		return fmt.Errorf("read room_joined response: %w", err)
	}
	if roomJoined.Type != relay.MsgRoomJoined {
		return fmt.Errorf("expected room_joined, got %s", roomJoined.Type)
	}

	ptySession, err := session.SpawnPTY(cols, rows)
	if err != nil {
		return fmt.Errorf("spawn PTY (terminal %dx%d): %w", cols, rows, err)
	}
	defer ptySession.File.Close()
	ptySession.Cmd.Wait()

	hostID := ""
	if payload, ok := roomJoined.Payload.(map[string]interface{}); ok {
		if hid, ok := payload["host_id"].(string); ok {
			hostID = hid
		}
	}

	fmt.Println("=== Relay Session ===")
	fmt.Printf("  Room code: %s\n", roomCode)
	if cfg.Password != "" {
		fmt.Printf("  Password: %s\n", cfg.Password)
	}
	fmt.Printf("  Share with: relay join %s\n", roomCode)
	if cfg.RecordPath != "" {
		fmt.Printf("  Recording to: %s\n", cfg.RecordPath)
	}
	fmt.Println("\n[Press Ctrl+C to end session]")

	hs := &hostState{queue: make(map[string]*pendingCmd)}
	var wg sync.WaitGroup
	var seq uint64

	var rec *record.RecordWriter
	if cfg.RecordPath != "" {
		rec, err = record.NewRecordWriter(cfg.RecordPath, cols, rows)
		if err != nil {
			ptySession.File.Close()
			ptySession.Cmd.Wait()
			return fmt.Errorf("open recording file %q: %w", cfg.RecordPath, err)
		}
		defer rec.Close()
	}

	// PTY -> WebSocket
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := ptySession.ReadLoop(func(b64 string) {
			seq++
			msg := relay.NewMessage(relay.MsgTerminalData, relay.TerminalData{Data: b64, Seq: seq})
			if rec != nil {
				if err := rec.Write(msg); err != nil {
					logger.Warn("recording write: %v", err)
				}
			}
			conn.WriteJSON(msg)
		})
		if err != nil {
			logger.Error("PTY read: %v", err)
		}
	}()

	// WebSocket -> PTY
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg relay.Message
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			switch msg.Type {
			case relay.MsgCommandQueue:
				if p, ok := msg.Payload.(map[string]interface{}); ok {
					cmdID, _ := p["command_id"].(string)
					user, _ := p["username"].(string)
					cmdStr, _ := p["command"].(string)
					if cmdID != "" && cmdStr != "" {
						hs.queueMu.Lock()
						hs.queue[cmdID] = &pendingCmd{id: cmdID, user: user, command: cmdStr, time: time.Now()}
						hs.queueMu.Unlock()
						fmt.Fprintf(os.Stderr, "\n[CMD QUEUED] id=%s user=%s cmd=%q\n", cmdID, user, cmdStr)
					}
				}
			case relay.MsgCommandApprove:
				if p, ok := msg.Payload.(map[string]interface{}); ok {
					cmdID, _ := p["command_id"].(string)
					hs.queueMu.Lock()
					cmd, ok := hs.queue[cmdID]
					delete(hs.queue, cmdID)
					hs.queueMu.Unlock()
					if ok {
						fmt.Fprintf(os.Stderr, "\n[CMD APPROVED] %s (user=%s)\n", cmd.command, cmd.user)
						session.InjectCommand(ptySession, cmd.command)
					}
				}
			case relay.MsgCommandReject:
				if p, ok := msg.Payload.(map[string]interface{}); ok {
					cmdID, _ := p["command_id"].(string)
					reason, _ := p["reason"].(string)
					hs.queueMu.Lock()
					delete(hs.queue, cmdID)
					hs.queueMu.Unlock()
					fmt.Fprintf(os.Stderr, "\n[CMD REJECTED] %s%s\n", cmdID, map[bool]string{true: ": " + reason}[reason != ""])
				}
			case relay.MsgResize:
				if p, ok := msg.Payload.(map[string]interface{}); ok {
					w, _ := p["width"].(float64)
					h, _ := p["height"].(float64)
					ptySession.Resize(int(w), int(h))
					if rec != nil {
						if err := rec.Write(&msg); err != nil {
							logger.Warn("recording write: %v", err)
						}
					}
				}
			case relay.MsgPing:
				conn.WriteJSON(relay.NewMessage(relay.MsgPong, nil))
			}
		}
	}()

	// Resize monitor
	wg.Add(1)
	go func() {
		defer wg.Done()
		prevCols, prevRows := cols, rows
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			c, r, _ := ptySession.GetSize()
			if c != prevCols || r != prevRows {
				prevCols, prevRows = c, r
				ptySession.Resize(c, r)
				msg := relay.NewMessage(relay.MsgResize, relay.Resize{Width: c, Height: r})
				if rec != nil {
					if err := rec.Write(msg); err != nil {
						logger.Warn("recording write: %v", err)
					}
				}
				conn.WriteJSON(msg)
			}
		}
	}()

	// Cursor position broadcaster
	wg.Add(1)
	go func() {
		defer wg.Done()
		session.StartCursorPoller(ptySession.File, 100*time.Millisecond, func(x, y int) {
			if x < 0 || y < 0 {
				return
			}
			msg := relay.NewMessage(relay.MsgCursorMove, relay.CursorMove{
				UserID:   hostID,
				Username: "host",
				X:        x,
				Y:        y,
				Color:    "#FF6B6B",
			})
			if rec != nil {
				if err := rec.Write(msg); err != nil {
					logger.Warn("recording write: %v", err)
				}
			}
			conn.WriteJSON(msg)
		})
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	fmt.Println("\n[Ending session...]")
	conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	wg.Wait()
	return nil
}