package record

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
)

var ErrEOF = errors.New("end of file")
var errNotHeader = errors.New("first line is not a file header")

// RecordReader reads events from a JSONL recording file.
type RecordReader struct {
	file    *os.File
	scanner *bufio.Scanner
	peeked  *RecordEvent // one-event lookahead buffer
	header  *FileHeader
	lineNum int
}

// NewRecordReader opens a recording file and reads the header.
func NewRecordReader(path string) (*RecordReader, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	rr := &RecordReader{
		file:    file,
		scanner: bufio.NewScanner(file),
		lineNum: 0,
	}

	// Increase scanner buffer for long lines (e.g. large terminal data payloads)
	rr.scanner.Buffer(make([]byte, 64), 1024*1024)

	// Read and validate header
	if err := rr.readHeader(); err != nil {
		file.Close()
		return nil, err
	}

	return rr, nil
}

// Header returns the recording file header.
func (rr *RecordReader) Header() *FileHeader {
	return rr.header
}

// Next returns the next event. Returns errEOF when done.
func (rr *RecordReader) Next() (*RecordEvent, error) {
	// Return peeked event if any
	if rr.peeked != nil {
		e := rr.peeked
		rr.peeked = nil
		return e, nil
	}

	if !rr.scanner.Scan() {
		if err := rr.scanner.Err(); err != nil {
			return nil, err
		}
		return nil, ErrEOF
	}
	rr.lineNum++

	line := rr.scanner.Bytes()
	var e RecordEvent
	if err := json.Unmarshal(line, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

// Peek returns the next event without advancing. Subsequent Peek/Next calls
// return the same event.
func (rr *RecordReader) Peek() (*RecordEvent, error) {
	if rr.peeked != nil {
		return rr.peeked, nil
	}
	e, err := rr.Next()
	if err != nil {
		return nil, err
	}
	rr.peeked = e
	return e, nil
}

// Reset seeks back to the beginning and re-reads the header.
func (rr *RecordReader) Reset() error {
	if _, err := rr.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	rr.scanner = bufio.NewScanner(rr.file)
	rr.scanner.Buffer(make([]byte, 64), 1024*1024)
	rr.peeked = nil
	rr.lineNum = 0
	return rr.readHeader()
}

// Close closes the underlying file.
func (rr *RecordReader) Close() error {
	return rr.file.Close()
}

func (rr *RecordReader) readHeader() error {
	if !rr.scanner.Scan() {
		if err := rr.scanner.Err(); err != nil {
			return err
		}
		return errors.New("empty recording file")
	}
	rr.lineNum++

	line := rr.scanner.Bytes()
	var e RecordEvent
	if err := json.Unmarshal(line, &e); err != nil {
		return err
	}
	if !IsHeader(&e) {
		return errNotHeader
	}

	// Parse header payload
	if e.Msg == nil || e.Msg.Payload == nil {
		return errors.New("malformed header: missing payload")
	}
	payloadBytes, ok := e.Msg.Payload.(json.RawMessage)
	if !ok {
		return errors.New("malformed header: payload is not JSON")
	}
	var h FileHeader
	if err := json.Unmarshal(payloadBytes, &h); err != nil {
		return err
	}
	rr.header = &h
	return nil
}
