package protocol

import "encoding/json"

// MessageType is a string discriminator for every protocol message.
type MessageType string

// --- Core message types ---

const (
	TypeTerminalData    MessageType = "terminal_data"
	TypeChat            MessageType = "chat"
	TypeCommandRequest  MessageType = "command_request"
	TypeCommandApprove  MessageType = "command_approve"
	TypeCommandReject   MessageType = "command_reject"
	TypeJoinRoom        MessageType = "join_room"
	TypeCursor          MessageType = "cursor"
	TypeMarker          MessageType = "marker"
	TypeResize          MessageType = "resize"
	TypePing            MessageType = "ping"
	TypePong            MessageType = "pong"
	TypeRoomJoined      MessageType = "room_joined"
	TypeUserJoined      MessageType = "user_joined"
	TypeUserLeft        MessageType = "user_left"
	TypeError           MessageType = "error"
)

// Message is the wire-format envelope.
type Message struct {
	Type    MessageType   `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// EncodeMessage serializes a typed payload into a wire Message.
func EncodeMessage(typ MessageType, payload interface{}) (*Message, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &Message{Type: typ, Payload: raw}, nil
}

// DecodeMessage deserializes a wire Message into a typed payload.
// It returns the Message and the raw payload bytes.
func DecodeMessage(data []byte) (*Message, error) {
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// DecodePayload extracts a typed payload from a decoded Message.
func DecodePayload[T any](msg *Message) (T, error) {
	var p T
	if msg.Payload == nil {
		return p, nil
	}
	err := json.Unmarshal(msg.Payload, &p)
	return p, err
}

// --- Typed payloads ---

type JoinRoomPayload struct {
	RoomCode string `json:"room_code"`
	Password string `json:"password,omitempty"`
	Username string `json:"username"`
	IsHost   bool   `json:"is_host"`
}

type RoomJoinedPayload struct {
	RoomCode string     `json:"room_code"`
	HostID   string     `json:"host_id"`
	Users    []UserInfo `json:"users"`
}

type UserInfo struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	IsHost   bool   `json:"is_host"`
	Color    string `json:"color"`
}

type UserJoinedPayload struct {
	User UserInfo `json:"user"`
}

type UserLeftPayload struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
}

type TerminalDataPayload struct {
	Data string `json:"data"` // base64-encoded terminal bytes
	Seq  uint64 `json:"seq"`
}

type ResizePayload struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type CursorPayload struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	X        int    `json:"x"`
	Y        int    `json:"y"`
	Color    string `json:"color"`
}

type ChatPayload struct {
	UserID    string `json:"user_id"`
	Username  string `json:"username"`
	Text      string `json:"text"`
	Timestamp int64  `json:"timestamp"`
}

type CommandRequestPayload struct {
	CommandID string `json:"command_id"`
	UserID    string `json:"user_id"`
	Username  string `json:"username"`
	Command   string `json:"command"`
	Timestamp int64  `json:"timestamp"`
}

type CommandApprovePayload struct {
	CommandID string `json:"command_id"`
	ByUserID  string `json:"by_user_id"`
}

type CommandRejectPayload struct {
	CommandID string `json:"command_id"`
	ByUserID  string `json:"by_user_id"`
	Reason    string `json:"reason,omitempty"`
}

type MarkerPayload struct {
	MarkerID  string `json:"marker_id"`
	UserID    string `json:"user_id"`
	Username  string `json:"username"`
	CursorX   int    `json:"cursor_x"`
	CursorY   int    `json:"cursor_y"`
	Note      string `json:"note,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

type MarkerRemovePayload struct {
	MarkerID string `json:"marker_id"`
	UserID   string `json:"user_id"`
}

type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
