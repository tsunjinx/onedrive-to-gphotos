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
