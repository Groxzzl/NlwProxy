package console

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestDecodeKeySupportsWindowsAndANSISequences(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want Key
		n    int
	}{
		{"ansi up", []byte("\x1b[A"), KeyUp, 3},
		{"ansi down", []byte("\x1b[B"), KeyDown, 3},
		{"windows up", []byte{0, 72}, KeyUp, 2},
		{"windows down", []byte{224, 80}, KeyDown, 2},
		{"enter cr", []byte{'\r'}, KeyEnter, 1},
		{"enter lf", []byte{'\n'}, KeyEnter, 1},
		{"escape", []byte{27}, KeyEscape, 1},
		{"letter", []byte{'n'}, KeyRune, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, n, ok := DecodeKey(tc.data)
			if !ok || got.Kind != tc.want || n != tc.n {
				t.Fatalf("DecodeKey(%v)=(%+v,%d,%v), want kind=%v n=%d", tc.data, got, n, ok, tc.want, tc.n)
			}
		})
	}
	if _, _, ok := DecodeKey([]byte{'\x1b', '['}); ok {
		t.Fatal("partial ANSI sequence decoded before complete")
	}
}

func TestSelectorNavigationWrapsAndTransitions(t *testing.T) {
	s := NewProfileSelector([]Profile{{Name: "alpha", Detail: "direct", Enabled: true}, {Name: "beta", Detail: "socks5", Enabled: false}})
	if s.Selected() != 0 {
		t.Fatal("initial selection must be first row")
	}
	if action := s.Apply(KeyEvent{Kind: KeyUp}); action != SelectorNone || s.Selected() != 1 {
		t.Fatalf("up action=%v selected=%d", action, s.Selected())
	}
	if action := s.Apply(KeyEvent{Kind: KeyDown}); action != SelectorNone || s.Selected() != 0 {
		t.Fatalf("down action=%v selected=%d", action, s.Selected())
	}
	for key, want := range map[rune]SelectorAction{'n': SelectorNew, 'e': SelectorEdit, 'd': SelectorDelete, 'q': SelectorQuit} {
		if got := s.Apply(KeyEvent{Kind: KeyRune, Rune: key}); got != want {
			t.Fatalf("key=%q action=%v want=%v", key, got, want)
		}
	}
	if got := s.Apply(KeyEvent{Kind: KeyEnter}); got != SelectorSelect {
		t.Fatalf("enter action=%v", got)
	}
	if got := s.Apply(KeyEvent{Kind: KeyEscape}); got != SelectorQuit {
		t.Fatalf("escape action=%v", got)
	}
}

func TestConfirmationRequiresExplicitYes(t *testing.T) {
	if !ConfirmKey(KeyEvent{Kind: KeyRune, Rune: 'y'}) || !ConfirmKey(KeyEvent{Kind: KeyRune, Rune: 'Y'}) {
		t.Fatal("Y must confirm")
	}
	for _, key := range []KeyEvent{{Kind: KeyRune, Rune: 'n'}, {Kind: KeyEnter}, {Kind: KeyEscape}} {
		if ConfirmKey(key) {
			t.Fatalf("key %+v confirmed", key)
		}
	}
}

func TestRenderProfileSelectorUsesThemeHighlightAndResponsiveLayout(t *testing.T) {
	s := NewProfileSelector([]Profile{{Name: "alpha", Detail: "direct · enabled", Enabled: true}, {Name: "beta", Detail: "socks5 · disabled"}})
	s.Apply(KeyEvent{Kind: KeyDown})
	wide := RenderProfileSelector(s, 100, true)
	for _, want := range []string{"\x1b[", "SELECT PROFILE", "alpha", "beta", "N New", "E Edit", "D Delete", "Q Quit", themeSelectedBG} {
		if !strings.Contains(wide, want) {
			t.Fatalf("wide render missing %q:\n%s", want, wide)
		}
	}
	narrow := RenderProfileSelector(s, 34, false)
	if strings.Contains(narrow, "\x1b[") || !strings.Contains(narrow, "↑/↓") || !strings.Contains(narrow, "> beta") {
		t.Fatalf("bad narrow fallback:\n%s", narrow)
	}
	for _, line := range strings.Split(strings.TrimSuffix(narrow, "\n"), "\n") {
		if len([]rune(line)) > 34 {
			t.Fatalf("line exceeds width: %q", line)
		}
	}
}

func TestTerminalSequencesAndPlainFallback(t *testing.T) {
	if got := EnterScreen(true); got != "\x1b[?1049h\x1b[?25l" {
		t.Fatalf("enter=%q", got)
	}
	if got := LeaveScreen(true); got != "\x1b[0m\x1b[?25h\x1b[?1049l" {
		t.Fatalf("leave=%q", got)
	}
	if EnterScreen(false) != "" || LeaveScreen(false) != "" || ClearFrame(false) != "" {
		t.Fatal("redirected output must not receive control sequences")
	}
}

func TestPlainSelectorProcessesNamedKeysWithoutANSI(t *testing.T) {
	selector := NewProfileSelector([]Profile{{Name: "alpha"}, {Name: "beta"}})
	var out bytes.Buffer
	selected := ""
	err := runPlainSelector(strings.NewReader("down\nenter\nq\n"), &out, selector, SelectorHandlers{
		Select: func(profile Profile) error { selected = profile.Name; return nil },
	})
	if err != nil || selected != "beta" {
		t.Fatalf("err=%v selected=%q", err, selected)
	}
	if strings.Contains(out.String(), "\x1b[") {
		t.Fatalf("redirected output contains ANSI: %q", out.String())
	}
}

func TestDeleteActionUsesConfirmation(t *testing.T) {
	selector := NewProfileSelector([]Profile{{Name: "alpha"}})
	deleted := false
	handlers := SelectorHandlers{
		Confirm: func(profile Profile) (bool, error) { return profile.Name == "alpha", nil },
		Delete:  func(Profile) error { deleted = true; return nil },
	}
	quit, err := handleSelectorAction(io.Discard, selector, SelectorDelete, handlers, KeyEvent{}, false)
	if quit || err != nil || !deleted {
		t.Fatalf("quit=%v err=%v deleted=%v", quit, err, deleted)
	}
}
