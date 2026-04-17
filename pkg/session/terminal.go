package session

import (
	"encoding/base64"
	"fmt"
	"strings"
	"sync"

	"github.com/fatih/color"
)

// VirtualTerminal emulates a terminal buffer for remote rendering.
type VirtualTerminal struct {
	Width       int
	Height      int
	mu          sync.Mutex
	buffer      [][]cell          // visible screen
	scrollback  [][]cell         // history
	cursorX     int
	cursorY     int
	savedX      int
	savedY      int
	sgr         sgrState
	maxScroll   int
}

// cell represents one character cell with styling.
type cell struct {
	Char rune
	FG   color.Attribute
	BG   color.Attribute
	Bold bool
}

type sgrState struct {
	parseState ansiParserState
	params     []int
	paramBuf   int
	bold       bool
	italic     bool
	underline  bool
	reverse    bool
	fg         color.Attribute
	bg         color.Attribute
}

type ansiParserState int

const (
	stateGround ansiParserState = iota
	stateEsc
	stateCSI
)

func (s *sgrState) getParam(index, def int) int {
	if index < len(s.params) {
		v := s.params[index]
		if v == 0 {
			return def
		}
		return v
	}
	return def
}

var (
	fgBlack   = color.FgBlack
	fgRed     = color.FgRed
	fgGreen   = color.FgGreen
	fgYellow  = color.FgYellow
	fgBlue    = color.FgBlue
	fgMagenta = color.FgMagenta
	fgCyan    = color.FgCyan
	fgWhite   = color.FgWhite
	fgHiBlack = color.FgHiBlack
	fgHiRed   = color.FgHiRed
	fgHiGreen = color.FgHiGreen
	fgHiYellow = color.FgHiYellow
	fgHiBlue  = color.FgHiBlue
	fgHiMagenta = color.FgHiMagenta
	fgHiCyan  = color.FgHiCyan
	fgHiWhite = color.FgHiWhite
)

func NewVirtualTerminal(width, height int) *VirtualTerminal {
	vt := &VirtualTerminal{
		Width:     width,
		Height:    height,
		maxScroll: 10000,
	}
	vt.initBuffer()
	return vt
}

func (vt *VirtualTerminal) initBuffer() {
	vt.buffer = make([][]cell, vt.Height)
	vt.scrollback = make([][]cell, 0, vt.maxScroll)
	emptyCell := cell{Char: ' ', FG: fgWhite, BG: color.FgBlack}
	for i := range vt.buffer {
		row := make([]cell, vt.Width)
		for j := range row {
			row[j] = emptyCell
		}
		vt.buffer[i] = row
	}
}

func (vt *VirtualTerminal) Resize(width, height int) {
	vt.mu.Lock()
	defer vt.mu.Unlock()
	if width == vt.Width && height == vt.Height {
		return
	}
	oldBuffer := vt.buffer
	oldH := vt.Height
	oldW := vt.Width
	vt.Width = width
	vt.Height = height
	vt.initBuffer()
	// Copy what fits
	for y := 0; y < min(height, oldH); y++ {
		for x := 0; x < min(width, oldW); x++ {
			vt.buffer[y][x] = oldBuffer[y][x]
		}
	}
	vt.cursorX = min(vt.cursorX, width-1)
	vt.cursorY = min(vt.cursorY, height-1)
}

// Write decodes base64-encoded terminal data and applies it to the buffer.
func (vt *VirtualTerminal) Write(b64 string) error {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return err
	}
	vt.mu.Lock()
	defer vt.mu.Unlock()
	vt.parse(raw)
	return nil
}

// WriteRaw applies raw bytes directly to the buffer (after parsing escape sequences).
func (vt *VirtualTerminal) WriteRaw(data []byte) {
	vt.mu.Lock()
	defer vt.mu.Unlock()
	vt.parse(data)
}

