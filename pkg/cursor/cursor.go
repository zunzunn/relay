package cursor

import (
	"fmt"
	"sync"
)

// Cursor represents a remote participant's cursor in the terminal.
type Cursor struct {
	UserID   string
	Username string
	Color    string // hex, e.g. "#FF6B6B"
	X        int
	Y        int
	Visible  bool
}

// Registry tracks all active cursors.
type Registry struct {
	mu      sync.RWMutex
	cursors map[string]*Cursor
}

func NewRegistry() *Registry {
	return &Registry{cursors: make(map[string]*Cursor)}
}

// Update inserts or updates a cursor position.
func (r *Registry) Update(c *Cursor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cursors[c.UserID] = c
}

// Remove deletes a cursor.
func (r *Registry) Remove(userID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cursors, userID)
}

// Get returns a cursor by userID.
func (r *Registry) Get(userID string) (*Cursor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.cursors[userID]
	return c, ok
}

// All returns all active cursors.
func (r *Registry) All() []*Cursor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var cursors []*Cursor
	for _, c := range r.cursors {
		if c.Visible {
			cursors = append(cursors, c)
		}
	}
	return cursors
}

// RenderBadge returns an ANSI escape sequence to draw a cursor badge.
// The badge is rendered BELOW the cursor position (line Y+1) to avoid
// obscuring the character under the cursor.
func RenderBadge(c *Cursor, termHeight, termWidth int) string {
	if !c.Visible || c.X < 0 || c.Y < 0 {
		return ""
	}
	// Place badge at cursor position; if at bottom row, place above
	badgeY := c.Y
	badgeX := c.X
	if badgeY >= termHeight-1 {
		badgeY = c.Y - 1
	}
	if badgeY < 0 {
		badgeY = 0
	}
	if badgeX >= termWidth {
		badgeX = termWidth - 1
	}
	// Convert hex color to RGB components
	r, g, b := hexToRGB(c.Color)
	fgR, fgG, fgB := contrastingColor(r, g, b)

	label := fmt.Sprintf("[%s]", c.Username)
	// Move to badge position, print colored background + text, move back
	return fmt.Sprintf(
		"\x1b[%d;%dH\x1b[48;2;%d;%d;%dm\x1b[38;2;%d;%d;%dm%s\x1b[0m",
		badgeY+1, badgeX+1,
		r, g, b,
		fgR, fgG, fgB,
		label,
	)
}

// RenderAll renders all cursor badges as a single ANSI string.
func RenderAll(cursors []*Cursor, termHeight, termWidth int) string {
	var result string
	for _, c := range cursors {
		result += RenderBadge(c, termHeight, termWidth)
	}
	return result
}

func hexToRGB(hex string) (r, g, b int) {
	hex = hex[1:] // strip '#'
	fmt.Sscanf(hex, "%02x%02x%02x", &r, &g, &b)
	return
}

// contrastingColor returns a high-contrast text color for a given background.
func contrastingColor(r, g, b int) (int, int, int) {
	// Luminance formula
	lum := 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
	if lum > 128 {
		return 0, 0, 0   // black text on light bg
	}
	return 255, 255, 255 // white text on dark bg
}