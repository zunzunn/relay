package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/relay-dev/relay/pkg/playback"
	"github.com/relay-dev/relay/pkg/relay"
	"github.com/relay-dev/relay/pkg/host"
	"github.com/relay-dev/relay/pkg/server"
	"github.com/relay-dev/relay/pkg/session"
	cursorpkg "github.com/relay-dev/relay/pkg/cursor"
	"golang.org/x/term"
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

func getInt(m map[string]interface{}, key string) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
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
			fmt.Println(string(data))
		}
	}()

	<-done
	conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	conn.Close()
}

	// WebSocket → VirtualTerminal
	go sess.readWS(conn)

	// Render loop
	go sess.renderLoop(ptyMaster)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	sess.cleanup()
}

func (s *joinSession) readPTY(conn *websocket.Conn, ptyMaster *os.File) {
	buf := make([]byte, 4096)
	for {
		n, err := ptyMaster.Read(buf)
		if err != nil || n == 0 {
			return
		}
		data := buf[:n]
		// Check for mouse escape sequence: ESC [ M <action> <x+1> <y+1>
		if len(data) >= 6 && data[0] == 0x1b && data[1] == '[' && data[2] == 'M' {
			action := int(data[3])
			x := int(data[4]) - 1
			y := int(data[5]) - 1
			if action == 0x20 || action == 0x22 { // button press or release
				msg := relay.NewMessage(relay.MsgCursorMove, relay.CursorMove{
					UserID:   s.userID,
					Username: s.username,
					X:        x,
					Y:        y,
					Color:    userColor(s.userID),
				})
				conn.WriteJSON(msg)
			}
		}
	}
}

func (s *joinSession) readWS(conn *websocket.Conn) {
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var msg relay.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		s.handleMessage(&msg)
	}
}

func (s *joinSession) renderLoop(ptyMaster *os.File) {
	ticker := time.NewTicker(16 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			s.mu.Lock()
			output := s.composeSplitView()
			s.mu.Unlock()
			ptyMaster.Write([]byte(output))
		}
	}
}

