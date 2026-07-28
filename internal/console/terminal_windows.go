//go:build windows

package console

import (
	"errors"
	"io"
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode       = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode       = kernel32.NewProc("SetConsoleMode")
	procGetConsoleScreenInfo = kernel32.NewProc("GetConsoleScreenBufferInfo")
)

const (
	enableProcessedInput         = 0x0001
	enableLineInput              = 0x0002
	enableEchoInput              = 0x0004
	enableVirtualTerminalInput   = 0x0200
	enableVirtualTerminalProcess = 0x0004
	disableNewlineAutoReturn     = 0x0008
)

type consoleScreenBufferInfo struct {
	Size              struct{ X, Y int16 }
	CursorPosition    struct{ X, Y int16 }
	Attributes        uint16
	Window            struct{ Left, Top, Right, Bottom int16 }
	MaximumWindowSize struct{ X, Y int16 }
}

func consoleMode(file *os.File) (uint32, bool) {
	var mode uint32
	r, _, _ := procGetConsoleMode.Call(file.Fd(), uintptr(unsafe.Pointer(&mode)))
	return mode, r != 0
}

func isTerminalFile(file *os.File) bool {
	if file == nil {
		return false
	}
	_, ok := consoleMode(file)
	return ok
}

func terminalSize(file *os.File) (int, int) {
	if file == nil {
		return 80, 24
	}
	var info consoleScreenBufferInfo
	r, _, _ := procGetConsoleScreenInfo.Call(file.Fd(), uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return 80, 24
	}
	return int(info.Window.Right-info.Window.Left) + 1, int(info.Window.Bottom-info.Window.Top) + 1
}

// withRawInput disables line buffering and echo, restoring the exact mode on return.
func withRawInput(file *os.File, run func(io.Reader) error) error {
	mode, ok := consoleMode(file)
	if !ok {
		return run(file)
	}
	raw := mode &^ (enableLineInput | enableEchoInput)
	raw |= enableProcessedInput | enableVirtualTerminalInput
	if r, _, e := procSetConsoleMode.Call(file.Fd(), uintptr(raw)); r == 0 {
		return errors.New("enable raw console input: " + e.Error())
	}
	defer procSetConsoleMode.Call(file.Fd(), uintptr(mode))
	return run(file)
}

func enableANSI(file *os.File) bool {
	mode, ok := consoleMode(file)
	if !ok {
		return false
	}
	mode |= enableVirtualTerminalProcess
	// Keep normal CR/LF semantics. DISABLE_NEWLINE_AUTO_RETURN causes LF to
	// preserve the current column in some Windows hosts, producing diagonal UI.
	mode &^= disableNewlineAutoReturn
	r, _, _ := procSetConsoleMode.Call(file.Fd(), uintptr(mode))
	return r != 0
}
