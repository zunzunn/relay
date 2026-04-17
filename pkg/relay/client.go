package relay

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 1024 * 1024 * 2 // 2MB
	sendBufSize    = 256
)

// WSClient is a WebSocket client wrapper used by both host and joiners.
type WSClient struct {
	ID     string
	User   *UserInfo
	Conn   *websocket.Conn
	Send   chan []byte
	closed chan struct{}
	mu     sync.Mutex
}

func NewWSClient(id string, conn *websocket.Conn) *WSClient {
	return &WSClient{
		ID:     id,
		Conn:   conn,
		Send:   make(chan []byte, sendBufSize),
		closed: make(chan struct{}),
	}
}

// ReadPump runs in its own goroutine. It reads JSON messages and dispatches
// them through the returned channel. The caller closes the returned channel
// to signal that no more messages are expected.
func (c *WSClient) ReadPump(handler func(msg *Message) error) error {
	c.Conn.SetReadLimit(maxMessageSize)
	c.Conn.SetReadDeadline(time.Now().Add(pongWait))
	c.Conn.SetPongHandler(func(string) error {
		c.Conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})
	for {
		_, data, err := c.Conn.ReadMessage()
		if err != nil {
			close(c.closed)
			return err
		}
		var msg Message
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if err := handler(&msg); err != nil {
			// Handler can signal to stop reading
			break
		}
	}
	close(c.closed)
	return nil
}

// WritePump runs in its own goroutine. It writes messages from the Send channel
// to the WebSocket connection.
func (c *WSClient) WritePump() error {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()
	for {
		select {
		case msg, ok := <-c.Send:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return nil
			}
			if err := c.Conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return err
			}
		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return err
			}
		case <-c.closed:
			return nil
		}
	}
}

// SendJSON marshals v as JSON and sends it through the Send channel.
func (c *WSClient) SendJSON(v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	select {
	case c.Send <- data:
		return nil
	case <-c.closed:
		return ErrClientClosed
	}
}

var ErrClientClosed = &clientClosedError{}

type clientClosedError struct{}

func (e *clientClosedError) Error() string { return "client closed" }

// Close gracefully shuts down the connection.
func (c *WSClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	c.Conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	c.Conn.Close()
}