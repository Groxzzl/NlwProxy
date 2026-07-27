//go:build !windows

package console

import (
	"io"
	"os"
)

func isTerminalFile(file *os.File) bool                           { return false }
func terminalSize(file *os.File) (int, int)                       { return 80, 24 }
func withRawInput(file *os.File, run func(io.Reader) error) error { return run(file) }
func enableANSI(file *os.File) bool                               { return false }
