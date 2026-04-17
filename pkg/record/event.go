package record

import (
	"encoding/json"
	"os"
	"time"

	"github.com/relay-dev/relay/pkg/relay"
)

// RecordEvent is one line in a JSONL recording file.
type RecordEvent struct {
	TS   int64          `json:"ts"`   // Unix nanoseconds; 0 = header
	Type string         `json:"type"` // mirrors relay.Message.Type; "file_header" for header
	Msg  *relay.Message `json:"msg"`  // full message; nil for header
}

// FileHeader is the metadata written as the first JSONL line.
type FileHeader struct {
	Version    int    `json:"version"`
	TerminalW  int    `json:"terminal_w"`
	TerminalH  int    `json:"terminal_h"`
	RecordedAt string `json:"recorded_at"` // RFC3339
	Hostname   string `json:"hostname"`
}

// IsHeader returns true if e is the file header (ts == 0).
func IsHeader(e *RecordEvent) bool {
	return e.TS == 0
}

// NewRecordEvent wraps a relay message with a timestamp.
func NewRecordEvent(msg *relay.Message) *RecordEvent {
	return &RecordEvent{
		TS:   time.Now().UnixNano(),
		Type: string(msg.Type),
		Msg:  msg,
	}
}

// MarshalLine serializes e as a JSONL line (with trailing newline).
func (e *RecordEvent) MarshalLine() ([]byte, error) {
	data, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// NewFileHeaderEvent creates the header record for a new recording.
func NewFileHeaderEvent(terminalW, terminalH int) (*RecordEvent, error) {
	h := FileHeader{
		Version:    1,
		TerminalW: terminalW,
		TerminalH: terminalH,
		RecordedAt: time.Now().Format(time.RFC3339),
		Hostname:   hostname(),
	}
	// Embed the header as a JSON object in Msg for uniform line format
	hData, err := json.Marshal(h)
	if err != nil {
		return nil, err
	}
	return &RecordEvent{
		TS:   0,
		Type: "file_header",
		Msg:  &relay.Message{Type: "file_header", Payload: json.RawMessage(hData)},
	}, nil
}

func hostname() string {
	h, _ := os.Hostname()
	return h
}
