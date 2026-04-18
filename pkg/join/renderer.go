package join

import (
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	sidebarWidth = 30
	maxLines    = 200
)

// Renderer maintains terminal + sidebar buffers and renders split view.
type Renderer struct {
	mu            sync.Mutex
	termLines     []string
	sidebarLines  []string
	width         int
	height        int
	dirty         bool
}

// NewRenderer creates a renderer with the given terminal dimensions.
func NewRenderer(width, height int) *Renderer {
	return &Renderer{width: width, height: height}
}

// AddTerminal decodes and appends terminal output.
func (r *Renderer) AddTerminal(b64 string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return
	}
	lines := strings.Split(string(decoded), "\n")
	r.termLines = append(r.termLines, lines...)
	if len(r.termLines) > maxLines {
		r.termLines = r.termLines[len(r.termLines)-maxLines:]
	}
	r.dirty = true
}

// AddSidebar appends a line to the sidebar.
func (r *Renderer) AddSidebar(format string, args ...interface{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	line := fmt.Sprintf(format, args...)
	r.sidebarLines = append(r.sidebarLines, line)
	if len(r.sidebarLines) > r.maxSidebar() {
		r.sidebarLines = r.sidebarLines[len(r.sidebarLines)-r.maxSidebar():]
	}
	r.dirty = true
}

func (r *Renderer) maxSidebar() int {
	h := r.height
	if h <= 0 {
		h = 24
	}
	return h - 2
}

// Render draws the split view to stdout.
func (r *Renderer) Render() {
	r.mu.Lock()
	if !r.dirty {
		r.mu.Unlock()
		return
	}
	r.dirty = false
	termWidth := r.width - sidebarWidth - 1
	if termWidth < 20 {
		termWidth = r.width / 2
	}
	if termWidth < 1 {
		termWidth = 40
	}
	termLines := r.termLines
	sidebarLines := r.sidebarLines
	height := r.height
	if height <= 0 {
		height = 24
	}
	termLines = termLinesOf(append([]string(nil), termLines...), height-1)
	sidebarLines = sidebarLinesOf(append([]string(nil), sidebarLines...), height-1)
	r.mu.Unlock()

	// Clear screen and home cursor
	fmt.Print("\033[2J\033[H")

	for i := 0; i < height-1; i++ {
		var left, right string
		if i < len(termLines) {
			left = truncate(termLines[i], termWidth)
		}
		if i < len(sidebarLines) {
			right = sidebarLines[i]
		}
		// Write left (terminal), move cursor right, write sidebar, move to next line
		fmt.Print(left)
		fmt.Printf("\033[%dC", termWidth+1)
		fmt.Print(right)
		if i < height-2 {
			fmt.Print("\033[1B\r")
		}
	}
	// Status bar
	fmt.Print("\033[1;1H")
	sep := strings.Repeat("─", r.width)
	fmt.Printf("\033[38;5;240m%s\033[0m", sep)
	fmt.Printf("\033[1;%dH", r.width-sidebarWidth)
	fmt.Printf("\033[38;5;240m %s \033[0m", "EVENTS")
}

// IsDirty reports whether the view needs re-rendering.
func (r *Renderer) IsDirty() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dirty
}

// Resize updates dimensions.
func (r *Renderer) Resize(width, height int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.width = width
	r.height = height
	r.dirty = true
}

func termLinesOf(lines []string, limit int) []string {
	start := 0
	if len(lines) > limit {
		start = len(lines) - limit
	}
	var trimmed []string
	for i := start; i < len(lines); i++ {
		trimmed = append(trimmed, lines[i])
	}
	// pad to limit
	for len(trimmed) < limit {
		trimmed = append(trimmed, "")
	}
	return trimmed[:limit]
}

func sidebarLinesOf(lines []string, limit int) []string {
	start := 0
	if len(lines) > limit {
		start = len(lines) - limit
	}
	var trimmed []string
	for i := start; i < len(lines); i++ {
		lines[i] = "│ " + lines[i]
		trimmed = append(trimmed, lines[i])
	}
	for len(trimmed) < limit {
		trimmed = append(trimmed, "│")
	}
	return trimmed[:limit]
}

func truncate(s string, width int) string {
	runes := []rune(s)
	if len(runes) > width {
		return string(runes[:width-1]) + "…"
	}
	return s + spaces(width - len(runes))
}

func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(" ", n)
}

// formatTimestamp returns a short HH:MM timestamp.
func formatTimestamp(ts int64) string {
	return time.Unix(ts, 0).Format("15:04")
}
