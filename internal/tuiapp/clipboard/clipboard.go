// Package clipboard provides an injectable Windows clip.exe abstraction.
package clipboard

import (
	"fmt"
	"os/exec"
	"strings"
)

type Writer interface{ WriteText(string) error }
type WriterFunc func(string) error

func (f WriterFunc) WriteText(s string) error { return f(s) }

type ClipEXE struct{ Command string }

func (c ClipEXE) WriteText(text string) error {
	name := c.Command
	if name == "" {
		name = "clip.exe"
	}
	cmd := exec.Command(name)
	cmd.Stdin = strings.NewReader(text)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}
