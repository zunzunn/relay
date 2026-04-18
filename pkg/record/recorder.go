package record

import (
	"encoding/json"
	"sync"

	"github.com/relay-dev/relay/pkg/relay"
)

// Recorder wraps RecordWriter with a simpler byte-slice interface.
type Recorder struct {
	w *RecordWriter
	m sync.Mutex
}

// NewRecorder opens (or creates) a recording file and writes the header.
func NewRecorder(path string, terminalW, terminalH int) (*Recorder, error) {
	w, err := NewRecordWriter(path, terminalW, terminalH)
	if err != nil {
		return nil, err
	}
	return &Recorder{w: w}, nil
}

// Record writes a relay message to the recording file.
func (r *Recorder) Record(msg []byte) error {
	r.m.Lock()
	defer r.m.Unlock()

	var m relay.Message
	if err := json.Unmarshal(msg, &m); err != nil {
		return err
	}
	return r.w.Write(&m)
}

// Close flushes and closes the recording file.
func (r *Recorder) Close() error {
	return r.w.Close()
}
