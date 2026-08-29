//go:build darwin || linux

package value

import (
	"syscall"
	"unsafe"
)

// terminalCols returns the visible column count of the controlling terminal
// for os.Stdout, or 0 when stdout is not a TTY (piped to a file, under go test,
// CI without a pty, etc.). Used by TapeDiagram to pick a row width that fits
// the screen instead of always wrapping at 32 cells.
func terminalCols() int {
	var ws struct {
		Row, Col       uint16
		Xpixel, Ypixel uint16
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
		uintptr(syscall.Stdout),
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(&ws)))
	if errno != 0 || ws.Col == 0 {
		return 0
	}
	return int(ws.Col)
}
