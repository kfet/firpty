package firpty

import (
	"strings"
	"sync"
	"unicode/utf8"
)

// Screen is a minimal VT100/ANSI terminal emulator that tracks a grid of
// runes plus a scrollback buffer. It implements just enough escape-sequence
// handling to faithfully capture the visible output of typical CLI programs.
type Screen struct {
	mu   sync.Mutex
	rows int
	cols int
	grid [][]rune
	cur  struct{ r, c int }

	scrollback []string
	maxScroll  int
}

// NewScreen creates a screen with the given dimensions.
func NewScreen(rows, cols int) *Screen {
	if rows <= 0 {
		rows = 1
	}
	if cols <= 0 {
		cols = 1
	}
	s := &Screen{rows: rows, cols: cols, maxScroll: 10000}
	s.grid = s.makeGrid()
	return s
}

func (s *Screen) makeGrid() [][]rune {
	g := make([][]rune, s.rows)
	for i := range g {
		g[i] = make([]rune, s.cols)
		for j := range g[i] {
			g[i][j] = ' '
		}
	}
	return g
}

// Write processes raw terminal output, including VT100/ANSI escape codes.
func (s *Screen) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := len(p)
	i := 0
	for i < n {
		b := p[i]
		switch {
		case b == 0x1b:
			i = s.handleEscape(p, i)
		case b == '\n':
			s.linefeed()
			i++
		case b == '\r':
			s.cur.c = 0
			i++
		case b == '\b':
			if s.cur.c > 0 {
				s.cur.c--
			}
			i++
		case b == '\t':
			s.cur.c = (s.cur.c + 8) &^ 7
			if s.cur.c >= s.cols {
				s.cur.c = s.cols - 1
			}
			i++
		case b == '\a':
			i++
		case b >= 0x20:
			r, size := utf8.DecodeRune(p[i:])
			if r == utf8.RuneError && size <= 1 {
				r = rune(b)
				size = 1
			}
			s.putRune(r)
			i += size
		default:
			i++
		}
	}
	return n, nil
}

func (s *Screen) putRune(r rune) {
	if s.cur.c >= s.cols {
		s.cur.c = 0
		s.linefeed()
	}
	s.grid[s.cur.r][s.cur.c] = r
	s.cur.c++
}

func (s *Screen) linefeed() {
	if s.cur.r == s.rows-1 {
		s.scrollback = append(s.scrollback, s.rowString(0))
		if len(s.scrollback) > s.maxScroll {
			s.scrollback = s.scrollback[len(s.scrollback)-s.maxScroll:]
		}
		copy(s.grid, s.grid[1:])
		s.grid[s.rows-1] = make([]rune, s.cols)
		for j := range s.grid[s.rows-1] {
			s.grid[s.rows-1][j] = ' '
		}
	} else {
		s.cur.r++
	}
}

func (s *Screen) handleEscape(p []byte, i int) int {
	if i+1 >= len(p) {
		return i + 1
	}
	switch p[i+1] {
	case '[':
		return s.handleCSI(p, i+2)
	case ']':
		j := i + 2
		for j < len(p) {
			if p[j] == '\a' {
				return j + 1
			}
			if p[j] == 0x1b && j+1 < len(p) && p[j+1] == '\\' {
				return j + 2
			}
			j++
		}
		return j
	case '(', ')':
		if i+2 < len(p) {
			return i + 3
		}
		return i + 2
	default:
		return i + 2
	}
}

