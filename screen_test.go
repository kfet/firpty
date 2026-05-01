package firpty

import (
	"strings"
	"testing"
)

func TestScreen_BasicWrite(t *testing.T) {
	s := NewScreen(24, 80)
	if _, err := s.Write([]byte("hello world")); err != nil {
		t.Fatal(err)
	}
	if got := s.CaptureVisible(); got != "hello world" {
		t.Fatalf("got %q", got)
	}
}

func TestScreen_NewScreenClampsZero(t *testing.T) {
	s := NewScreen(0, 0)
	if s.rows != 1 || s.cols != 1 {
		t.Fatalf("want 1x1, got %dx%d", s.rows, s.cols)
	}
}

func TestScreen_Newline(t *testing.T) {
	s := NewScreen(24, 80)
	s.Write([]byte("line1\r\nline2\r\nline3"))
	if got := s.CaptureVisible(); got != "line1\nline2\nline3" {
		t.Fatalf("got %q", got)
	}
}

func TestScreen_CursorMovementBackward(t *testing.T) {
	s := NewScreen(24, 80)
	s.Write([]byte("ABCDE"))
	s.Write([]byte("\x1b[3Dxyz"))
	if got := s.CaptureVisible(); got != "ABxyz" {
		t.Fatalf("got %q", got)
	}
}

func TestScreen_CursorMovementUpDownForward(t *testing.T) {
	s := NewScreen(5, 10)
	s.Write([]byte("\x1b[3;1H"))
	s.Write([]byte("X"))
	s.Write([]byte("\x1b[2A"))
	s.Write([]byte("Y"))
	s.Write([]byte("\x1b[3B"))
	s.Write([]byte("Z"))
	s.Write([]byte("\x1b[5C"))
	s.Write([]byte("Q"))
	out := s.Capture(0)
	if !strings.Contains(out, "Y") || !strings.Contains(out, "X") ||
		!strings.Contains(out, "Z") || !strings.Contains(out, "Q") {
		t.Fatalf("missing chars in %q", out)
	}
}

func TestScreen_DefaultParamsForCursorMoves(t *testing.T) {
	s := NewScreen(5, 10)
	s.Write([]byte("ab"))
	// CSI A/B/C/D with no params default to 1.
	s.Write([]byte("\x1b[A"))
	s.Write([]byte("\x1b[B"))
	s.Write([]byte("\x1b[C"))
	s.Write([]byte("\x1b[D"))
	// No assertion on exact placement; just ensure no panic and content remains.
	if !strings.Contains(s.CaptureVisible(), "ab") {
		t.Fatal("content lost")
	}
}

