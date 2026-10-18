//go:build !darwin && !windows

package staging

import "errors"

func Free(string) (uint64, error) { return 0, errors.New("disk space check unsupported on this OS") }