// parse processes a stream of bytes through the ANSI parser state machine.
func (vt *VirtualTerminal) parse(data []byte) {
	i := 0
	for i < len(data) {
		b := data[i]
		switch vt.sgr.parseState {
		case stateGround:
			switch b {
			case 0x1b: // ESC
				vt.sgr.parseState = stateEsc
			case '\r':
				vt.cursorX = 0
				i++
			case '\n':
				vt.newline()
				i++
			case '\t':
				vt.cursorX = (vt.cursorX / 8 * 8) + 8
				if vt.cursorX >= vt.Width {
					vt.cursorX = vt.Width - 1
				}
				i++
			case '\b':
				if vt.cursorX > 0 {
					vt.cursorX--
				}
				i++
			case 0x07: // BEL
				// Bell — ignore in buffer
				i++
			default:
				if b >= 0x20 {
					vt.putCell(rune(b))
				}
				i++
			}
		case stateEsc:
			if b == '[' {
				vt.sgr.parseState = stateCSI
				vt.sgr.params = nil
				vt.sgr.paramBuf = 0
			} else {
				vt.sgr.parseState = stateGround
			}
			i++
		case stateCSI:
			switch b {
			case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
				vt.sgr.paramBuf = vt.sgr.paramBuf*10 + int(b-'0')
			case ';':
				vt.sgr.params = append(vt.sgr.params, vt.sgr.paramBuf)
				vt.sgr.paramBuf = 0
			case 'A': // CUU — cursor up
				n := vt.sgr.getParam(0, 1)
				vt.cursorY = max(0, vt.cursorY-n)
				vt.sgr.parseState = stateGround
				i++
			case 'B': // CUD — cursor down
				n := vt.sgr.getParam(0, 1)
				vt.cursorY = min(vt.Height-1, vt.cursorY+n)
				vt.sgr.parseState = stateGround
				i++
			case 'C': // CUF — cursor forward
				n := vt.sgr.getParam(0, 1)
				vt.cursorX = min(vt.Width-1, vt.cursorX+n)
				vt.sgr.parseState = stateGround
				i++
			case 'D': // CUB — cursor back
				n := vt.sgr.getParam(0, 1)
				vt.cursorX = max(0, vt.cursorX-n)
				vt.sgr.parseState = stateGround
				i++
			case 'H': // CUP — cursor position
				vt.cursorY = clamp(vt.sgr.getParam(0, 1)-1, 0, vt.Height-1)
				vt.cursorX = clamp(vt.sgr.getParam(1, 1)-1, 0, vt.Width-1)
				vt.sgr.parseState = stateGround
				i++
			case 'f': // HVP — cursor position (same as CUP)
				vt.cursorY = clamp(vt.sgr.getParam(0, 1)-1, 0, vt.Height-1)
				vt.cursorX = clamp(vt.sgr.getParam(1, 1)-1, 0, vt.Width-1)
				vt.sgr.parseState = stateGround
				i++
			case 'J': // ED — erase in display
				mode := vt.sgr.getParam(0, 0)
				vt.eraseDisplay(mode)
				vt.sgr.parseState = stateGround
				i++
			case 'K': // EK — erase in line
				mode := vt.sgr.getParam(0, 0)
				vt.eraseLine(mode)
				vt.sgr.parseState = stateGround
				i++
			case 'm': // SGR — set graphics rendition
				vt.applySGR()
				vt.sgr.parseState = stateGround
				i++
			case 'r': // DECSTBM — set top/bottom margins (ignore)
				vt.sgr.parseState = stateGround
				i++
			case 'l': // mode set (ignore)
				vt.sgr.parseState = stateGround
				i++
			case 'h': // mode set (ignore)
				vt.sgr.parseState = stateGround
				i++
			case 'n': // DSR — device status report
				// Ignore — we don't respond to these
				vt.sgr.parseState = stateGround
				i++
			case '@': // ICH — insert characters
				n := vt.sgr.getParam(0, 1)
				vt.insertChars(n)
				vt.sgr.parseState = stateGround
				i++
			case 'P': // DCH — delete characters
				n := vt.sgr.getParam(0, 1)
				vt.deleteChars(n)
				vt.sgr.parseState = stateGround
				i++
			case 'M': // DL — delete lines
				n := vt.sgr.getParam(0, 1)
				vt.deleteLines(n)
				vt.sgr.parseState = stateGround
				i++
			default:
				// Unknown CSI — ignore
				vt.sgr.parseState = stateGround
				i++
			}
		}
	}
}

func (s *sgrState) reset() {
	s.bold = false
	s.italic = false
	s.underline = false
	s.reverse = false
	s.fg = fgWhite
	s.bg = color.FgBlack
}

