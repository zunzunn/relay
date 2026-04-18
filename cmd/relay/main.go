package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"time"

	"github.com/gorilla/websocket"
	"github.com/relay-dev/relay/pkg/host"
	"github.com/relay-dev/relay/pkg/playback"
	"github.com/relay-dev/relay/pkg/protocol"
	"github.com/relay-dev/relay/pkg/relay"
	"github.com/relay-dev/relay/pkg/server"
)

var dialer = websocket.DefaultDialer

func main() {
	flag.Parse()

	rootCmd := flag.NewFlagSet("relay", flag.ExitOnError)
	serverAddr := rootCmd.String("server", "localhost:8787", "Relay server address")

	if flag.NArg() == 0 {
		printUsage()
		os.Exit(1)
	}

	switch flag.Arg(0) {
	case "server":
		runServer(flag.Args()[1:])
	case "host":
		runHost(flag.Args()[1:])
	case "join":
		runJoin(rootCmd, *serverAddr, flag.Args()[1:])
	case "cmd":
		runCmd(rootCmd, *serverAddr, flag.Args()[1:])
	case "approve":
		runApprove(rootCmd, *serverAddr, flag.Args()[1:])
	case "reject":
		runReject(rootCmd, *serverAddr, flag.Args()[1:])
	case "chat":
		runChat(rootCmd, *serverAddr, flag.Args()[1:])
	case "mark":
		runMark(rootCmd, *serverAddr, flag.Args()[1:])
	case "record":
		runRecord(flag.Args()[1:])
	case "playback":
		runPlayback(flag.Args()[1:])
	default:
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Usage: relay <command> [flags]")
	fmt.Println("")
	fmt.Println("Commands:")
	fmt.Println("  server              Start the relay WebSocket server")
	fmt.Println("  host                Host a new terminal session")
	fmt.Println("  join <code>         Join an existing terminal session")
	fmt.Println("  cmd <cmd>           Queue a command for host approval")
	fmt.Println("  approve <id>        Approve a queued command (host only)")
	fmt.Println("  reject <id>         Reject a queued command (host only)")
	fmt.Println("  chat <msg>          Send a chat message")
	fmt.Println("  mark [n]            Drop a marker at line n")
	fmt.Println("  mark remove         Remove all markers")
	fmt.Println("  record <file>       Record a session to a JSONL file")
	fmt.Println("  playback <file>    Replay a recorded session")
	fmt.Println("")
	fmt.Println("Flags:")
	fmt.Println("  -server <addr>      Relay server address (default: localhost:8787)")
}

// --- helpers ---

func getPayload(msg *relay.Message) map[string]interface{} {
	if m, ok := msg.Payload.(map[string]interface{}); ok {
		return m
	}
	return nil
}

func getStr(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// --- command implementations ---

func runServer(args []string) {
	serverCmd := flag.NewFlagSet("server", flag.ExitOnError)
	port := serverCmd.Int("port", 8787, "Port to listen on")
	serverCmd.Parse(args)
	server.Run(*port)
}

func runHost(args []string) {
	hostCmd := flag.NewFlagSet("host", flag.ExitOnError)
	serverAddr := hostCmd.String("server", "localhost:8787", "Relay server address")
	password := hostCmd.String("password", "", "Optional room password")
	recordPath := hostCmd.String("record", "", "Record session to a JSONL file")
	hostCmd.Parse(args)

	if err := host.Run(host.Config{
		ServerAddr: *serverAddr,
		Password:   *password,
		RecordPath: *recordPath,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runJoin(rootCmd *flag.FlagSet, serverAddr string, args []string) {
	joinCmd := flag.NewFlagSet("join", flag.ExitOnError)
	username := joinCmd.String("username", "viewer", "Your display name")
	password := joinCmd.String("password", "", "Room password (if required)")
	joinCmd.Parse(args)

	code := joinCmd.Arg(0)
	if code == "" {
		fmt.Fprintln(os.Stderr, "Error: room code required")
		fmt.Fprintln(os.Stderr, "Usage: relay join [-username name] [-password pass] <room-code>")
		os.Exit(1)
	}

	u := url.URL{Scheme: "ws", Host: serverAddr, Path: "/ws"}
	q := u.Query()
	q.Set("username", *username)
	u.RawQuery = q.Encode()

	conn, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to connect to relay server: %v\n", err)
		os.Exit(1)
	}

	joinMsg := relay.Message{
		Type: relay.MsgJoinRoom,
		Payload: relay.JoinRoom{
			RoomCode: code,
			Password: *password,
			Username: *username,
			IsHost:   false,
		},
	}
	if err := conn.WriteJSON(joinMsg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to send join: %v\n", err)
		conn.Close()
		os.Exit(1)
	}

	var roomJoined relay.Message
	if err := conn.ReadJSON(&roomJoined); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to read room joined: %v\n", err)
		conn.Close()
		os.Exit(1)
	}
	if roomJoined.Type != relay.MsgRoomJoined {
		fmt.Fprintln(os.Stderr, "Error: unexpected response from server")
		conn.Close()
		os.Exit(1)
	}

	fmt.Printf("Connected to room %s\n", code)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	done := make(chan struct{})

	go func() {
		<-sig
		close(done)
	}()

	go func() {
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				close(done)
				return
			}
			msg, err := protocol.DecodeMessage(data)
			if err != nil {
				fmt.Println(string(data))
				continue
			}
			switch msg.Type {
			case protocol.TypeRoomJoined:
				var joined protocol.RoomJoinedPayload
				json.Unmarshal(msg.Payload, &joined)
				fmt.Printf("Connected to room %s\n", joined.RoomCode)
			case protocol.TypeTerminalData:
				// Decoded but not rendered yet (Part 6)
			case protocol.TypeChat:
				var chat protocol.ChatPayload
				json.Unmarshal(msg.Payload, &chat)
				fmt.Printf("[%s] %s: %s\n", time.Unix(chat.Timestamp, 0).Format("15:04"), chat.Username, chat.Text)
			case protocol.TypeUserJoined:
				var joined protocol.UserJoinedPayload
				json.Unmarshal(msg.Payload, &joined)
				fmt.Printf("%s joined the room\n", joined.User.Username)
			case protocol.TypeUserLeft:
				var left protocol.UserLeftPayload
				json.Unmarshal(msg.Payload, &left)
				fmt.Printf("%s left the room\n", left.Username)
			case protocol.TypeCommandRequest:
				var req protocol.CommandRequestPayload
				json.Unmarshal(msg.Payload, &req)
				fmt.Printf("[%s] queued: %s\n", req.Username, req.Command)
			case protocol.TypeCommandApprove:
				var approve protocol.CommandApprovePayload
				json.Unmarshal(msg.Payload, &approve)
				fmt.Printf("Command %s approved\n", approve.CommandID)
			case protocol.TypeCommandReject:
				var reject protocol.CommandRejectPayload
				json.Unmarshal(msg.Payload, &reject)
				fmt.Printf("Command %s rejected\n", reject.CommandID)
			case protocol.TypeMarker:
				var marker protocol.MarkerPayload
				json.Unmarshal(msg.Payload, &marker)
				fmt.Printf("[%s] dropped marker at line %d: %s\n", marker.Username, marker.CursorY+1, marker.Note)
			case protocol.TypePong:
				// ignore
			default:
				fmt.Printf("[%s]\n", msg.Type)
			}
		}
	}()

	<-done
	conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	conn.Close()
}

func runCmd(rootCmd *flag.FlagSet, serverAddr string, args []string) {
	cmdFlag := flag.NewFlagSet("cmd", flag.ExitOnError)
	username := cmdFlag.String("username", "viewer", "Your display name")
	cmdFlag.Parse(args)

	cmd := cmdFlag.Arg(0)
	if cmd == "" {
		fmt.Fprintln(os.Stderr, "Error: command required")
		fmt.Fprintln(os.Stderr, "Usage: relay cmd [-username name] <command>")
		os.Exit(1)
	}

	u := url.URL{Scheme: "ws", Host: serverAddr, Path: "/ws"}
	q := u.Query()
	q.Set("username", *username)
	u.RawQuery = q.Encode()

	conn, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: connecting: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	msg := relay.NewMessage(relay.MsgCommandQueue, relay.CommandQueue{
		CommandID: fmt.Sprintf("%d", time.Now().UnixNano()),
		UserID:    *username,
		Username:  *username,
		Command:   cmd,
		Timestamp: time.Now().Unix(),
	})
	if err := conn.WriteJSON(msg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: sending: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Queued: %s\n", cmd)
}

func runApprove(rootCmd *flag.FlagSet, serverAddr string, args []string) {
	cmdFlag := flag.NewFlagSet("approve", flag.ExitOnError)
	username := cmdFlag.String("username", "host", "Your display name")
	cmdFlag.Parse(args)

	cmdID := cmdFlag.Arg(0)
	if cmdID == "" {
		fmt.Fprintln(os.Stderr, "Error: command ID required")
		fmt.Fprintln(os.Stderr, "Usage: relay approve <command-id>")
		os.Exit(1)
	}

	u := url.URL{Scheme: "ws", Host: serverAddr, Path: "/ws"}
	q := u.Query()
	q.Set("username", *username)
	q.Set("is_host", "true")
	u.RawQuery = q.Encode()

	conn, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: connecting: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	msg := relay.NewMessage(relay.MsgCommandApprove, relay.CommandApprove{
		CommandID: cmdID,
		ByUserID:  *username,
	})
	if err := conn.WriteJSON(msg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: sending: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Approved: %s\n", cmdID)
}

func runReject(rootCmd *flag.FlagSet, serverAddr string, args []string) {
	cmdFlag := flag.NewFlagSet("reject", flag.ExitOnError)
	username := cmdFlag.String("username", "host", "Your display name")
	cmdFlag.Parse(args)

	cmdID := cmdFlag.Arg(0)
	if cmdID == "" {
		fmt.Fprintln(os.Stderr, "Error: command ID required")
		fmt.Fprintln(os.Stderr, "Usage: relay reject <command-id>")
		os.Exit(1)
	}

	u := url.URL{Scheme: "ws", Host: serverAddr, Path: "/ws"}
	q := u.Query()
	q.Set("username", *username)
	q.Set("is_host", "true")
	u.RawQuery = q.Encode()

	conn, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: connecting: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	msg := relay.NewMessage(relay.MsgCommandReject, relay.CommandReject{
		CommandID: cmdID,
		ByUserID:  *username,
	})
	if err := conn.WriteJSON(msg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: sending: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Rejected: %s\n", cmdID)
}

func runChat(rootCmd *flag.FlagSet, serverAddr string, args []string) {
	cmdFlag := flag.NewFlagSet("chat", flag.ExitOnError)
	username := cmdFlag.String("username", "viewer", "Your display name")
	cmdFlag.Parse(args)

	text := cmdFlag.Arg(0)
	if text == "" {
		fmt.Fprintln(os.Stderr, "Error: message required")
		fmt.Fprintln(os.Stderr, "Usage: relay chat [-username name] <message>")
		os.Exit(1)
	}

	u := url.URL{Scheme: "ws", Host: serverAddr, Path: "/ws"}
	q := u.Query()
	q.Set("username", *username)
	u.RawQuery = q.Encode()

	conn, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: connecting: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	msg := relay.NewMessage(relay.MsgChatMessage, relay.ChatMessage{
		UserID:    *username,
		Username:  *username,
		Text:      text,
		Timestamp: time.Now().Unix(),
	})
	if err := conn.WriteJSON(msg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: sending: %v\n", err)
		os.Exit(1)
	}
}

func runMark(rootCmd *flag.FlagSet, serverAddr string, args []string) {
	cmdFlag := flag.NewFlagSet("mark", flag.ExitOnError)
	username := cmdFlag.String("username", "viewer", "Your display name")
	note := cmdFlag.String("note", "", "Marker note")
	cmdFlag.Parse(args)

	if len(args) > 0 && args[0] == "remove" {
		u := url.URL{Scheme: "ws", Host: serverAddr, Path: "/ws"}
		q := u.Query()
		q.Set("username", *username)
		u.RawQuery = q.Encode()
		conn, _, err := dialer.Dial(u.String(), nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: connecting: %v\n", err)
			os.Exit(1)
		}
		defer conn.Close()
		msg := relay.NewMessage(relay.MsgMarkerRemove, relay.MarkerRemove{
			MarkerID: "all",
			UserID:   *username,
		})
		conn.WriteJSON(msg)
		fmt.Println("Markers removed")
		return
	}

	lineStr := cmdFlag.Arg(0)
	if lineStr == "" {
		fmt.Fprintln(os.Stderr, "Usage: relay mark [-username name] [-note text] <line>")
		os.Exit(1)
	}
	var line int
	if _, err := fmt.Sscanf(lineStr, "%d", &line); err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid line number: %s\n", lineStr)
		os.Exit(1)
	}

	u := url.URL{Scheme: "ws", Host: serverAddr, Path: "/ws"}
	q := u.Query()
	q.Set("username", *username)
	u.RawQuery = q.Encode()

	conn, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: connecting: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	msg := relay.NewMessage(relay.MsgMarker, relay.Marker{
		MarkerID:   fmt.Sprintf("%d", line),
		UserID:     *username,
		Username:   *username,
		CursorX:    0,
		CursorY:    line - 1,
		Note:       *note,
		Timestamp:  time.Now().Unix(),
	})
	if err := conn.WriteJSON(msg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: sending: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Marker dropped at line %d\n", line)
}

func runRecord(args []string) {
	fmt.Println("Recording is enabled via the host command:")
	fmt.Println("  relay host --record <output.jsonl>")
	fmt.Println("")
	fmt.Println("The host records all terminal output and collaboration events")
	fmt.Println("to a JSONL file as the session progresses.")
	fmt.Println("")
	fmt.Println("Usage: relay host --record session.jsonl")
}

func runPlayback(args []string) {
	playbackCmd := flag.NewFlagSet("playback", flag.ExitOnError)
	speed := playbackCmd.Float64("speed", 1.0, "Playback speed multiplier (0.25, 0.5, 1, 2, 4, 8)")
	playbackCmd.Parse(args)

	filePath := playbackCmd.Arg(0)
	if filePath == "" {
		fmt.Fprintln(os.Stderr, "Error: recording file required")
		fmt.Fprintln(os.Stderr, "Usage: relay playback [-speed 1.0] <file.jsonl>")
		os.Exit(1)
	}

	player, err := playback.NewPlayer(filePath, *speed)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer player.Close()

	fmt.Printf("Playing: %s\n", filePath)
	fmt.Println("SPACE: pause/resume  +/-: speed  n/p: step  g/G: seek  q: quit")

	if err := player.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

