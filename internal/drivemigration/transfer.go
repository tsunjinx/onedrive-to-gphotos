package drivemigration

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"od2gp/internal/googledrive"
	"od2gp/internal/staging"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

func (e *Engine) stagePath(id int64) string {
	return filepath.Join(e.Directory, "stage-"+strconv.FormatInt(id, 10)+".bin")
}
func (e *Engine) stageKey(id int64) []byte {
	h := hmac.New(sha256.New, e.Store.Key)
	fmt.Fprintf(h, "od2gp-drive-stage-%d", id)
	return h.Sum(nil)
}
func (e *Engine) save(id int64, n *Node) error { return e.Store.Save(id, n.State, n) }
func (e *Engine) metadata(n *Node) googledrive.File {
	mime := n.Source.MimeType
	if mime == "" || strings.HasPrefix(mime, "application/vnd.google-apps.") {
		mime = "application/octet-stream"
	}
	if n.Source.IsDir {
		mime = googledrive.Folder
	}
	return googledrive.File{ID: n.TargetID, Name: n.Source.Name, MIME: mime, Parents: []string{n.ParentTarget}, Modified: n.Source.ModTime, Properties: map[string]string{"od2gp_job": e.Meta.ID, "onedrive_id": n.Source.ID}}
}
func (e *Engine) verify(n *Node, f googledrive.File) error {
	if f.ID != n.TargetID || f.Name != n.Source.Name || f.Trashed || len(f.Parents) != 1 || f.Parents[0] != n.ParentTarget || f.Properties["od2gp_job"] != e.Meta.ID || f.Properties["onedrive_id"] != n.Source.ID {
		return errors.New("destination identity/name/parent conflict; intervention required")
	}
	if n.Source.IsDir {
		if f.MIME != googledrive.Folder {
			return errors.New("destination is not a folder")
		}
		n.Verification = "folder_tree"
		return nil
	}
	if f.Size != n.Source.Size {
		return errors.New("destination size mismatch; intervention required")
	}
	if n.Hash == "" {
		return errors.New("local content hash unavailable; intervention required")
	}
	n.Verification = "metadata_only"
	if f.SHA256 != "" {
		if !strings.EqualFold(f.SHA256, n.Hash) {
			return errors.New("destination checksum mismatch; intervention required")
		}
		n.Verification = "sha256"
	}
	return nil
}
func (e *Engine) transfer(ctx context.Context, id int64, n *Node) error {
	if err := e.gate(ctx); err != nil {
		return err
	}
	if n.TargetID == "" {
		target, err := e.Destination.GenerateID(ctx)
		if err != nil {
			return err
		}
		n.TargetID = target
		if err = e.save(id, n); err != nil {
			return err
		}
	}
	f, err := e.Destination.Get(ctx, n.TargetID)
	if err == nil {
		if err = e.verify(n, f); err != nil {
			return err
		}
		return e.complete(id, n)
	}
	var api *googledrive.Error
	if !errors.As(err, &api) || api.Code != 404 {
		return err
	}
	if n.Source.IsDir {
		f, err = e.Destination.CreateFolder(ctx, e.metadata(n))
		if err != nil {
			return err
		}
		if err = e.verify(n, f); err != nil {
			return err
		}
		return e.complete(id, n)
	}
	sourcePath := path.Join(e.Meta.Folder, n.Source.Path)
	fresh, err := e.Source.Stat(ctx, sourcePath)
	if err != nil {
		return err
	}
	if !Same(fresh, n.Source) {
		return errors.New("OneDrive source version changed; replan this item before transfer")
	}
	if n.Hash == "" {
		n.State = "downloading"
		if err = e.save(id, n); err != nil {
			return err
		}
		w, offset, err := staging.Resume(ctx, e.stagePath(id), e.stageKey(id), n.Source.Size)
		if err != nil {
			return err
		}
		if offset < n.Source.Size {
			if err = e.Source.Read(ctx, sourcePath, offset, w); err != nil {
				w.Abort()
				return err
			}
		}
		n.Hash, err = w.Finish()
		if err != nil {
			return err
		}
		fresh, err = e.Source.Stat(ctx, sourcePath)
		if err != nil {
			return err
		}
		if !Same(fresh, n.Source) {
			return errors.New("OneDrive source changed during download; intervention required")
		}
		n.State = "staged"
		if err = e.save(id, n); err != nil {
			return err
		}
	}
	reader, err := staging.Open(e.stagePath(id), e.stageKey(id), n.Source.Size)
	if err != nil {
		return err
	}
	defer reader.Close()
	if n.Session != "" {
		off, final, err := e.Destination.Query(ctx, n.Session, n.Source.Size)
		if err == nil {
			if final != nil {
				if err = e.verify(n, *final); err != nil {
					return err
				}
				reader.Close()
				return e.complete(id, n)
			}
			if off < 0 || off > n.Source.Size {
				return errors.New("invalid server upload offset")
			}
			n.Offset = off
		} else if errors.As(err, &api) && (api.Code == 404 || api.Code == 410) {
			n.Session = ""
			n.Offset = 0
		} else {
			return err
		}
	}
	if n.Session == "" {
		session, err := e.Destination.Start(ctx, e.metadata(n), n.Source.Size)
		if err != nil {
			return err
		}
		n.Session = session
		n.Offset = 0
	}
	n.State = "uploading"
	if err = e.save(id, n); err != nil {
		return err
	}
	buffer := make([]byte, 4<<20)
	for n.Offset < n.Source.Size {
		if err = e.gate(ctx); err != nil {
			return err
		}
		length := min(int64(len(buffer)), n.Source.Size-n.Offset)
		if _, err = reader.ReadAt(buffer[:length], n.Offset); err != nil {
			return err
		}
		next, final, err := e.Destination.Chunk(ctx, n.Session, n.Offset, n.Source.Size, buffer[:length])
		if err != nil {
			return err
		}
		if final != nil {
			if err = e.verify(n, *final); err != nil {
				return err
			}
			reader.Close()
			return e.complete(id, n)
		}
		if next <= n.Offset || next > n.Source.Size {
			return errors.New("Drive upload did not acknowledge a valid chunk")
		}
		n.Offset = next
		if err = e.save(id, n); err != nil {
			return err
		}
	}
	return errors.New("upload ended without confirmed destination file")
}
func (e *Engine) complete(id int64, n *Node) error {
	n.State = "completed"
	n.Reason = ""
	n.Session = ""
	if err := e.save(id, n); err != nil {
		return err
	}
	if err := os.Remove(e.stagePath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("completed file cache cleanup failed: %w", err)
	}
	return nil
}

// Verify rechecks cloud identities and checksums. It does not mark missing items complete.
func (e *Engine) Verify(ctx context.Context) error {
	ids, err := e.Store.IDs()
	if err != nil {
		return err
	}
	for _, id := range ids {
		var n Node
		if err = e.Store.Load(id, &n); err != nil {
			return err
		}
		if n.State == "skipped" {
			continue
		}
		if n.State != "completed" {
			return errors.New("job still contains unresolved items")
		}
		f, err := e.Destination.Get(ctx, n.TargetID)
		if err != nil {
			return err
		}
		if err = e.verify(&n, f); err != nil {
			return fmt.Errorf("verification failed for %q: %w", n.Source.Path, err)
		}
		if !n.Source.IsDir {
			source, err := e.Source.Stat(ctx, path.Join(e.Meta.Folder, n.Source.Path))
			if err != nil {
				return err
			}
			if !Same(source, n.Source) {
				return fmt.Errorf("source changed after transfer: %q", n.Source.Path)
			}
		}
	}
	return nil
}
