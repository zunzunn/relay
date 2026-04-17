package playback

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/relay-dev/relay/pkg/cursor"
	relay "github.com/relay-dev/relay/pkg/relay"
	"github.com/relay-dev/relay/pkg/record"
	"github.com/relay-dev/relay/pkg/session"
	"golang.org/x/term"
)

type chatEntry struct {
	Username string
	Text     string
	Time     time.Time
}

type markerEntry struct {
	ID       string
	Username string
	Y        int
	Note     string
	Color    string
}

// Player drives the replay of a recorded session.
type Player struct {
	rr       *record.RecordReader
	vt       *session.VirtualTerminal
	curReg   *cursor.Registry
	chatLog  []chatEntry
	markers  map[string]markerEntry
	cmdQueue []*relay.QueuedCommand
	width    int
	height   int
	sidebarW int
	done     chan struct{}
	mu       sync.Mutex

	state         PlayerState
	speed         float64
	firstEventTS  int64
	lastEventTS   int64
	playStartTS   int64
	accumPause    int64
	pauseStartTS  int64
	ring          []*record.RecordEvent
	ringIdx       int
	ringCount     int
	currentIdx    int
	totalEvents   int

	ptyMaster *os.File
	oldState  *term.State
}

func NewPlayer(filePath string, speed float64) (*Player, error) {
	rr, err := record.NewRecordReader(filePath)
	if err != nil {
		return nil, err
	}

	h := rr.Header()
	if h == nil {
		rr.Close()
		return nil, fmt.Errorf("no header in recording file")
	}

	ptyMaster, _, err := pty.Open()
	if err != nil {
		rr.Close()
		return nil, err
	}

	oldState, err := term.MakeRaw(int(ptyMaster.Fd()))
	if err != nil {
		ptyMaster.Close()
		rr.Close()
		return nil, err
	}

	p := &Player{
		rr:        rr,
		vt:        session.NewVirtualTerminal(h.TerminalW, h.TerminalH),
		curReg:    cursor.NewRegistry(),
		chatLog:   make([]chatEntry, 0),
		markers:   make(map[string]markerEntry),
		cmdQueue:  make([]*relay.QueuedCommand, 0),
		width:     h.TerminalW,
		height:    h.TerminalH,
		sidebarW:  max(1, h.TerminalW*30/100),
		done:      make(chan struct{}),
		speed:     speed,
		ring:      make([]*record.RecordEvent, 100),
	}
	p.ptyMaster = ptyMaster
	p.oldState = oldState

	return p, nil
}

func (p *Player) Run() error {
	controlCh := make(chan PlayerControl, 1)
	go p.handleControls(controlCh)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(16 * time.Millisecond)
	defer ticker.Stop()

	p.playStartTS = time.Now().UnixNano()

	for {
		select {
		case <-p.done:
			return nil
		case c := <-controlCh:
			if !p.handleControl(c) {
				return nil
			}
		case <-ticker.C:
			if p.state == stateEnded || p.state == statePaused {
				p.renderFrame()
				continue
			}
			if err := p.tick(); err != nil {
				if err == record.ErrEOF {
					p.state = stateEnded
					p.renderFrame()
					continue
				}
				return err
			}
			p.renderFrame()
		}
	}
}

func (p *Player) tick() error {
	e, err := p.rr.Peek()
	if err != nil {
		return err
	}

	sleepDur := p.calculateSleep(e.TS)
	if sleepDur > 0 {
		time.Sleep(sleepDur)
	}

	e, err = p.rr.Next()
	if err != nil {
		return err
	}
	p.processEvent(e)
	p.totalEvents++

	if p.firstEventTS == 0 {
		p.firstEventTS = e.TS
	}
	p.lastEventTS = e.TS

	if p.ringCount < len(p.ring) {
		p.ringCount++
	}
	p.ring[p.ringIdx] = e
	p.ringIdx = (p.ringIdx + 1) % len(p.ring)
	p.currentIdx++

	return nil
}

func (p *Player) calculateSleep(eventTS int64) time.Duration {
	if p.firstEventTS == 0 || eventTS <= p.firstEventTS {
		return 0
	}
	wallElapsed := time.Now().UnixNano() - p.playStartTS - p.accumPause
	eventOffset := eventTS - p.firstEventTS
	targetElapsed := int64(float64(eventOffset) / p.speed)
	sleepNanos := targetElapsed - wallElapsed
	if sleepNanos < 0 {
		return 0
	}
	return time.Duration(sleepNanos) * time.Nanosecond
}