func (vt *VirtualTerminal) applySGR() {
	params := vt.sgr.params
	if len(params) == 0 {
		vt.sgr.reset()
		return
	}
	i := 0
	for i < len(params) {
		p := params[i]
		switch {
		case p == 0:
			vt.sgr.reset()
		case p == 1:
			vt.sgr.bold = true
		case p == 3:
			vt.sgr.italic = true
		case p == 4:
			vt.sgr.underline = true
		case p == 7:
			vt.sgr.reverse = true
		case p == 22:
			vt.sgr.bold = false
		case p == 23:
			vt.sgr.italic = false
		case p == 24:
			vt.sgr.underline = false
		case p == 27:
			vt.sgr.reverse = false
		case p >= 30 && p <= 37:
			vt.sgr.fg = color.Attribute(p)
		case p == 39:
			vt.sgr.fg = fgWhite
		case p >= 40 && p <= 47:
			vt.sgr.bg = color.Attribute(p)
		case p == 49:
			vt.sgr.bg = color.FgBlack
		case p >= 90 && p <= 97:
			vt.sgr.fg = color.Attribute(p)
		case p >= 100 && p <= 107:
			vt.sgr.bg = color.Attribute(p)
		case p >= 38 && i+2 < len(params) && params[i+1] == 5: // 256-color
			vt.sgr.fg = color.Attribute(params[i+2] + 232)
			i += 2
		case p >= 38 && i+4 < len(params) && params[i+1] == 2: // 24-bit color
			// We approximate 24-bit with 256-color
			r, g, b := params[i+2], params[i+3], params[i+4]
			vt.sgr.fg = rgbToColor(r, g, b)
			i += 4
		case p >= 48 && i+2 < len(params) && params[i+1] == 5:
			vt.sgr.bg = color.Attribute(params[i+2] + 232)
			i += 2
		case p >= 48 && i+4 < len(params) && params[i+1] == 2:
			r, g, b := params[i+2], params[i+3], params[i+4]
			vt.sgr.bg = rgbToColor(r, g, b)
			i += 4
		}
		i++
	}
}

func rgbToColor(r, g, b int) color.Attribute {
	// Map 24-bit RGB to the nearest xterm 256-color index
	// 0-5 range for each component in xterm's 6x6x6 cube
	ri := r / 40
	gi := g / 40
	bi := b / 40
	index := 16 + ri*36 + gi*6 + bi
	if index > 255 {
		index = 255
	}
	return color.Attribute(index)
}

func (vt *VirtualTerminal) putCell(ch rune) {
	// Grow row if needed
	row := vt.buffer[vt.cursorY]
	if len(row) < vt.Width {
		newRow := make([]cell, vt.Width)
		copy(newRow, row)
		for j := len(row); j < vt.Width; j++ {
			newRow[j] = cell{Char: ' ', FG: fgWhite, BG: color.FgBlack}
		}
		vt.buffer[vt.cursorY] = newRow
	}
	if vt.cursorX >= vt.Width {
		vt.cursorX = vt.Width - 1
	}
	cell := cell{
		Char: ch,
		FG:   vt.sgr.fg,
		BG:   vt.sgr.bg,
		Bold: vt.sgr.bold,
	}
	if vt.sgr.reverse {
		cell.FG, cell.BG = cell.BG, cell.FG
	}
	vt.buffer[vt.cursorY][vt.cursorX] = cell
	vt.cursorX++
}

func (vt *VirtualTerminal) newline() {
	vt.cursorX = 0
	vt.cursorY++
	if vt.cursorY >= vt.Height {
		// Scroll up: move top line to scrollback
		top := vt.buffer[0]
		vt.scrollback = append(vt.scrollback, top)
		if len(vt.scrollback) > vt.maxScroll {
			vt.scrollback = vt.scrollback[1:]
		}
		// Shift buffer up
		copy(vt.buffer, vt.buffer[1:])
		// Clear bottom line
		last := vt.Height - 1
		emptyCell := cell{Char: ' ', FG: fgWhite, BG: color.FgBlack}
		row := make([]cell, vt.Width)
		for i := range row {
			row[i] = emptyCell
		}
		vt.buffer[last] = row
		vt.cursorY = vt.Height - 1
	}
}

func (vt *VirtualTerminal) eraseDisplay(mode int) {
	switch mode {
	case 0: // Erase from cursor to end
		vt.eraseLine(0)
		for y := vt.cursorY + 1; y < vt.Height; y++ {
			vt.clearRow(y)
		}
	case 1: // Erase from start to cursor
		for y := 0; y < vt.cursorY; y++ {
			vt.clearRow(y)
		}
		vt.eraseLine(1)
	case 2, 3: // Erase entire screen
		for y := 0; y < vt.Height; y++ {
			vt.clearRow(y)
		}
		vt.cursorX = 0
		vt.cursorY = 0
	}
}