func (s *joinSession) watchResize(ptyMaster *os.File) {
	prevCols, prevRows := s.width, s.height
	for {
		select {
		case <-s.done:
			return
		case <-time.After(200 * time.Millisecond):
		}
		cols, rows, err := term.GetSize(int(ptyMaster.Fd()))
		if err != nil {
			continue
		}
		if cols != prevCols || rows != prevRows {
			prevCols, prevRows = cols, rows
			pty.Setsize(ptyMaster, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
			s.mu.Lock()
			s.width = cols
			s.height = rows
			s.sidebarW = max(1, cols*30/100)
			s.vt.Resize(cols, rows)
			conn := s.conn
			s.mu.Unlock()
			if conn != nil {
				msg := relay.NewMessage(relay.MsgResize, relay.Resize{Width: cols, Height: rows})
				conn.WriteJSON(msg)
			}
		}
	}
}

func (s *joinSession) composeSplitView() string {
	termW := s.termWidth
	sidebarW := s.sidebarW
	termAreaW := termW - sidebarW - 1 // minus 1 for divider

	// Terminal output with cursor overlays
	termOutput := s.vt.Render()
	lines := strings.Split(termOutput, "\r\n")

	// Remote cursor overlays
	var overlayLines []string
	for y := 0; y < len(lines) && y < s.height; y++ {
		overlayLines = append(overlayLines, "")
	}
	for _, cur := range s.cursorReg.All() {
		if cur.X >= 0 && cur.X < termAreaW && cur.Y >= 0 && cur.Y < s.height {
			r, g, b := hexToRGB(cur.Color)
			fgR, fgG, fgB := contrastingColor(r, g, b)
			label := fmt.Sprintf("[%s]", cur.Username)
			badge := fmt.Sprintf(
				"\x1b[%d;%dH\x1b[48;2;%d;%d;%dm\x1b[38;2;%d;%d;%dm%s\x1b[0m",
				cur.Y+1, max(1, cur.X+1),
				r, g, b,
				fgR, fgG, fgB,
				label,
			)
			if cur.Y < len(overlayLines) {
				overlayLines[cur.Y] += badge
			}
		}
	}

	sidebar := s.buildSidebar()
	sidebarLines := strings.Split(sidebar, "\r\n")

	var out strings.Builder
	divider := "\x1b[38;5;240m│\x1b[0m"
	for y := 0; y < s.height; y++ {
		// Terminal line (up to termAreaW columns)
		if y < len(lines) {
			line := lines[y]
			if len(line) > termAreaW {
				line = line[:termAreaW]
			}
			out.WriteString(line)
			// Pad to divider
			visible := stripANSI(line)
			padding := termAreaW - len(visible)
			if padding > 0 {
				out.WriteString(strings.Repeat(" ", padding))
			}
		} else {
			out.WriteString(strings.Repeat(" ", termAreaW))
		}
		// Divider
		out.WriteString(divider)
		// Sidebar line
		if y < len(sidebarLines) {
			sl := sidebarLines[y]
			if len(sl) > sidebarW {
				sl = sl[:sidebarW]
			}
			out.WriteString(sl)
			// Pad sidebar
			spadding := stripANSI(sl)
			if len(spadding) < sidebarW {
				out.WriteString(strings.Repeat(" ", sidebarW-len(spadding)))
			}
		} else {
			out.WriteString(strings.Repeat(" ", sidebarW))
		}
		out.WriteString("\r\n")
		// Apply cursor overlay for this line
		if y < len(overlayLines) && overlayLines[y] != "" {
			out.WriteString(overlayLines[y])
		}
	}
	return out.String()
}

func (s *joinSession) buildSidebar() string {
	var sb strings.Builder
	sb.WriteString("\x1b[38;5;250m\x1b[48;5;234m")
	sep := strings.Repeat("─", s.sidebarW)
	sb.WriteString(sep + "\r\n")

	// Room info
	title := "  RELAY SESSION"
	if len(title) > s.sidebarW {
		title = title[:s.sidebarW]
	}
	sb.WriteString("\x1b[1m\x1b[38;5;229m" + title + "\r\n")
	sb.WriteString(sep + "\r\n")

	// Markers section
	sb.WriteString("\x1b[38;5;215m  Markers\r\n")
	if len(s.markers) == 0 {
		sb.WriteString("\x1b[38;5;240m  (none)\r\n")
	} else {
		for _, m := range s.markers {
			note := m.Note
			if note == "" {
				note = "line " + fmt.Sprintf("%d", m.Y+1)
			}
			if len(note) > s.sidebarW-4 {
				note = note[:s.sidebarW-4]
			}
			sb.WriteString(fmt.Sprintf("\x1b[38;5;%s[m  %s\r\n", m.Color, note))
		}
	}
	sb.WriteString(sep + "\r\n")

	// Chat section
	sb.WriteString("\x1b[38;5;86m  Chat\r\n")
	chatCount := 0
	s.mu.Lock()
	for i := len(s.chatLog) - 1; i >= 0 && chatCount < 5; i-- {
		entry := s.chatLog[i]
		chatCount++
		msg := fmt.Sprintf("  <%s> %s", entry.Username, entry.Text)
		if len(msg) > s.sidebarW {
			msg = msg[:s.sidebarW]
		}
		sb.WriteString(msg + "\r\n")
	}
	s.mu.Unlock()
	if chatCount == 0 {
		sb.WriteString("\x1b[38;5;240m  (no messages)\r\n")
	}
	sb.WriteString(sep + "\r\n")

	// Command queue section
	sb.WriteString("\x1b[38;5;228m  Command Queue\r\n")
	s.mu.Lock()
	hasPending := false
	for _, cmd := range s.cmdQueue {
		if cmd.Status == "pending" {
			hasPending = true
		}
	}
	if hasPending {
		for _, cmd := range s.cmdQueue {
			if cmd.Status == "pending" {
				cmdtxt := cmd.Command
				if len(cmdtxt) > s.sidebarW-4 {
					cmdtxt = cmdtxt[:s.sidebarW-4]
				}
				statusStr := "\x1b[38;5;214m  " + cmdtxt + "\r\n"
				sb.WriteString(statusStr)
			}
		}
	} else {
		sb.WriteString("\x1b[38;5;240m  (empty)\r\n")
	}
	s.mu.Unlock()
	sb.WriteString(sep + "\r\n")

	// Connected users
	sb.WriteString("\x1b[38;5;147m  Users\r\n")
	for _, cur := range s.cursorReg.All() {
		name := "  " + cur.Username
		if len(name) > s.sidebarW {
			name = name[:s.sidebarW]
		}
		r, g, b := hexToRGB(cur.Color)
		sb.WriteString(fmt.Sprintf("\x1b[38;2;%d;%d;%dm%s\x1b[0m\r\n", r, g, b, name))
	}
	sb.WriteString(sep + "\r\n")

	sb.WriteString("\x1b[0m")
	return sb.String()
}

func stripANSI(s string) string {
	var result strings.Builder
	inEscape := false
	for _, r := range s {
		if r == '\x1b' {
			inEscape = true
		} else if inEscape && r == 'm' {
			inEscape = false
		} else if !inEscape {
			result.WriteRune(r)
		}
	}
	return result.String()
}

func (s *joinSession) cleanup() {
	close(s.done)
	if s.ptyFile != nil && s.oldState != nil {
		term.Restore(int(s.ptyFile.Fd()), s.oldState)
	}
	if s.conn != nil {
		s.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		s.conn.Close()
	}
}

func (s *joinSession) sendChat(text string) error {
	s.mu.Lock()
	s.chatLog = append(s.chatLog, chatEntry{
		Username: s.username,
		Text:     text,
		Time:     time.Now(),
	})
	s.mu.Unlock()
	msg := relay.NewMessage(relay.MsgChatMessage, relay.ChatMessage{
		UserID:   s.userID,
		Username: s.username,
		Text:     text,
		Timestamp: time.Now().Unix(),
	})
	return s.conn.WriteJSON(msg)
}

func (s *joinSession) sendMarker(line int, note string) error {
	markerID := fmt.Sprintf("%d", line)
	msg := relay.NewMessage(relay.MsgMarker, relay.Marker{
		MarkerID:  markerID,
		UserID:    s.userID,
		Username:  s.username,
		CursorX:   0,
		CursorY:   line - 1,
		Note:      note,
		Timestamp: time.Now().Unix(),
	})
	return s.conn.WriteJSON(msg)
}

func (s *joinSession) removeMarker(markerID string) error {
	msg := relay.NewMessage(relay.MsgMarkerRemove, relay.MarkerRemove{
		MarkerID: markerID,
		UserID:   s.userID,
	})
	return s.conn.WriteJSON(msg)
}

func (s *joinSession) sendCommand(cmd string) error {
	cmdID := fmt.Sprintf("%d", time.Now().UnixNano())
	msg := relay.NewMessage(relay.MsgCommandQueue, relay.CommandQueue{
		CommandID: cmdID,
		UserID:     s.userID,
		Username:   s.username,
		Command:    cmd,
		Timestamp:  time.Now().Unix(),
	})
	return s.conn.WriteJSON(msg)
}

// userColor returns a deterministic color for a user ID.
func userColor(userID string) string {
	colors := []string{
		"#FF6B6B", "#4ECDC4", "#45B7D1", "#96CEB4",
		"#FFEAA7", "#DDA0DD", "#98D8C8", "#F7DC6F",
		"#BB8FCE", "#85C1E9", "#F8B500", "#00CED1",
	}
	hash := 0
	for _, c := range userID {
		hash = hash*31 + int(c)
	}
	return colors[((hash % len(colors)) + len(colors))%len(colors)]
}

func hexToRGB(hex string) (r, g, b int) {
	hex = strings.TrimPrefix(hex, "#")
	fmt.Sscanf(hex, "%02x%02x%02x", &r, &g, &b)
	return
}

func contrastingColor(r, g, b int) (int, int, int) {
	lum := 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
	if lum > 128 {
		return 0, 0, 0
	}
	return 255, 255, 255
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
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
		MarkerID:  fmt.Sprintf("%d", line),
		UserID:    *username,
		Username:  *username,
		CursorX:   0,
		CursorY:   line - 1,
		Note:      *note,
		Timestamp: time.Now().Unix(),
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

func sendMessage(conn *websocket.Conn, msg *relay.Message) error {
	return conn.WriteJSON(msg)
}
