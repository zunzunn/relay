package session

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"
)

// PTY holds the file descriptor and subprocess for a pseudo-terminal session.
type PTY struct {
	File *os.File
	Cmd  *exec.Cmd
	Rows int
	Cols int
}

// SpawnPTY creates a new pseudo-terminal with a shell running inside.
func SpawnPTY(cols, rows int) (*PTY, error) {
	ptmx, _, err := pty.Open()
	if err != nil {
		return nil, fmt.Errorf("open ptmx: %w", err)
	}

	pty.Setsize(ptmx, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})

	shell := getShell()
	cmd := exec.Command(shell, "-i")
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		fmt.Sprintf("LINES=%d", rows),
		fmt.Sprintf("COLUMNS=%d", cols),
	)
	cmd.Dir = os.Getenv("HOME")

	setCmdAttrs(cmd)

	cmd.Stdin = ptmx
	cmd.Stdout = ptmx
	cmd.Stderr = ptmx

	if err := cmd.Start(); err != nil {
		ptmx.Close()
		return nil, fmt.Errorf("start shell: %w", err)
	}

	return &PTY{File: ptmx, Cmd: cmd, Rows: rows, Cols: cols}, nil
}

func getShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/bash"
}

func setCmdAttrs(cmd *exec.Cmd) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Setsid: true,
		}
		// Setctty only works on Linux
		if runtime.GOOS == "linux" {
			cmd.SysProcAttr.Setctty = true
		}
	}
}

// Resize updates the PTY dimensions and sends a SIGWINCH to the child process.
func (p *PTY) Resize(cols, rows int) error {
	p.Cols = cols
	p.Rows = rows
	return pty.Setsize(p.File, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}

// WriteString writes a string to the PTY.
func (p *PTY) WriteString(s string) (int, error) {
	return p.File.WriteString(s)
}

// InjectCommand writes a command followed by a newline to the PTY.
// Used when the host approves a queued command.
func InjectCommand(p *PTY, command string) error {
	_, err := p.WriteString(command + "\n")
	return err
}

// GetSize returns the current terminal dimensions.
func (p *PTY) GetSize() (cols, rows int, err error) {
	return term.GetSize(int(p.File.Fd()))
}

// ReadLoop reads from the PTY and calls onData with base64-encoded chunks.
// It blocks until the PTY is closed or the context is cancelled.
func (p *PTY) ReadLoop(onData func(b64 string)) error {
	buf := make([]byte, 4096)
	for {
		n, err := p.File.Read(buf)
		if n > 0 {
			data := base64.StdEncoding.EncodeToString(buf[:n])
			onData(data)
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// StartCursorPoller periodically queries the cursor position and calls onMove.
func StartCursorPoller(ptmx *os.File, interval time.Duration, onMove func(x, y int)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		x, y := queryCursorPos(ptmx)
		onMove(x, y)
	}
}

// queryCursorPos queries the terminal for the current cursor position.
// It sends a Device Status Report (DSR) escape sequence and reads the response.
func queryCursorPos(ptmx *os.File) (x, y int) {
	// Send DSR (Device Status Report) — "Report Cursor Position"
	ptmx.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
	ptmx.Write([]byte("\x1b[6n"))

	// Read response: ESC [ <row> ; <col> R
	ptmx.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 20)
	n, err := ptmx.Read(buf)
	if err != nil {
		return -1, -1
	}

	// Parse CSI 6 n response: \x1b[<row>;<col>R
	data := string(buf[:n])
	var row, col int
	_, err = fmt.Sscanf(data, "\x1b[%d;%dR", &row, &col)
	if err != nil {
		return -1, -1
	}
	return col - 1, row - 1 // convert to 0-based
}