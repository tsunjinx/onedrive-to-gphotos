package migration

import (
	"context"
	"errors"
	"fmt"
	"os"

	"od2gp/internal/staging"
)

func (e *Engine) download(ctx context.Context, meta *Meta, id int64, item *Item, name string, key []byte) error {
	current, err := e.Source.Stat(ctx, item.Path)
	if err != nil {
		return err
	}
	if !sameVersion(current, item.Source) {
		return errors.New("source file changed since planning; refusing to combine versions")
	}
	item.State = "downloading"
	if err := e.Store.Save(id, item.State, item); err != nil {
		return err
	}
	writer, offset, err := staging.Resume(ctx, name, key, item.Source.Size)
	if err != nil {
		return err
	}
	defer func() {
		if writer != nil {
			writer.Abort()
		}
	}()
	info, err := os.Stat(name)
	if err != nil {
		return err
	}
	free, err := staging.Free(e.Directory)
	if err != nil {
		return errors.New("cannot check available cache disk space")
	}
	if free < uint64(staging.Required(item.Source.Size)-info.Size()+(128<<20)) {
		return errors.New("insufficient free disk space for this file and journal reserve")
	}
	if offset > 0 {
		fmt.Fprintf(e.Progress, "  [%d] Resuming verified source download at %d / %d bytes\n", id, offset, item.Source.Size)
	}
	if offset < item.Source.Size {
		err = e.Source.Read(ctx, item.Path, offset, writer)
		var noRange interface{ RestartDownload() bool }
		if offset > 0 && errors.As(err, &noRange) && noRange.RestartDownload() {
			writer.Abort()
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := os.Remove(name); err != nil {
				return err
			}
			writer, err = staging.Create(name, key, item.Source.Size)
			if err != nil {
				return err
			}
			fmt.Fprintf(e.Progress, "  [%d] Source ignored the range; restarting this file safely.\n", id)
			err = e.Source.Read(ctx, item.Path, 0, writer)
		}
		if err != nil {
			return err
		}
	}
	hash, err := writer.Finish()
	if err != nil {
		return err
	}
	current, err = e.Source.Stat(ctx, item.Path)
	if err != nil {
		return err
	}
	if !sameVersion(current, item.Source) {
		return errors.New("source changed during download; encrypted staging is not trusted")
	}
	item.Hash = hash
	item.State = "staged"
	item.Reason = ""
	return e.Store.Save(id, item.State, item)
}
