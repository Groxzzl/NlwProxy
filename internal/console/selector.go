package console

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

// Key identifies terminal input independently of the host console encoding.
type Key uint8

const (
	KeyUnknown Key = iota
	KeyUp
	KeyDown
	KeyEnter
	KeyEscape
	KeyRune
)

type KeyEvent struct {
	Kind Key
	Rune rune
}

// DecodeKey recognizes ANSI/VT and Windows console byte sequences.
func DecodeKey(data []byte) (KeyEvent, int, bool) {
	if len(data) == 0 {
		return KeyEvent{}, 0, false
	}
	switch data[0] {
	case 0, 0xe0:
		if len(data) < 2 {
			return KeyEvent{}, 0, false
		}
		switch data[1] {
		case 72:
			return KeyEvent{Kind: KeyUp}, 2, true
		case 80:
			return KeyEvent{Kind: KeyDown}, 2, true
		default:
			return KeyEvent{Kind: KeyUnknown}, 2, true
		}
	case 27:
		if len(data) == 1 {
			return KeyEvent{Kind: KeyEscape}, 1, true
		}
		if data[1] == '[' || data[1] == 'O' {
			if len(data) < 3 {
				return KeyEvent{}, 0, false
			}
			switch data[2] {
			case 'A':
				return KeyEvent{Kind: KeyUp}, 3, true
			case 'B':
				return KeyEvent{Kind: KeyDown}, 3, true
			}
		}
		return KeyEvent{Kind: KeyEscape}, 1, true
	case '\r', '\n':
		return KeyEvent{Kind: KeyEnter}, 1, true
	}
	r, n := utf8.DecodeRune(data)
	if r == utf8.RuneError && n == 1 && !utf8.FullRune(data) {
		return KeyEvent{}, 0, false
	}
	return KeyEvent{Kind: KeyRune, Rune: r}, n, true
}

type Profile struct {
	Name    string
	Detail  string
	Enabled bool
}

type SelectorAction uint8

const (
	SelectorNone SelectorAction = iota
	SelectorSelect
	SelectorNew
	SelectorEdit
	SelectorDelete
	SelectorQuit
)

type ProfileSelector struct {
	profiles []Profile
	selected int
}

func NewProfileSelector(profiles []Profile) *ProfileSelector {
	return &ProfileSelector{profiles: append([]Profile(nil), profiles...)}
}

func (s *ProfileSelector) Profiles() []Profile { return append([]Profile(nil), s.profiles...) }
func (s *ProfileSelector) Selected() int       { return s.selected }
func (s *ProfileSelector) Current() (Profile, bool) {
	if len(s.profiles) == 0 {
		return Profile{}, false
	}
	return s.profiles[s.selected], true
}

func (s *ProfileSelector) Apply(key KeyEvent) SelectorAction {
	switch key.Kind {
	case KeyUp:
		if len(s.profiles) > 0 {
			s.selected = (s.selected - 1 + len(s.profiles)) % len(s.profiles)
		}
	case KeyDown:
		if len(s.profiles) > 0 {
			s.selected = (s.selected + 1) % len(s.profiles)
		}
	case KeyEnter:
		if len(s.profiles) > 0 {
			return SelectorSelect
		}
	case KeyEscape:
		return SelectorQuit
	case KeyRune:
		switch strings.ToUpper(string(key.Rune)) {
		case "N":
			return SelectorNew
		case "E":
			if len(s.profiles) > 0 {
				return SelectorEdit
			}
		case "D":
			if len(s.profiles) > 0 {
				return SelectorDelete
			}
		case "Q":
			return SelectorQuit
		}
	}
	return SelectorNone
}

func ConfirmKey(key KeyEvent) bool {
	return key.Kind == KeyRune && (key.Rune == 'y' || key.Rune == 'Y')
}

const (
	themeReset      = "\x1b[0m"
	themeBold       = "\x1b[1m"
	themeMuted      = "\x1b[38;5;244m"
	themePrimary    = "\x1b[38;5;81m"
	themeAccent     = "\x1b[38;5;213m"
	themeSuccess    = "\x1b[38;5;84m"
	themeDanger     = "\x1b[38;5;203m"
	themeSelectedBG = "\x1b[48;5;24m"
	themeSelectedFG = "\x1b[38;5;231m"
)

func EnterScreen(interactive bool) string {
	if !interactive {
		return ""
	}
	return "\x1b[?1049h\x1b[?25l"
}

func LeaveScreen(interactive bool) string {
	if !interactive {
		return ""
	}
	return "\x1b[0m\x1b[?25h\x1b[?1049l"
}

func ClearFrame(interactive bool) string {
	if !interactive {
		return ""
	}
	return "\x1b[2J\x1b[H"
}

