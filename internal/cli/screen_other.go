//go:build !darwin && !windows

package cli

import "os"

func clearConsole(*os.File) {}

func terminalInput(*os.File) bool { return false }

func enableProgressOutput(*os.File) (func(), bool) { return func() {}, false }

func progressColumns(*os.File) int { return 80 }
