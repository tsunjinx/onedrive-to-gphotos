package cli

import (
	"context"
	"io"
)

type menuByte struct {
	value byte
	err   error
}

type menuInput struct {
	ctx     context.Context
	updates <-chan menuByte
}

// One bounded input pump supports cancellation during any nested command's
// prompt. A blocked source read lasts until stdin closes or the CLI exits.
func newMenuInput(ctx context.Context, input io.Reader) io.Reader {
	updates := make(chan menuByte, 128)
	go func() {
		defer close(updates)
		var buffer [256]byte
		for {
			if ctx.Err() != nil {
				return
			}
			n, err := input.Read(buffer[:])
			for _, value := range buffer[:n] {
				select {
				case updates <- menuByte{value: value}:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				select {
				case updates <- menuByte{err: err}:
				case <-ctx.Done():
				}
				return
			}
		}
	}()
	return menuInput{ctx: ctx, updates: updates}
}

func (r menuInput) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	case update, ok := <-r.updates:
		if !ok {
			return 0, io.EOF
		}
		if update.err != nil {
			return 0, update.err
		}
		p[0] = update.value
		return 1, nil
	}
}
