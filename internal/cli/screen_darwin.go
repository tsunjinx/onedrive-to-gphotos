package cli

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
)

func clearConsole(file *os.File) {
	if _, err := unix.IoctlGetTermios(int(file.Fd()), unix.TIOCGETA); err != nil {
		return
	}
	fmt.Fprint(file, "\x1b[2J\x1b[H")
}

func terminalInput(file *os.File) bool {
	_, err := unix.IoctlGetTermios(int(file.Fd()), unix.TIOCGETA)
	return err == nil
}