func (s *Screen) handleCSI(p []byte, start int) int {
	j := start
	for j < len(p) && ((p[j] >= '0' && p[j] <= '9') || p[j] == ';' || p[j] == '?') {
		j++
	}
	if j >= len(p) {
		return j
	}
	params := parseCSIParams(string(p[start:j]))
	final := p[j]

	switch final {
	case 'A':
		s.cur.r = max(0, s.cur.r-paramDefault(params, 0, 1))
	case 'B':
		s.cur.r = min(s.rows-1, s.cur.r+paramDefault(params, 0, 1))
	case 'C':
		s.cur.c = min(s.cols-1, s.cur.c+paramDefault(params, 0, 1))
	case 'D':
		s.cur.c = max(0, s.cur.c-paramDefault(params, 0, 1))
	case 'H', 'f':
		s.cur.r = clamp(paramDefault(params, 0, 1)-1, 0, s.rows-1)
		s.cur.c = clamp(paramDefault(params, 1, 1)-1, 0, s.cols-1)
	case 'J':
		switch paramDefault(params, 0, 0) {
		case 0:
			s.clearRange(s.cur.r, s.cur.c, s.rows-1, s.cols-1)
		case 1:
			s.clearRange(0, 0, s.cur.r, s.cur.c)
		case 2, 3:
			s.grid = s.makeGrid()
			s.cur.r, s.cur.c = 0, 0
		}
	case 'K':
		switch paramDefault(params, 0, 0) {
		case 0:
			for c := s.cur.c; c < s.cols; c++ {
				s.grid[s.cur.r][c] = ' '
			}
		case 1:
			for c := 0; c <= s.cur.c; c++ {
				s.grid[s.cur.r][c] = ' '
			}
		case 2:
			for c := 0; c < s.cols; c++ {
				s.grid[s.cur.r][c] = ' '
			}
		}
	case 'G':
		s.cur.c = clamp(paramDefault(params, 0, 1)-1, 0, s.cols-1)
	case 'd':
		s.cur.r = clamp(paramDefault(params, 0, 1)-1, 0, s.rows-1)
	case 'L':
		n := paramDefault(params, 0, 1)
		for k := 0; k < n; k++ {
			if s.cur.r < s.rows-1 {
				copy(s.grid[s.cur.r+1:], s.grid[s.cur.r:s.rows-1])
			}
			s.grid[s.cur.r] = make([]rune, s.cols)
			for c := range s.grid[s.cur.r] {
				s.grid[s.cur.r][c] = ' '
			}
		}
	case 'M':
		n := paramDefault(params, 0, 1)
		for k := 0; k < n; k++ {
			if s.cur.r < s.rows-1 {
				copy(s.grid[s.cur.r:], s.grid[s.cur.r+1:])
			}
			s.grid[s.rows-1] = make([]rune, s.cols)
			for c := range s.grid[s.rows-1] {
				s.grid[s.rows-1][c] = ' '
			}
		}
	}
	return j + 1
}

func (s *Screen) clearRange(r1, c1, r2, c2 int) {
	for r := r1; r <= r2 && r < s.rows; r++ {
		startC, endC := 0, s.cols-1
		if r == r1 {
			startC = c1
		}
		if r == r2 {
			endC = c2
		}
		for c := startC; c <= endC && c < s.cols; c++ {
			s.grid[r][c] = ' '
		}
	}
}

func (s *Screen) rowString(r int) string {
	return strings.TrimRight(string(s.grid[r]), " ")
}

// Capture returns the last n lines (scrollback + visible). n<=0 means all.
func (s *Screen) Capture(lines int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	visible := []string{}
	lastNonEmpty := -1
	for r := 0; r < s.rows; r++ {
		line := s.rowString(r)
		visible = append(visible, line)
		if line != "" {
			lastNonEmpty = r
		}
	}
	visible = visible[:lastNonEmpty+1]

	all := make([]string, 0, len(s.scrollback)+len(visible))
	all = append(all, s.scrollback...)
	all = append(all, visible...)

	if lines > 0 && lines < len(all) {
		all = all[len(all)-lines:]
	}
	return strings.Join(all, "\n")
}

// CaptureVisible returns only the visible (non-scrollback) screen content.
func (s *Screen) CaptureVisible() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	lines := []string{}
	lastNonEmpty := -1
	for r := 0; r < s.rows; r++ {
		line := s.rowString(r)
		lines = append(lines, line)
		if line != "" {
			lastNonEmpty = r
		}
	}
	if lastNonEmpty < 0 {
		return ""
	}
	return strings.Join(lines[:lastNonEmpty+1], "\n")
}

func parseCSIParams(s string) []int {
	if s == "" || s[0] == '?' {
		return nil
	}
	parts := strings.Split(s, ";")
	params := make([]int, len(parts))
	for i, p := range parts {
		v := 0
		for _, ch := range p {
			if ch >= '0' && ch <= '9' {
				v = v*10 + int(ch-'0')
			}
		}
		params[i] = v
	}
	return params
}

func paramDefault(params []int, idx, def int) int {
	if idx < len(params) && params[idx] > 0 {
		return params[idx]
	}
	return def
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
