package relay

import "github.com/google/uuid"

// MessageType is the discriminator field in every JSON message.
type MessageType string

const (
	MsgJoinRoom       MessageType = "join_room"
	MsgLeaveRoom      MessageType = "leave_room"
	MsgRoomJoined     MessageType = "room_joined"
	MsgUserJoined     MessageType = "user_joined"
	MsgUserLeft       MessageType = "user_left"
	MsgTerminalData   MessageType = "terminal_data"
	MsgCursorMove     MessageType = "cursor_move"
	MsgCommandQueue   MessageType = "command_queue"
	MsgCommandApprove MessageType = "command_approve"
	MsgCommandReject  MessageType = "command_reject"
	MsgChatMessage    MessageType = "chat_message"
	MsgMarker         MessageType = "marker"
	MsgMarkerRemove   MessageType = "marker_remove"
	MsgResize         MessageType = "resize"
	MsgError          MessageType = "error"
	MsgPing           MessageType = "ping"
	MsgPong           MessageType = "pong"
)

// Message is the envelope sent over WebSocket.
type Message struct {
	Type    MessageType `json:"type"`
	Payload interface{} `json:"payload,omitempty"`
}

// --- Join / Leave ---

type JoinRoom struct {
	RoomCode  string `json:"room_code"`
	Password  string `json:"password,omitempty"`
	Username  string `json:"username"`
	IsHost    bool   `json:"is_host"`
	TerminalW int    `json:"terminal_w"`
	TerminalH int    `json:"terminal_h"`
}

type RoomJoined struct {
	RoomCode     string     `json:"room_code"`
	HostID       string     `json:"host_id"`
	Users        []UserInfo `json:"users"`
	TerminalW    int        `json:"terminal_w"`
	TerminalH    int        `json:"terminal_h"`
	Passwordured bool       `json:"password_required"`
}

type UserInfo struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	IsHost   bool   `json:"is_host"`
	Color    string `json:"color"`
}

type LeaveRoom struct{}

type UserJoined struct {
	User UserInfo `json:"user"`
}

type UserLeft struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
}

// --- Terminal ---

type TerminalData struct {
	Data string `json:"data"` // base64-encoded raw terminal bytes
	Seq  uint64 `json:"seq"`
}

type Resize struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// --- Cursor ---

type CursorMove struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	X        int    `json:"x"` // column, 0-based
	Y        int    `json:"y"` // row, 0-based
	Color    string `json:"color"`
}

// --- Command Queue ---

type CommandQueue struct {
	CommandID string `json:"command_id"`
	UserID    string `json:"user_id"`
	Username  string `json:"username"`
	Command   string `json:"command"`
	Timestamp int64  `json:"timestamp"`
}

type CommandApprove struct {
	CommandID string `json:"command_id"`
	ByUserID  string `json:"by_user_id"`
}

type CommandReject struct {
	CommandID string `json:"command_id"`
	ByUserID  string `json:"by_user_id"`
	Reason    string `json:"reason,omitempty"`
}

// --- Chat ---

type ChatMessage struct {
	UserID    string `json:"user_id"`
	Username  string `json:"username"`
	Text      string `json:"text"`
	Timestamp int64  `json:"timestamp"`
}

// --- Markers ---

type Marker struct {
	MarkerID  string `json:"marker_id"` // "1", "2", ... or uuid
	UserID    string `json:"user_id"`
	Username  string `json:"username"`
	CursorX   int    `json:"cursor_x"`
	CursorY   int    `json:"cursor_y"`
	Note      string `json:"note,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

type MarkerRemove struct {
	MarkerID string `json:"marker_id"`
	UserID   string `json:"user_id"`
}

// --- Error ---

type ErrMessage struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// --- Helpers ---

func NewMessage(typ MessageType, payload interface{}) *Message {
	return &Message{Type: typ, Payload: payload}
}

func (m *Message) HasPayload() bool {
	return m.Payload != nil
}

// GenerateID returns a short random ID for commands/markers.
func GenerateID() string {
	id := uuid.New()
	return string(id[:8])
}