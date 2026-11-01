package cli

import (
	"errors"
	"golang.org/x/sys/windows"
	"testing"
)

func TestQuickEditDisabledAndConsoleRestored(t *testing.T) {
	original := uint32(windows.ENABLE_QUICK_EDIT_MODE | windows.ENABLE_LINE_INPUT | windows.ENABLE_ECHO_INPUT | windows.ENABLE_PROCESSED_INPUT)
	current := original
	restore := disableQuickEdit(1, func(_ windows.Handle, p *uint32) error { *p = current; return nil }, func(_ windows.Handle, m uint32) error { current = m; return nil })
	if current&windows.ENABLE_QUICK_EDIT_MODE != 0 || current&windows.ENABLE_EXTENDED_FLAGS == 0 {
		t.Fatal("QuickEdit remains enabled")
	}
	preserved := uint32(windows.ENABLE_LINE_INPUT | windows.ENABLE_ECHO_INPUT | windows.ENABLE_PROCESSED_INPUT)
	if current&preserved != original&preserved {
		t.Fatal("prompt or Ctrl+C modes changed")
	}
	restore()
	if current != original {
		t.Fatal("console mode not restored")
	}
}
func TestQuickEditPipeDoesNotChangeMode(t *testing.T) {
	restore := disableQuickEdit(1, func(windows.Handle, *uint32) error { return errors.New("not console") }, func(windows.Handle, uint32) error { t.Fatal("changed pipe mode"); return nil })
	restore()
}
