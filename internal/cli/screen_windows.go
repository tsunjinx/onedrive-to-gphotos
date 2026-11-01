package cli

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
)

func clearConsole(file *os.File) {
	handle := windows.Handle(file.Fd())
	var mode uint32
	if windows.GetConsoleMode(handle, &mode) != nil {
		return
	}
	if windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) != nil {
		return
	}
	defer windows.SetConsoleMode(handle, mode)
	fmt.Fprint(file, "\x1b[2J\x1b[H")
}

func terminalInput(file *os.File) bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(file.Fd()), &mode) == nil
}

func enableProgressOutput(file *os.File) (func(), bool) {
	h := windows.Handle(file.Fd())
	var mode uint32
	if windows.GetConsoleMode(h, &mode) != nil {
		return func() {}, false
	}
	if windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) != nil {
		return func() {}, false
	}
	return func() { _ = windows.SetConsoleMode(h, mode) }, true
}

func progressColumns(file *os.File) int {
	var info windows.ConsoleScreenBufferInfo
	if windows.GetConsoleScreenBufferInfo(windows.Handle(file.Fd()), &info) == nil {
		return int(info.Window.Right - info.Window.Left + 1)
	}
	return 80
}
