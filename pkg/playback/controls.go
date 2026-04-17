package playback

import "fmt"

// PlayerState represents the current playback state.
type PlayerState int

const (
	statePlaying PlayerState = iota
	statePaused
	stateEnded
)

// PlayerControl represents a keyboard control action.
type PlayerControl int

const (
	ctrlNone PlayerControl = iota
	ctrlPause
	ctrlStepFwd
	ctrlStepBwd
	ctrlSpeedUp
	ctrlSpeedDown
	ctrlSeekStart
	ctrlSeekEnd
	ctrlQuit
)

// parseKey maps a raw byte sequence to a PlayerControl.
// Raw stdin provides bytes; arrow keys are multi-byte escape sequences.
// Returns ctrlNone for unhandled input.
func parseKey(buf []byte) PlayerControl {
	if len(buf) == 0 {
		return ctrlNone
	}
	switch buf[0] {
	case ' ':
		return ctrlPause
	case '+', '=':
		return ctrlSpeedUp
	case '-':
		return ctrlSpeedDown
	case 'n', '\x06': // 'n' or Ctrl+F (forward)
		return ctrlStepFwd
	case 'p', '\x02': // 'p' or Ctrl+B (backward)
		return ctrlStepBwd
	case 'g':
		return ctrlSeekStart
	case 'G':
		return ctrlSeekEnd
	case 'q', '\x1b': // 'q' or Escape
		return ctrlQuit
	}
	// Arrow key sequences: ESC [ A/B/C/D
	if len(buf) >= 3 && buf[0] == 0x1b && buf[1] == '[' {
		switch buf[2] {
		case 'A':
			return ctrlNone // up: no action
		case 'B':
			return ctrlNone // down: no action
		case 'C':
			return ctrlStepFwd // right arrow
		case 'D':
			return ctrlStepBwd // left arrow
		}
	}
	return ctrlNone
}

// speedLabel returns a human-readable speed string.
func speedLabel(speed float64) string {
	switch speed {
	case 0.25:
		return "0.25x"
	case 0.5:
		return "0.5x"
	case 1.0:
		return "1x"
	case 2.0:
		return "2x"
	case 4.0:
		return "4x"
	case 8.0:
		return "8x"
	default:
		return "?"
	}
}

// statusBar returns the bottom-of-screen status bar string.
func statusBar(state PlayerState, speed float64, current, total int, eventTS, firstTS, lastTS int64) string {
	var stateStr string
	switch state {
	case statePlaying:
		stateStr = "\x1b[32m[PLAY]\x1b[0m"
	case statePaused:
		stateStr = "\x1b[33m[PAUSE]\x1b[0m"
	case stateEnded:
		stateStr = "\x1b[36m[END]\x1b[0m"
	}

	speedStr := speedLabel(speed)

	pos := fmt.Sprintf("%d/%d", current, total)

	elapsed := ""
	if firstTS > 0 && eventTS > firstTS {
		elapsed = fmtDuration(eventTS - firstTS)
	}

	bar := fmt.Sprintf("%s %s  %s  evt:%s SPACE:pause  +/-:speed  n/p:step  g/G:seek  q:quit",
		stateStr, speedStr, pos, elapsed)
	return bar
}

func fmtDuration(ns int64) string {
	sec := ns / 1e9
	m := sec / 60
	s := sec % 60
	return fmt.Sprintf("%02d:%02d", m, s)
}
