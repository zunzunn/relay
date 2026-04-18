package join

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/relay-dev/relay/pkg/protocol"
	"golang.org/x/term"
)

// Client connects to a relay room and renders the split view.
type Client struct {
	serverAddr string
	roomCode   string
	username   string
	password   string
	conn       *websocket.Conn
	renderer   *Renderer
	done       chan struct{}
	mu         sync.Mutex
}

// NewClient creates a join client.
func NewClient(serverAddr, roomCode, username, password string) *Client {
	return &Client{
		serverAddr: serverAddr,
		roomCode:   roomCode,
		username:   username,
		password:   password,
		done:       make(chan struct{}),
	}
}

// Run connects, receives messages, and renders until interrupted.
func (c *Client) Run() error {
	cols, rows, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		cols, rows = 80, 24
	}
	c.renderer = NewRenderer(cols, rows)

	u := url.URL{Scheme: "ws", Host: c.serverAddr, Path: "/ws"}
	q := u.Query()
	q.Set("username", c.username)
	u.RawQuery = q.Encode()

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	c.conn = conn

	joinMsg, _ := protocol.EncodeMessage(protocol.TypeJoinRoom, protocol.JoinRoomPayload{
		RoomCode: c.roomCode,
		Password: c.password,
		Username: c.username,
		IsHost:   false,
	})
	if err := conn.WriteJSON(joinMsg); err != nil {
		return fmt.Errorf("send join: %w", err)
	}

	var joined protocol.Message
	if err := conn.ReadJSON(&joined); err != nil {
		return fmt.Errorf("read room_joined: %w", err)
	}
	c.renderer.AddSidebar("Connected to %s", c.roomCode)
	c.renderer.SetRoomCode(c.roomCode)
	c.renderer.AddSidebar("Joined at %s", time.Now().Format("15:04"))

	// Start all loops
	go c.readLoop()
	go c.renderLoop()
	go c.watchResize()
	go c.inputLoop()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	close(c.done)
	conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	conn.Close()
	return nil
}

func (c *Client) readLoop() {
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			close(c.done)
			return
		}
		msg, err := protocol.DecodeMessage(data)
		if err != nil {
			continue
		}
		c.handle(msg)
	}
}

func (c *Client) handle(msg *protocol.Message) {
	switch msg.Type {
	case protocol.TypeTerminalData:
		var td protocol.TerminalDataPayload
		json.Unmarshal(msg.Payload, &td)
		c.renderer.AddTerminal(td.Data)

	case protocol.TypeChat:
		var chat protocol.ChatPayload
		json.Unmarshal(msg.Payload, &chat)
		c.renderer.AddSidebar("[%s] %s: %s",
			formatTimestamp(chat.Timestamp), chat.Username, chat.Text)

	case protocol.TypeUserJoined:
		var joined protocol.UserJoinedPayload
		json.Unmarshal(msg.Payload, &joined)
		c.renderer.AddSidebar("-> %s joined", joined.User.Username)

	case protocol.TypeUserLeft:
		var left protocol.UserLeftPayload
		json.Unmarshal(msg.Payload, &left)
		c.renderer.AddSidebar("<- %s left", left.Username)

	case protocol.TypeCommandRequest:
		var req protocol.CommandRequestPayload
		json.Unmarshal(msg.Payload, &req)
		c.renderer.AddSidebar("[%s] queued: %s", req.Username, req.Command)

	case protocol.TypeCommandApprove:
		var approve protocol.CommandApprovePayload
		json.Unmarshal(msg.Payload, &approve)
		c.renderer.AddSidebar("approved: %s", approve.CommandID)

	case protocol.TypeCommandReject:
		var reject protocol.CommandRejectPayload
		json.Unmarshal(msg.Payload, &reject)
		c.renderer.AddSidebar("rejected: %s", reject.CommandID)

	case protocol.TypeMarker:
		var marker protocol.MarkerPayload
		json.Unmarshal(msg.Payload, &marker)
		c.renderer.AddSidebar("marker %s @ L%d: %s",
			marker.Username, marker.CursorY+1, marker.Note)

	case protocol.TypeResize:
		var resize protocol.ResizePayload
		json.Unmarshal(msg.Payload, &resize)
		c.renderer.Resize(resize.Width, resize.Height)

	case protocol.TypePong:
		// ignore
	}
}

func (c *Client) renderLoop() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			c.renderer.Render()
		}
	}
}

func (c *Client) watchResize() {
	prevCols, prevRows := c.renderer.width, c.renderer.height
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			cols, rows, err := term.GetSize(int(os.Stdout.Fd()))
			if err != nil {
				continue
			}
			if cols != prevCols || rows != prevRows {
				prevCols, prevRows = cols, rows
				c.renderer.Resize(cols, rows)
			}
		}
	}
}

func (c *Client) inputLoop() {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		select {
		case <-c.done:
			return
		default:
		}
		if !scanner.Scan() {
			return
		}
		text := scanner.Text()
		c.processInput(text)
	}
}

func (c *Client) processInput(text string) {
	if strings.HasPrefix(text, "/chat ") {
		msg := strings.TrimPrefix(text, "/chat ")
		c.sendChat(msg)
		c.renderer.AddSidebar("[you]: %s", msg)
		return
	}
	if strings.HasPrefix(text, "/cmd ") {
		cmd := strings.TrimPrefix(text, "/cmd ")
		c.sendCommand(cmd)
		c.renderer.AddSidebar("[you] queued: %s", cmd)
		return
	}
	// Default: treat as chat
	c.sendChat(text)
	c.renderer.AddSidebar("[you]: %s", text)
}

func (c *Client) sendChat(text string) {
	msg, err := protocol.EncodeMessage(protocol.TypeChat, protocol.ChatPayload{
		UserID:    c.username,
		Username:  c.username,
		Text:      text,
		Timestamp: time.Now().Unix(),
	})
	if err != nil {
		return
	}
	c.conn.WriteJSON(msg)
}

func (c *Client) sendCommand(cmd string) {
	msg, err := protocol.EncodeMessage(protocol.TypeCommandRequest, protocol.CommandRequestPayload{
		CommandID: fmt.Sprintf("%d", time.Now().UnixNano()),
		UserID:    c.username,
		Username:  c.username,
		Command:   cmd,
		Timestamp: time.Now().Unix(),
	})
	if err != nil {
		return
	}
	c.conn.WriteJSON(msg)
}