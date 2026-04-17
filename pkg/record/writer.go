package record

import (
	"bufio"
	"os"
	"sync"

	"github.com/relay-dev/relay/pkg/relay"
)

// RecordWriter writes relay messages to a JSONL file.
type RecordWriter struct {
	file   *os.File
	buf    *bufio.Writer
	mu     sync.Mutex
	closed bool
}

// NewRecordWriter opens (or creates) a JSONL recording file and writes the header.
func NewRecordWriter(path string, terminalW, terminalH int) (*RecordWriter, error) {
	file, err := os.Create(path)
	if err != nil {
		return nil, err
	}

	rw := &RecordWriter{
		file: file,
		buf:  bufio.NewWriter(file),
	}

	// Write header synchronously
	header, err := NewFileHeaderEvent(terminalW, terminalH)
	if err != nil {
		file.Close()
		return nil, err
	}
	line, err := header.MarshalLine()
	if err != nil {
		file.Close()
		return nil, err
	}
	if _, err := rw.buf.Write(line); err != nil {
		file.Close()
		return nil, err
	}
	if err := rw.buf.Flush(); err != nil {
		file.Close()
		return nil, err
	}

	return rw, nil
}

// Write records a relay message to the JSONL file.
func (rw *RecordWriter) Write(msg *relay.Message) error {
	rw.mu.Lock()
	defer rw.mu.Unlock()

	if rw.closed {
		return nil
	}

	event := NewRecordEvent(msg)
	line, err := event.MarshalLine()
	if err != nil {
		return err
	}
	_, err = rw.buf.Write(line)
	return err
}

// Close flushes and closes the recording file.
func (rw *RecordWriter) Close() error {
	rw.mu.Lock()
	defer rw.mu.Unlock()

	if rw.closed {
		return nil
	}
	rw.closed = true

	if err := rw.buf.Flush(); err != nil {
		rw.file.Close()
		return err
	}
	return rw.file.Close()
}