func RenderProfileSelector(s *ProfileSelector, width int, color bool) string {
	if width < 24 {
		width = 24
	}
	if width > 96 {
		width = 96
	}
	paint := func(code, text string) string {
		if !color {
			return text
		}
		return code + text + themeReset
	}
	fit := func(text string, size int) string {
		if size < 1 {
			return ""
		}
		r := []rune(text)
		if len(r) > size {
			if size == 1 {
				return "…"
			}
			return string(r[:size-1]) + "…"
		}
		return text + strings.Repeat(" ", size-len(r))
	}
	inner := width - 4
	var b strings.Builder
	border := strings.Repeat("─", width-2)
	fmt.Fprintf(&b, "%s\n", paint(themeMuted, "┌"+border+"┐"))
	title := fit("NLWPROXY  /  SELECT PROFILE", inner)
	fmt.Fprintf(&b, "%s %s %s\n", paint(themeMuted, "│"), paint(themeBold+themePrimary, title), paint(themeMuted, "│"))
	fmt.Fprintf(&b, "%s\n", paint(themeMuted, "├"+border+"┤"))
	if len(s.profiles) == 0 {
		row := fit("  No profiles configured. Press N to create one.", inner)
		fmt.Fprintf(&b, "%s %s %s\n", paint(themeMuted, "│"), paint(themeMuted, row), paint(themeMuted, "│"))
	}
	for i, profile := range s.profiles {
		marker := "  "
		if i == s.selected {
			marker = "> "
		}
		status := "disabled"
		if profile.Enabled {
			status = "enabled"
		}
		content := marker + profile.Name
		if width >= 48 {
			detail := profile.Detail
			if detail == "" {
				detail = status
			}
			content += "  ·  " + detail
		}
		row := fit(content, inner)
		if i == s.selected && color {
			row = themeSelectedBG + themeSelectedFG + themeBold + row + themeReset
		} else if profile.Enabled {
			row = paint(themeSuccess, row)
		} else {
			row = paint(themeMuted, row)
		}
		fmt.Fprintf(&b, "%s %s %s\n", paint(themeMuted, "│"), row, paint(themeMuted, "│"))
	}
	fmt.Fprintf(&b, "%s\n", paint(themeMuted, "├"+border+"┤"))
	help := "↑/↓ Move  Enter Select"
	if width >= 44 {
		help += "  N New  E Edit  D Delete  Q Quit"
	} else {
		help += "  N/E/D/Q"
	}
	fmt.Fprintf(&b, "%s %s %s\n", paint(themeMuted, "│"), paint(themeAccent, fit(help, inner)), paint(themeMuted, "│"))
	fmt.Fprintf(&b, "%s\n", paint(themeMuted, "└"+border+"┘"))
	return b.String()
}

func RenderConfirmation(prompt string, color bool, width int) string {
	if width < 24 {
		width = 24
	}
	text := prompt + " [y/N]"
	if len([]rune(text)) > width {
		text = string([]rune(text)[:width-1]) + "…"
	}
	if color {
		return themeDanger + themeBold + text + themeReset
	}
	return text
}

type SelectorHandlers struct {
	Select  func(Profile) error
	New     func() error
	Edit    func(Profile) error
	Delete  func(Profile) error
	Confirm func(Profile) (bool, error)
}

// RunProfileSelector owns terminal setup and guarantees cursor/screen restoration.
// Redirected stdin/stdout uses a stable line-oriented fallback without ANSI.
func RunProfileSelector(in *os.File, out *os.File, profiles []Profile, handlers SelectorHandlers) error {
	interactive := isTerminalFile(in) && isTerminalFile(out) && enableANSI(out)
	selector := NewProfileSelector(profiles)
	if !interactive {
		return runPlainSelector(in, out, selector, handlers)
	}
	if _, err := io.WriteString(out, EnterScreen(true)); err != nil {
		return err
	}
	defer io.WriteString(out, LeaveScreen(true))
	draw := func() error {
		width, _ := terminalSize(out)
		_, err := io.WriteString(out, ClearFrame(true)+RenderProfileSelector(selector, width, true))
		return err
	}
	return withRawInput(in, func(reader io.Reader) error {
		if err := draw(); err != nil {
			return err
		}
		buf := make([]byte, 0, 8)
		one := make([]byte, 1)
		for {
			n, err := reader.Read(one)
			if n > 0 {
				buf = append(buf, one[0])
				for len(buf) > 0 {
					key, used, ok := DecodeKey(buf)
					if !ok {
						break
					}
					buf = buf[used:]
					quit, actionErr := handleSelectorAction(out, selector, selector.Apply(key), handlers, key, true)
					if actionErr != nil || quit {
						return actionErr
					}
					if err := draw(); err != nil {
						return err
					}
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					return nil
				}
				return err
			}
		}
	})
}

func handleSelectorAction(out io.Writer, selector *ProfileSelector, action SelectorAction, handlers SelectorHandlers, key KeyEvent, color bool) (bool, error) {
	current, hasCurrent := selector.Current()
	switch action {
	case SelectorQuit:
		return true, nil
	case SelectorSelect:
		if handlers.Select != nil && hasCurrent {
			return false, handlers.Select(current)
		}
	case SelectorNew:
		if handlers.New != nil {
			return false, handlers.New()
		}
	case SelectorEdit:
		if handlers.Edit != nil && hasCurrent {
			return false, handlers.Edit(current)
		}
	case SelectorDelete:
		if handlers.Delete == nil || !hasCurrent {
			return false, nil
		}
		confirmed := false
		var err error
		if handlers.Confirm != nil {
			confirmed, err = handlers.Confirm(current)
		} else {
			confirmed = ConfirmKey(key)
		}
		if err != nil || !confirmed {
			return false, err
		}
		return false, handlers.Delete(current)
	}
	return false, nil
}

func runPlainSelector(in io.Reader, out io.Writer, selector *ProfileSelector, handlers SelectorHandlers) error {
	if _, err := io.WriteString(out, RenderProfileSelector(selector, 80, false)); err != nil {
		return err
	}
	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			line = "q"
		}
		key := KeyEvent{Kind: KeyRune, Rune: []rune(line)[0]}
		if strings.EqualFold(line, "up") {
			key = KeyEvent{Kind: KeyUp}
		} else if strings.EqualFold(line, "down") {
			key = KeyEvent{Kind: KeyDown}
		} else if strings.EqualFold(line, "enter") {
			key = KeyEvent{Kind: KeyEnter}
		}
		quit, err := handleSelectorAction(out, selector, selector.Apply(key), handlers, key, false)
		if err != nil || quit {
			return err
		}
		if _, err := io.WriteString(out, RenderProfileSelector(selector, 80, false)); err != nil {
			return err
		}
	}
	return scanner.Err()
}