func (vt *VirtualTerminal) eraseLine(mode int) {
	switch mode {
	case 0: // Erase from cursor to end of line
		vt.clearRowFrom(vt.cursorY, vt.cursorX)
	case 1: // Erase from start to cursor
		vt.clearRowTo(vt.cursorY, vt.cursorX)
	case 2: // Erase entire line
		vt.clearRow(vt.cursorY)
	}
}

func (vt *VirtualTerminal) clearRow(y int) {
	emptyCell := cell{Char: ' ', FG: fgWhite, BG: color.FgBlack}
	for i := range vt.buffer[y] {
		vt.buffer[y][i] = emptyCell
	}
}

func (vt *VirtualTerminal) clearRowFrom(y, fromX int) {
	emptyCell := cell{Char: ' ', FG: fgWhite, BG: color.FgBlack}
	for x := fromX; x < len(vt.buffer[y]); x++ {
		vt.buffer[y][x] = emptyCell
	}
}

func (vt *VirtualTerminal) clearRowTo(y, toX int) {
	emptyCell := cell{Char: ' ', FG: fgWhite, BG: color.FgBlack}
	for x := 0; x <= toX && x < len(vt.buffer[y]); x++ {
		vt.buffer[y][x] = emptyCell
	}
}

func (vt *VirtualTerminal) insertChars(n int) {
	row := vt.buffer[vt.cursorY]
	end := vt.Width - n
	for x := min(vt.cursorX, end); x < end; x++ {
		row[x] = row[x+n]
	}
	for x := max(0, end); x < vt.Width; x++ {
		row[x] = cell{Char: ' ', FG: fgWhite, BG: color.FgBlack}
	}
}

func (vt *VirtualTerminal) deleteChars(n int) {
	row := vt.buffer[vt.cursorY]
	for x := vt.cursorX; x < vt.Width-n; x++ {
		row[x] = row[x+n]
	}
	for x := max(0, vt.Width-n); x < vt.Width; x++ {
		row[x] = cell{Char: ' ', FG: fgWhite, BG: color.FgBlack}
	}
}

func (vt *VirtualTerminal) deleteLines(n int) {
	// Shift lines up from cursor position
	start := vt.cursorY
	for y := start + n; y < vt.Height; y++ {
		vt.buffer[y-n] = vt.buffer[y]
	}
	emptyCell := cell{Char: ' ', FG: fgWhite, BG: color.FgBlack}
	for y := max(0, vt.Height-n); y < vt.Height; y++ {
		row := make([]cell, vt.Width)
		for i := range row {
			row[i] = emptyCell
		}
		vt.buffer[y] = row
	}
}

// Render returns the current terminal buffer as a string for display.
// ApplyCursorOverlays applies ANSI cursor badges from other participants.
func (vt *VirtualTerminal) Render() string {
	var sb strings.Builder
	for y := 0; y < vt.Height; y++ {
		row := vt.buffer[y]
		for x := 0; x < vt.Width; x++ {
			c := row[x]
			s := ""
			if c.Bold {
				s = s + "\x1b[1m"
			}
			s = s + fmt.Sprintf("\x1b[38;5;%dm\x1b[48;5;%dm%c", c.FG-232, c.BG-232, c.Char)
			sb.WriteString(s)
		}
		sb.WriteString("\x1b[0m\r\n")
	}
	return sb.String()
}

// RenderWithOverlays renders the terminal buffer and overlays cursor badges.
func (vt *VirtualTerminal) RenderWithOverlays(cursors []CursorInfo, splitWidth int) string {
	var sb strings.Builder
	for y := 0; y < vt.Height; y++ {
		row := vt.buffer[y]
		// Render terminal cells up to split
		for x := 0; x < vt.Width && x < splitWidth; x++ {
			c := row[x]
			fg := c.FG
			bg := c.BG
			if c.Bold {
				sb.WriteString("\x1b[1m")
			}
			sb.WriteString(fmt.Sprintf("\x1b[38;5;%dm\x1b[48;5;%dm%c", fg-232, bg-232, c.Char))
		}
		sb.WriteString("\x1b[0m")
		sb.WriteString("\r\n")
	}
	return sb.String()
}

// CursorInfo holds the position and identity of a remote cursor.
type CursorInfo struct {
	UserID   string
	Username string
	Color    string
	X        int
	Y        int
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}