func (p *Player) handleControl(c PlayerControl) bool {
	switch c {
	case ctrlQuit:
		close(p.done)
		return false
	case ctrlPause:
		if p.state == statePlaying {
			p.state = statePaused
			p.pauseStartTS = time.Now().UnixNano()
		} else if p.state == statePaused {
			p.accumPause += time.Now().UnixNano() - p.pauseStartTS
			p.state = statePlaying
		} else if p.state == stateEnded {
			p.seekToStart()
			p.state = statePlaying
		}
	case ctrlStepFwd:
		if p.state == statePaused {
			e, err := p.rr.Next()
			if err == nil {
				p.processEvent(e)
				p.totalEvents++
				if p.firstEventTS == 0 {
					p.firstEventTS = e.TS
				}
				p.lastEventTS = e.TS
				if p.ringCount < len(p.ring) {
					p.ringCount++
				}
				p.ring[p.ringIdx] = e
				p.ringIdx = (p.ringIdx + 1) % len(p.ring)
				p.currentIdx++
			} else if err == record.ErrEOF {
				p.state = stateEnded
			}
		}
	case ctrlStepBwd:
		if p.state == statePaused && p.currentIdx > 0 {
			p.currentIdx--
			p.ringIdx = (p.ringIdx - 1 + len(p.ring)) % len(p.ring)
			p.reprocessFrom(0)
		}
	case ctrlSpeedUp:
		if p.speed < 8.0 {
			p.speed *= 2
		}
	case ctrlSpeedDown:
		if p.speed > 0.25 {
			p.speed /= 2
		}
	case ctrlSeekStart:
		p.seekToStart()
		p.state = statePaused
	case ctrlSeekEnd:
		p.seekToEnd()
		p.state = stateEnded
	}
	return true
}

func (p *Player) seekToStart() {
	p.rr.Reset()
	p.vt.Resize(p.width, p.height)
	p.curReg = cursor.NewRegistry()
	p.chatLog = make([]chatEntry, 0)
	p.markers = make(map[string]markerEntry)
	p.cmdQueue = make([]*relay.QueuedCommand, 0)
	p.firstEventTS = 0
	p.lastEventTS = 0
	p.totalEvents = 0
	p.currentIdx = 0
	p.ringIdx = 0
	p.ringCount = 0
	p.ring = make([]*record.RecordEvent, 100)
	p.playStartTS = time.Now().UnixNano()
	p.accumPause = 0
	p.pauseStartTS = 0
}

func (p *Player) seekToEnd() {
	p.rr.Reset()
	for {
		e, err := p.rr.Next()
		if err == record.ErrEOF {
			break
		}
		if err != nil {
			break
		}
		p.processEvent(e)
	}
}

func (p *Player) reprocessFrom(idx int) {
	p.vt.Resize(p.width, p.height)
	p.curReg = cursor.NewRegistry()
	p.chatLog = make([]chatEntry, 0)
	p.markers = make(map[string]markerEntry)
	p.cmdQueue = make([]*relay.QueuedCommand, 0)
	for i := 0; i < idx && i < p.ringCount; i++ {
		pos := (p.ringIdx - p.ringCount + i + len(p.ring)) % len(p.ring)
		if p.ring[pos] != nil {
			p.processEvent(p.ring[pos])
		}
	}
}

