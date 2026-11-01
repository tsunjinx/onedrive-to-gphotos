package cli

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// Classic console selection can block progress writes and hence transfer workers.
// Change only this console's input mode; retain line input, echo and Ctrl+C.
func prepareTransferConsole(input io.Reader) func() {
	file, ok := input.(*os.File)
	if !ok {
		return func() {}
	}
	return disableQuickEdit(windows.Handle(file.Fd()), windows.GetConsoleMode, windows.SetConsoleMode)
}
func disableQuickEdit(handle windows.Handle, get func(windows.Handle, *uint32) error, set func(windows.Handle, uint32) error) func() {
	var original uint32
	if get(handle, &original) != nil {
		return func() {}
	}
	mode := (original | windows.ENABLE_EXTENDED_FLAGS) &^ windows.ENABLE_QUICK_EDIT_MODE
	if mode == original {
		return func() {}
	}
	if set(handle, mode) != nil {
		return func() {}
	}
	return func() { _ = set(handle, original) }
}
