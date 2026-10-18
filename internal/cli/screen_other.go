//go:build !darwin && !windows

package cli

import "os"

func clearConsole(*os.File) {}

func terminalInput(*os.File) bool { return false }