func (p *Player) processEvent(e *record.RecordEvent) {
	if e.Msg == nil {
		return
	}
	switch e.Msg.Type {
	case relay.MsgTerminalData:
		if pl := getPayload(e.Msg); pl != nil {
			if data, ok := pl["data"].(string); ok {
				decoded, _ := base64.StdEncoding.DecodeString(data)
				p.vt.WriteRaw(decoded)
			}
		}
	case relay.MsgResize:
		if pl := getPayload(e.Msg); pl != nil {
			w := getInt(pl, "width")
			h := getInt(pl, "height")
			if w > 0 && h > 0 {
				p.mu.Lock()
				p.width = w
				p.height = h
				p.sidebarW = max(1, w*30/100)
				p.mu.Unlock()
				p.vt.Resize(w, h)
			}
		}
	case relay.MsgCursorMove:
		if pl := getPayload(e.Msg); pl != nil {
			cur := &cursor.Cursor{
				UserID:   getStr(pl, "user_id"),
				Username: getStr(pl, "username"),
				Color:    getStr(pl, "color"),
				X:        getInt(pl, "x"),
				Y:        getInt(pl, "y"),
				Visible:  true,
			}
			p.curReg.Update(cur)
		}
	case relay.MsgChatMessage:
		if pl := getPayload(e.Msg); pl != nil {
			p.chatLog = append(p.chatLog, chatEntry{
				Username: getStr(pl, "username"),
				Text:     getStr(pl, "text"),
				Time:     time.Now(),
			})
			if len(p.chatLog) > 100 {
				p.chatLog = p.chatLog[len(p.chatLog)-100:]
			}
		}
	case relay.MsgMarker:
		if pl := getPayload(e.Msg); pl != nil {
			p.markers[getStr(pl, "marker_id")] = markerEntry{
				ID:       getStr(pl, "marker_id"),
				Username: getStr(pl, "username"),
				Y:        getInt(pl, "cursor_y"),
				Note:     getStr(pl, "note"),
				Color:    getStr(pl, "color"),
			}
		}
	case relay.MsgMarkerRemove:
		if pl := getPayload(e.Msg); pl != nil {
			delete(p.markers, getStr(pl, "marker_id"))
		}
	case relay.MsgCommandQueue:
		if pl := getPayload(e.Msg); pl != nil {
			p.cmdQueue = append(p.cmdQueue, &relay.QueuedCommand{
				ID:       getStr(pl, "command_id"),
				UserID:   getStr(pl, "user_id"),
				Username: getStr(pl, "username"),
				Command:  getStr(pl, "command"),
				Status:   "pending",
			})
		}
	case relay.MsgCommandApprove, relay.MsgCommandReject:
		if pl := getPayload(e.Msg); pl != nil {
			cmdID := getStr(pl, "command_id")
			status := "rejected"
			if e.Msg.Type == relay.MsgCommandApprove {
				status = "approved"
			}
			for _, cmd := range p.cmdQueue {
				if cmd.ID == cmdID {
					cmd.Status = status
				}
			}
		}
	}
}

func (p *Player) handleControls(ch chan PlayerControl) {
	buf := make([]byte, 32)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			ch <- ctrlQuit
			return
		}
		c := parseKey(buf[:n])
		if c != ctrlNone {
			select {
			case ch <- c:
			case <-p.done:
				return
			}
		}
	}
}

func (p *Player) renderFrame() {
	p.mu.Lock()
	frame := p.composeFrame()
	p.mu.Unlock()
	p.ptyMaster.Write([]byte(frame))
}

