package migration

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sync"
	"time"

	"od2gp/internal/googlephotos"
	"od2gp/internal/journal"
	"od2gp/internal/onedrive"
	"od2gp/internal/staging"
)

type Photos interface {
	Start(context.Context, string, int64) (googlephotos.Session, error)
	Query(context.Context, googlephotos.Session) (int64, string, error)
	Chunk(context.Context, googlephotos.Session, int64, []byte, bool) (string, error)
	Create(context.Context, string, string) (googlephotos.Media, error)
	Get(context.Context, string) (googlephotos.Media, error)
}
type Engine struct {
	Store      *journal.Store
	Source     Source
	Photos     Photos
	Directory  string
	Progress   io.Writer
	Reconcile  bool
	MaxRetries int
	Workers    int
	retryMu    sync.Mutex
	retryWait  func(context.Context, time.Duration) error
}

func (e *Engine) Discover(ctx context.Context, meta *Meta) error {
	// Discovery may be restarted only before any transfer has been allowed.
	if meta.InventoryComplete {
		return nil
	}
	if err := e.Store.ResetInventory(); err != nil {
		return err
	}
	var id int64
	batch := make([]journal.Record, 0, 100)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := e.Store.SaveBatch(batch); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}
	err := e.Source.Walk(ctx, meta.Folder, func(source onedrive.Item) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		id++
		item := Item{Source: source, State: "discovered"}
		var err error
		item.Path, err = fullPath(meta.Folder, source.Path)
		if err != nil {
			return err
		}
		item.MIME, item.Video, item.Reason = classify(source)
		if item.Reason != "" {
			item.State = "skipped"
		}
		batch = append(batch, journal.Record{ID: id, State: item.State, Value: item})
		if id%100 == 0 {
			if err := flush(); err != nil {
				return err
			}
			fmt.Fprintf(e.Progress, "Scanned %d source files...\n", id)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}
	drive, err := e.Source.DriveID(ctx)
	if err != nil {
		return err
	}
	if drive != meta.DriveID {
		return errors.New("OneDrive source changed during inventory")
	}
	meta.InventoryComplete = true
	return e.Store.Save(0, "job", meta)
}
func (e *Engine) Run(ctx context.Context, meta *Meta) error {
	if e.Workers < 0 || e.Workers > 8 {
		return errors.New("workers must be between 1 and 8 (or zero for serial default)")
	}
	if e.MaxRetries < 0 || e.MaxRetries > 10 {
		return errors.New("retry count must be between 0 and 10")
	}
	if !meta.RetryAt.IsZero() && time.Now().Before(meta.RetryAt) {
		if e.MaxRetries == 0 {
			return fmt.Errorf("provider requested a pause until %s", meta.RetryAt.Local().Format(time.RFC3339))
		}
		if err := e.awaitRetry(ctx, meta); err != nil {
			return err
		}
	}
	drive, err := e.Source.DriveID(ctx)
	if err != nil {
		return err
	}
	if drive != meta.DriveID {
		return errors.New("OneDrive drive changed; refusing to resume")
	}
	if !meta.InventoryComplete {
		fmt.Fprintln(e.Progress, "Resuming interrupted inventory...")
		if err := e.Discover(ctx, meta); err != nil {
			return err
		}
	}
	ids, err := e.Store.IDs()
	if err != nil {
		return err
	}
	if e.Workers >= 1 {
		return e.runParallel(ctx, meta, ids)
	}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		var item Item
		if err := e.Store.Load(id, &item); err != nil {
			return err
		}
		if item.State == "completed" || item.State == "skipped" || item.State == "processing_failed" {
			continue
		}
		fmt.Fprintf(e.Progress, "[%d/%d] %q: %s\n", id, len(ids), item.Path, item.State)
		if err := e.transferWithRetries(ctx, meta, id, &item); err != nil {
			return err
		}
	}
	return nil
}
func (e *Engine) transfer(ctx context.Context, meta *Meta, id int64, item *Item) error {
	switch item.State {
	case "discovered", "downloading", "staged", "uploading", "uploaded", "creating", "created", "processing":
	default:
		return fmt.Errorf("unrecognized saved transfer state %q; refusing transfer", item.State)
	}
	cachePath := filepath.Join(e.Directory, fmt.Sprintf("stage-%d.bin", id))
	mac := hmac.New(sha256.New, e.Store.Key)
	fmt.Fprintf(mac, "od2gp-stage-%d", id)
	stageKey := mac.Sum(nil)
	save := func() error { item.Reason = ""; return e.Store.Save(id, item.State, item) }
	if item.State == "processing" {
		return e.processing(ctx, id, item)
	}
	if item.State == "created" {
		return e.created(ctx, id, item, cachePath)
	}
	if staging.Required(item.Source.Size) > meta.CacheBytes {
		return errors.New("file exceeds cache capacity; create a new plan with a larger --cache-gib")
	}
	if item.State == "discovered" || item.State == "downloading" {
		current, err := e.Source.Stat(ctx, item.Path)
		if err != nil {
			return err
		}
		if !sameVersion(current, item.Source) {
			return errors.New("source file changed since planning; create a fresh plan for changed content")
		}
		// A partial encrypted download is restarted; no global progress is reset.
		if err := os.Remove(cachePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		item.State = "downloading"
		if err := save(); err != nil {
			return err
		}
		free, err := staging.Free(e.Directory)
		if err != nil {
			return errors.New("cannot check available cache disk space")
		}
		if free < uint64(staging.Required(item.Source.Size)+(128<<20)) {
			return errors.New("insufficient free disk space for this file and journal reserve")
		}
		writer, err := staging.Create(cachePath, stageKey, item.Source.Size)
		if err != nil {
			return err
		}
		err = e.Source.Read(ctx, item.Path, 0, writer)
		if err != nil {
			writer.Abort()
			return err
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
		if err := save(); err != nil {
			return err
		}
	}
	var reader *staging.Reader
	if item.State != "uploaded" && !(item.State == "creating" && !e.Reconcile) {
		var err error
		reader, err = staging.Open(cachePath, stageKey, item.Source.Size)
		if err != nil {
			if item.State == "staged" || item.State == "uploading" {
				item.State = "downloading"
				if saveErr := save(); saveErr != nil {
					return saveErr
				}
				return e.transfer(ctx, meta, id, item)
			}
			return err
		}
		defer reader.Close()
	}
	// Keep the original staged bytes until destination creation is committed.
	if item.State == "staged" || item.State == "uploading" {
		fresh := item.Upload.URL == "" || time.Since(item.Upload.Created) > 7*24*time.Hour
		if !fresh {
			offset, status, queryErr := e.Photos.Query(ctx, item.Upload)
			var api *googlephotos.APIError
			if errors.As(queryErr, &api) && (api.Code == 404 || api.Code == 410) {
				fresh = true
			} else if queryErr != nil {
				return queryErr
			} else if status != "active" {
				fresh = true
			} else {
				if offset < 0 || offset > item.Source.Size {
					return errors.New("Google upload offset is outside this file")
				}
				item.Offset = offset
			}
		}
		if fresh {
			session, err := e.Photos.Start(ctx, item.MIME, item.Source.Size)
			if err != nil {
				return err
			}
			item.Upload = session
			item.Offset = 0
		}
		item.State = "uploading"
		if err := save(); err != nil {
			return err
		}
		if item.Upload.Granularity <= 0 || item.Upload.Granularity > 64<<20 {
			return errors.New("invalid saved upload chunk granularity")
		}
		chunkSize := ((int64(16<<20) + item.Upload.Granularity - 1) / item.Upload.Granularity) * item.Upload.Granularity
		if chunkSize <= 0 || chunkSize > 64<<20 {
			return errors.New("invalid upload chunk size")
		}
		for item.Offset < item.Source.Size {
			if err := ctx.Err(); err != nil {
				return err
			}
			length := min(chunkSize, item.Source.Size-item.Offset)
			data := make([]byte, length)
			if _, err := reader.ReadAt(data, item.Offset); err != nil {
				return err
			}
			final := item.Offset+length == item.Source.Size
			token, err := e.Photos.Chunk(ctx, item.Upload, item.Offset, data, final)
			if err != nil {
				return err
			}
			item.Offset += length
			if final {
				item.Token = token
				item.TokenTime = time.Now().UTC()
				item.State = "uploaded"
			}
			if err := save(); err != nil {
				return err
			}
			fmt.Fprintf(e.Progress, "  [%d] Uploaded %d / %d bytes\n", id, item.Offset, item.Source.Size)
		}
		// A queried active session can contain every byte without being finalized.
		if item.State == "uploading" && item.Offset == item.Source.Size {
			token, err := e.Photos.Chunk(ctx, item.Upload, item.Offset, nil, true)
			if err != nil {
				return err
			}
			item.Token = token
			item.TokenTime = time.Now().UTC()
			item.State = "uploaded"
			if err := save(); err != nil {
				return err
			}
		}
	}
	if item.State == "creating" {
		if !e.Reconcile {
			return errors.New("destination creation outcome is uncertain; use resume --reconcile after reviewing status to retry the identical staged content")
		}
		// Verify the original staged bytes before explicitly retrying an uncertain creation.
		hash := sha256.New()
		if _, err := io.Copy(hash, io.NewSectionReader(reader, 0, item.Source.Size)); err != nil {
			return err
		}
		if hex.EncodeToString(hash.Sum(nil)) != item.Hash {
			return errors.New("staged content differs from the uncertain original upload")
		}
		item.State = "staged"
		item.Upload = googlephotos.Session{}
		item.Token = ""
		if err := save(); err != nil {
			return err
		}
		reader.Close()
		return e.transfer(ctx, meta, id, item)
	}
	if item.State == "uploaded" {
		if time.Since(item.TokenTime) > 23*time.Hour {
			item.Upload = googlephotos.Session{}
			item.Token = ""
			item.State = "staged"
			if err := save(); err != nil {
				return err
			}
			return errors.New("upload token expired; resume to upload this unfinished file again")
		}
		item.State = "creating"
		if err := save(); err != nil {
			return err
		}
		media, err := e.Photos.Create(ctx, item.Token, path.Base(item.Path))
		if err != nil {
			var api *googlephotos.APIError
			if errors.As(err, &api) && (api.Code == 401 || api.Code == 403 || api.Code == 429) {
				item.State = "uploaded"
				if saveErr := save(); saveErr != nil {
					return saveErr
				}
			}
			return err
		}
		item.MediaID = media.ID
		item.State = "created"
		item.Token = ""
		item.Upload = googlephotos.Session{}
		if err := save(); err != nil {
			return err
		}
		if reader != nil {
			reader.Close()
		} // Windows requires the file closed before deleting it.
		return e.created(ctx, id, item, cachePath)
	}
	return nil
}
func (e *Engine) created(ctx context.Context, id int64, item *Item, cachePath string) error {
	if item.MediaID == "" {
		return errors.New("saved destination ID is missing")
	}
	if err := os.Remove(cachePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	item.State = "completed"
	if item.Video {
		item.State = "processing"
	}
	if err := e.Store.Save(id, item.State, item); err != nil {
		return err
	}
	if item.Video {
		return e.processing(ctx, id, item)
	}
	return nil
}
func (e *Engine) processing(ctx context.Context, id int64, item *Item) error {
	media, err := e.Photos.Get(ctx, item.MediaID)
	if err != nil {
		return err
	}
	if media.Metadata.Video == nil {
		return errors.New("video processing status unavailable; resume later")
	}
	switch media.Metadata.Video.Status {
	case "READY":
		item.State = "completed"
		item.Reason = ""
	case "FAILED":
		item.State = "processing_failed"
		item.Reason = "Google could not process this video"
	default:
		item.State = "processing"
	}
	return e.Store.Save(id, item.State, item)
}
