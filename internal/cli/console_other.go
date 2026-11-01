//go:build !windows

package cli

import "io"

func prepareTransferConsole(io.Reader) func() { return func() {} }