func (p *Player) composeFrame() string {
	termW := p.width
	sidebarW := p.sidebarW
	termAreaW := termW - sidebarW - 1

	output := p.vt.Render()
	lines := stringsSplit(output, "\r\n")

	// Cursor overlays
	var overlayLines []string
	for y := 0; y < len(lines) && y < p.height; y++ {
		overlayLines = append(overlayLines, "")
	}
	for _, cur := range p.curReg.All() {
		if cur.X >= 0 && cur.X < termAreaW && cur.Y >= 0 && cur.Y < p.height {
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

	sidebar := p.buildSidebar()
	sidebarLines := stringsSplit(sidebar, "\r\n")

	// Status bar
	status := statusBar(p.state, p.speed, p.currentIdx, p.totalEvents, p.lastEventTS, p.firstEventTS, p.lastEventTS)

	var out strings.Builder
	divider := "\x1b[38;5;240m│\x1b[0m"

	for y := 0; y < p.height; y++ {
		if y < len(lines) {
			line := lines[y]
			if len(line) > termAreaW {
				line = line[:termAreaW]
			}
			out.WriteString(line)
			visible := stripANSI(line)
			for len(visible) < termAreaW {
				out.WriteByte(' ')
				visible += " "
			}
		} else {
			for x := 0; x < termAreaW; x++ {
				out.WriteByte(' ')
			}
		}
		out.WriteString(divider)
		if y < len(sidebarLines) {
			sl := sidebarLines[y]
			if len(sl) > sidebarW {
				sl = sl[:sidebarW]
			}
			out.WriteString(sl)
			spadding := stripANSI(sl)
			for len(spadding) < sidebarW {
				out.WriteByte(' ')
				spadding += " "
			}
		} else {
			for x := 0; x < sidebarW; x++ {
				out.WriteByte(' ')
			}
		}
		out.WriteString("\r\n")
	}

	// Status bar at bottom
	out.WriteString("\x1b[38;5;250m")
	for x := 0; x < termW; x++ {
		out.WriteByte('-')
	}
	out.WriteString("\x1b[0m")
	out.WriteString(status)
	out.WriteString("\r\n")

	// Cursor overlays
	for y := 0; y < len(overlayLines); y++ {
		if overlayLines[y] != "" {
			out.WriteString(overlayLines[y])
		}
	}

	return out.String()
}

func (p *Player) buildSidebar() string {
	var sb strings.Builder
	sb.WriteString("\x1b[38;5;250m\x1b[48;5;234m")
	sep := strings.Repeat("─", p.sidebarW)
	sb.WriteString(sep + "\r\n")

	title := "  PLAYBACK"
	if len(title) > p.sidebarW {
		title = title[:p.sidebarW]
	}
	sb.WriteString("\x1b[1m\x1b[38;5;229m" + title + "\r\n")
	sb.WriteString(sep + "\r\n")

	// Markers
	sb.WriteString("\x1b[38;5;215m  Markers\r\n")
	if len(p.markers) == 0 {
		sb.WriteString("\x1b[38;5;240m  (none)\r\n")
	} else {
		for _, m := range p.markers {
			note := m.Note
			if note == "" {
				note = fmt.Sprintf("line %d", m.Y+1)
			}
			if len(note) > p.sidebarW-4 {
				note = note[:p.sidebarW-4]
			}
			sb.WriteString(fmt.Sprintf("  %s\r\n", note))
		}
	}
	sb.WriteString(sep + "\r\n")

	// Chat
	sb.WriteString("\x1b[38;5;86m  Chat\r\n")
	chatCount := 0
	for i := len(p.chatLog) - 1; i >= 0 && chatCount < 5; i-- {
		entry := p.chatLog[i]
		chatCount++
		msg := fmt.Sprintf("  <%s> %s", entry.Username, entry.Text)
		if len(msg) > p.sidebarW {
			msg = msg[:p.sidebarW]
		}
		sb.WriteString(msg + "\r\n")
	}
	if chatCount == 0 {
		sb.WriteString("\x1b[38;5;240m  (no messages)\r\n")
	}
	sb.WriteString(sep + "\r\n")

	// Command queue
	sb.WriteString("\x1b[38;5;228m  Commands\r\n")
	hasPending := false
	for _, cmd := range p.cmdQueue {
		if cmd.Status == "pending" {
			hasPending = true
		}
	}
	if hasPending {
		for _, cmd := range p.cmdQueue {
			if cmd.Status == "pending" {
				cmdtxt := cmd.Command
				if len(cmdtxt) > p.sidebarW-4 {
					cmdtxt = cmdtxt[:p.sidebarW-4]
				}
				sb.WriteString(fmt.Sprintf("\x1b[38;5;214m  %s\r\n", cmdtxt))
			}
		}
	} else {
		sb.WriteString("\x1b[38;5;240m  (empty)\r\n")
	}
	sb.WriteString(sep + "\r\n")

	// Users
	sb.WriteString("\x1b[38;5;147m  Users\r\n")
	for _, cur := range p.curReg.All() {
		name := "  " + cur.Username
		if len(name) > p.sidebarW {
			name = name[:p.sidebarW]
		}
		r, g, b := hexToRGB(cur.Color)
		sb.WriteString(fmt.Sprintf("\x1b[38;2;%d;%d;%dm%s\x1b[0m\r\n", r, g, b, name))
	}
	sb.WriteString(sep + "\r\n")

	sb.WriteString("\x1b[0m")
	return sb.String()
}

func (p *Player) Close() error {
	close(p.done)
	if p.oldState != nil && p.ptyMaster != nil {
		term.Restore(int(p.ptyMaster.Fd()), p.oldState)
	}
	if p.ptyMaster != nil {
		p.ptyMaster.Close()
	}
	if p.rr != nil {
		p.rr.Close()
	}
	return nil
}

// --- helpers (duplicated from cmd/relay/main.go) ---

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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func stringsSplit(s, sep string) []string {
	if sep == "\r\n" {
		var result []string
		start := 0
		for i := 0; i < len(s); i++ {
			if i+1 < len(s) && s[i] == '\r' && s[i+1] == '\n' {
				result = append(result, s[start:i])
				start = i + 2
				i++
			}
		}
		if start < len(s) {
			result = append(result, s[start:])
		}
		return result
	}
	return strings.Split(s, sep)
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
