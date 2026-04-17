package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	relay "github.com/relay-dev/relay/pkg/relay"
	"github.com/relay-dev/relay/pkg/record"
	"github.com/relay-dev/relay/pkg/session"
	"golang.org/x/term"
)

var dialer = websocket.DefaultDialer

func main() {
	serverAddr := flag.String("server", "localhost:8787", "Relay server address")
	password := flag.String("password", "", "Optional room password")
	recordPath := flag.String("record", "", "Record session to a JSONL file")
	flag.Parse()

	roomCode := relay.GenerateRoomCode()

	// Get terminal size
	cols, rows, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		cols, rows = 80, 24
	}

	u := url.URL{Scheme: "ws", Host: *serverAddr, Path: "/ws"}
	q := u.Query()
	q.Set("username", "host")
	q.Set("is_host", "true")
	u.RawQuery = q.Encode()

	conn, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatalf("failed to connect to relay server: %v", err)
	}
	defer conn.Close()

	// Send join message
	joinMsg := relay.Message{
		Type: relay.MsgJoinRoom,
		Payload: relay.JoinRoom{
			RoomCode:  roomCode,
			Password:  *password,
			Username:  "host",
			IsHost:    true,
			TerminalW: cols,
			TerminalH: rows,
		},
	}
	if err := conn.WriteJSON(joinMsg); err != nil {
		log.Fatalf("failed to send join: %v", err)
	}

	// Read RoomJoined confirmation
	var roomJoined relay.Message
	if err := conn.ReadJSON(&roomJoined); err != nil {
		log.Fatalf("failed to read room joined: %v", err)
	}
	if roomJoined.Type != relay.MsgRoomJoined {
		log.Fatalf("unexpected message: %v", roomJoined.Type)
	}

	// Spawn PTY
	ptySession, err := session.SpawnPTY(cols, rows)
	if err != nil {
		log.Fatalf("failed to spawn PTY: %v", err)
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
	if *password != "" {
		fmt.Printf("  Password: %s\n", *password)
	}
	fmt.Printf("  Share with: relay join %s\n", roomCode)
	if *recordPath != "" {
		fmt.Printf("  Recording to: %s\n", *recordPath)
	}
	fmt.Println("\n[Press Ctrl+C to end session]")

	var wg sync.WaitGroup
	var seq uint64

	// Create recorder if --record was specified
	var rec *record.RecordWriter
	if *recordPath != "" {
		rec, err = record.NewRecordWriter(*recordPath, cols, rows)
		if err != nil {
			log.Fatalf("failed to create recorder: %v", err)
		}
		defer rec.Close()
	}

	// PTY → WebSocket: broadcast terminal data
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := ptySession.ReadLoop(func(b64 string) {
			seq++
			msg := relay.NewMessage(relay.MsgTerminalData, relay.TerminalData{Data: b64, Seq: seq})
			if rec != nil {
				rec.Write(msg)
			}
			conn.WriteJSON(msg)
		})
		if err != nil {
			log.Printf("PTY read error: %v", err)
		}
	}()

	// WebSocket → PTY: handle commands and resize
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
			case relay.MsgCommandApprove:
				if p, ok := msg.Payload.(map[string]interface{}); ok {
					if cmd, ok := p["command"].(string); ok {
						session.InjectCommand(ptySession, cmd)
					}
				}
			case relay.MsgResize:
				if p, ok := msg.Payload.(map[string]interface{}); ok {
					w, _ := p["width"].(float64)
					h, _ := p["height"].(float64)
					ptySession.Resize(int(w), int(h))
					if rec != nil {
						rec.Write(&msg)
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
					rec.Write(msg)
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
				rec.Write(msg)
			}
			conn.WriteJSON(msg)
		})
	}()

	// Wait for interrupt
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	fmt.Println("\n[Ending session...]")
	conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))

	// Give goroutines a moment to finish
	wg.Wait()
}