func TestScreen_EraseInLine(t *testing.T) {
	cases := []struct {
		name, write, want string
	}{
		{"to_end", "hello world\x1b[6G\x1b[K", "hello"},
		{"to_start", "hello world\x1b[6G\x1b[1K", "      world"},
		{"whole", "hello world\x1b[2K", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewScreen(2, 80)
			s.Write([]byte(tc.write))
			if got := s.CaptureVisible(); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestScreen_EraseDisplay(t *testing.T) {
	t.Run("below", func(t *testing.T) {
		s := NewScreen(4, 5)
		s.Write([]byte("aaaaa\r\nbbbbb\r\nccccc"))
		s.Write([]byte("\x1b[2;3H\x1b[J")) // row2 col3, erase below
		out := s.CaptureVisible()
		if !strings.Contains(out, "aaaaa") {
			t.Fatalf("row 1 cleared: %q", out)
		}
		if strings.Contains(out, "ccccc") {
			t.Fatalf("row 3 not cleared: %q", out)
		}
	})
	t.Run("above", func(t *testing.T) {
		s := NewScreen(4, 5)
		s.Write([]byte("aaaaa\r\nbbbbb\r\nccccc"))
		s.Write([]byte("\x1b[2;3H\x1b[1J"))
		out := s.CaptureVisible()
		if strings.Contains(out, "aaaaa") {
			t.Fatalf("row 1 not cleared: %q", out)
		}
		if !strings.Contains(out, "ccccc") {
			t.Fatalf("row 3 cleared: %q", out)
		}
	})
	t.Run("all_2", func(t *testing.T) {
		s := NewScreen(4, 5)
		s.Write([]byte("aaaaa\r\nbbbbb"))
		s.Write([]byte("\x1b[2J"))
		if got := s.CaptureVisible(); got != "" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("all_3", func(t *testing.T) {
		s := NewScreen(4, 5)
		s.Write([]byte("aaaaa"))
		s.Write([]byte("\x1b[3J"))
		if got := s.CaptureVisible(); got != "" {
			t.Fatalf("got %q", got)
		}
	})
}

func TestScreen_Scrollback(t *testing.T) {
	s := NewScreen(3, 80)
	s.Write([]byte("a\r\nb\r\nc\r\nd\r\ne"))
	if got := s.CaptureVisible(); got != "c\nd\ne" {
		t.Fatalf("visible: %q", got)
	}
	if got := s.Capture(5); got != "a\nb\nc\nd\ne" {
		t.Fatalf("all: %q", got)
	}
	if got := s.Capture(2); got != "d\ne" {
		t.Fatalf("tail: %q", got)
	}
	// lines<=0 returns everything.
	if got := s.Capture(0); got != "a\nb\nc\nd\ne" {
		t.Fatalf("zero: %q", got)
	}
}

func TestScreen_ScrollbackTrim(t *testing.T) {
	s := NewScreen(2, 5)
	s.maxScroll = 3
	for i := 0; i < 10; i++ {
		s.Write([]byte("x\n"))
	}
	if len(s.scrollback) != 3 {
		t.Fatalf("scrollback len %d", len(s.scrollback))
	}
}

func TestScreen_CursorPosition(t *testing.T) {
	s := NewScreen(24, 80)
	s.Write([]byte("\x1b[3;5Htest"))
	lines := strings.Split(s.CaptureVisible(), "\n")
	if len(lines) < 3 {
		t.Fatalf("lines=%d", len(lines))
	}
	if lines[2] != "    test" {
		t.Fatalf("row3 %q", lines[2])
	}
}

func TestScreen_CursorPositionClamps(t *testing.T) {
	s := NewScreen(3, 5)
	s.Write([]byte("\x1b[99;99H!"))
	if got := s.CaptureVisible(); !strings.Contains(got, "!") {
		t.Fatalf("got %q", got)
	}
}

func TestScreen_CursorPositionFForm(t *testing.T) {
	s := NewScreen(3, 5)
	s.Write([]byte("\x1b[2;2f!"))
	if got := strings.Split(s.CaptureVisible(), "\n"); got[1] != " !" {
		t.Fatalf("got %q", got)
	}
}

func TestScreen_GAndDAbsolute(t *testing.T) {
	s := NewScreen(3, 10)
	s.Write([]byte("X"))
	s.Write([]byte("\x1b[5G!"))
	s.Write([]byte("\x1b[3d?"))
	out := strings.Split(s.CaptureVisible(), "\n")
	if !strings.Contains(out[0], "!") {
		t.Fatalf("row0 %q", out[0])
	}
	if !strings.Contains(out[2], "?") {
		t.Fatalf("row2 %q", out[2])
	}
}

func TestScreen_InsertDeleteLines(t *testing.T) {
	s := NewScreen(4, 3)
	s.Write([]byte("aaa\r\nbbb\r\nccc"))
	s.Write([]byte("\x1b[2;1H\x1b[L")) // insert 1 line at row 2
	if !strings.Contains(s.CaptureVisible(), "aaa\n") {
		t.Fatalf("after insert: %q", s.CaptureVisible())
	}
	s.Write([]byte("\x1b[2M")) // delete 2 lines
	// just ensure no panic
	_ = s.CaptureVisible()
}

func TestScreen_InsertLinesAtBottom(t *testing.T) {
	s := NewScreen(2, 2)
	s.Write([]byte("aa\r\nbb"))
	s.Write([]byte("\x1b[L\x1b[M"))
	_ = s.CaptureVisible()
}

func TestScreen_SGRIgnored(t *testing.T) {
	s := NewScreen(2, 80)
	s.Write([]byte("\x1b[1;31mhello\x1b[0m"))
	if got := s.CaptureVisible(); got != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestScreen_OSCBel(t *testing.T) {
	s := NewScreen(2, 80)
	s.Write([]byte("\x1b]0;Title\x07hi"))
	if got := s.CaptureVisible(); got != "hi" {
		t.Fatalf("got %q", got)
	}
}

func TestScreen_OSCStTerminator(t *testing.T) {
	s := NewScreen(2, 80)
	s.Write([]byte("\x1b]0;Title\x1b\\hi"))
	if got := s.CaptureVisible(); got != "hi" {
		t.Fatalf("got %q", got)
	}
}

func TestScreen_OSCUnterminated(t *testing.T) {
	s := NewScreen(2, 80)
	s.Write([]byte("\x1b]0;abc"))
	// no panic, swallows everything
	_ = s.CaptureVisible()
}

func TestScreen_CharsetDesignation(t *testing.T) {
	s := NewScreen(2, 80)
	s.Write([]byte("\x1b(Bhello"))
	if got := s.CaptureVisible(); got != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestScreen_CharsetDesignationTruncated(t *testing.T) {
	s := NewScreen(2, 80)
	s.Write([]byte("\x1b("))
	_ = s.CaptureVisible()
}

func TestScreen_EscapeUnknown(t *testing.T) {
	s := NewScreen(2, 80)
	s.Write([]byte("\x1bZhi")) // unknown ESC
	if got := s.CaptureVisible(); got != "hi" {
		t.Fatalf("got %q", got)
	}
}

func TestScreen_EscapeAtEnd(t *testing.T) {
	s := NewScreen(2, 80)
	s.Write([]byte("\x1b"))
	_ = s.CaptureVisible()
}

func TestScreen_CSIIncomplete(t *testing.T) {
	s := NewScreen(2, 80)
	s.Write([]byte("\x1b[12"))
	_ = s.CaptureVisible()
}

func TestScreen_CSIPrivateModeIgnored(t *testing.T) {
	s := NewScreen(2, 80)
	s.Write([]byte("\x1b[?25hhi"))
	if got := s.CaptureVisible(); got != "hi" {
		t.Fatalf("got %q", got)
	}
}

func TestScreen_CSIModeAndScrollIgnored(t *testing.T) {
	s := NewScreen(2, 80)
	s.Write([]byte("\x1b[2hhi\x1b[2lho\x1b[1;5rha"))
	_ = s.CaptureVisible()
}

func TestScreen_Tab(t *testing.T) {
	s := NewScreen(2, 80)
	s.Write([]byte("a\tb"))
	if got := s.CaptureVisible(); got != "a       b" {
		t.Fatalf("got %q", got)
	}
}

func TestScreen_TabClampsToLastCol(t *testing.T) {
	s := NewScreen(2, 5)
	s.Write([]byte("ab\t"))
	// cursor should be at col 4 (last)
	s.Write([]byte("X"))
	if got := s.CaptureVisible(); got != "ab  X" {
		t.Fatalf("got %q", got)
	}
}

func TestScreen_Backspace(t *testing.T) {
	s := NewScreen(2, 80)
	s.Write([]byte("abc\b\bd"))
	if got := s.CaptureVisible(); got != "adc" {
		t.Fatalf("got %q", got)
	}
}

func TestScreen_BackspaceAtCol0(t *testing.T) {
	s := NewScreen(2, 5)
	s.Write([]byte("\b\bx"))
	if got := s.CaptureVisible(); got != "x" {
		t.Fatalf("got %q", got)
	}
}

func TestScreen_Bell(t *testing.T) {
	s := NewScreen(2, 5)
	s.Write([]byte("a\ab"))
	if got := s.CaptureVisible(); got != "ab" {
		t.Fatalf("got %q", got)
	}
}

func TestScreen_OtherControlIgnored(t *testing.T) {
	s := NewScreen(2, 5)
	s.Write([]byte{0x01, 'a'}) // SOH then 'a'
	if got := s.CaptureVisible(); got != "a" {
		t.Fatalf("got %q", got)
	}
}

func TestScreen_WrapAround(t *testing.T) {
	s := NewScreen(5, 10)
	s.Write([]byte("0123456789ABCDE"))
	if got := s.CaptureVisible(); got != "0123456789\nABCDE" {
		t.Fatalf("got %q", got)
	}
}

func TestScreen_UTF8(t *testing.T) {
	s := NewScreen(2, 5)
	s.Write([]byte("héllo"))
	if got := s.CaptureVisible(); got != "héllo" {
		t.Fatalf("got %q", got)
	}
}

func TestScreen_InvalidUTF8(t *testing.T) {
	s := NewScreen(2, 5)
	s.Write([]byte{0xff, 'x'}) // invalid leading byte
	out := s.CaptureVisible()
	if !strings.Contains(out, "x") {
		t.Fatalf("got %q", out)
	}
}

func TestScreen_CaptureVisibleEmpty(t *testing.T) {
	s := NewScreen(3, 5)
	if got := s.CaptureVisible(); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestScreen_CaptureNegativeLines(t *testing.T) {
	s := NewScreen(2, 5)
	s.Write([]byte("hello"))
	if got := s.Capture(-1); got != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestScreen_ParseCSIParams(t *testing.T) {
	if got := parseCSIParams(""); got != nil {
		t.Fatalf("empty: %v", got)
	}
	if got := parseCSIParams("?25"); got != nil {
		t.Fatalf("private: %v", got)
	}
	if got := parseCSIParams("1;2;33"); len(got) != 3 || got[0] != 1 || got[2] != 33 {
		t.Fatalf("multi: %v", got)
	}
	if got := parseCSIParams(""); got != nil {
		t.Fatalf("nil")
	}
	// Non-digit chars in a part are skipped (defensive).
	if got := parseCSIParams("1a;2"); got[0] != 1 || got[1] != 2 {
		t.Fatalf("bad %v", got)
	}
}

func TestScreen_ParamDefault(t *testing.T) {
	if paramDefault(nil, 0, 9) != 9 {
		t.Fatal("nil")
	}
	if paramDefault([]int{0}, 0, 7) != 7 {
		t.Fatal("zero")
	}
	if paramDefault([]int{3}, 0, 7) != 3 {
		t.Fatal("three")
	}
}

func TestScreen_Clamp(t *testing.T) {
	if clamp(-1, 0, 5) != 0 || clamp(10, 0, 5) != 5 || clamp(3, 0, 5) != 3 {
		t.Fatal("clamp")
	}
}
