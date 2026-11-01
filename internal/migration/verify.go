package migration

import (
	"context"
	"errors"
	"fmt"
	"od2gp/internal/googlephotos"
	"od2gp/internal/onedrive"
	"path"
	"path/filepath"
	"strconv"
	"time"
)

type mediaLister interface {
	ListAppMedia(context.Context, func(googlephotos.Media) error) error
}

func metadataMatches(source onedrive.Item, m googlephotos.Media) bool {
	if source.Name == "" || source.Name != m.Filename || source.MimeType == "" || source.MimeType != m.MIME || source.Width <= 0 || source.Height <= 0 {
		return false
	}
	w, e1 := strconv.ParseInt(m.Metadata.Width, 10, 64)
	h, e2 := strconv.ParseInt(m.Metadata.Height, 10, 64)
	a, e3 := time.Parse(time.RFC3339Nano, source.TakenTime)
	b, e4 := time.Parse(time.RFC3339Nano, m.Metadata.CreationTime)
	return e1 == nil && e2 == nil && e3 == nil && e4 == nil && w == source.Width && h == source.Height && a.Equal(b)
}

// VerifyReviews applies the user's filename+metadata policy, not byte identity.
// Missing facts and ambiguous candidates remain review items with staging intact.
func (e *Engine) VerifyReviews(ctx context.Context, meta *Meta) (result error) {
	defer func() {
		var api *googlephotos.APIError
		if errors.As(result, &api) && (api.Code == 429 || api.Code >= 500) {
			result = e.scheduleRetry(ctx, meta, api, 0, true, result)
		}
	}()
	sourceMetadata, ok := e.Source.(interface {
		StatMetadata(context.Context, string) (onedrive.Item, error)
	})
	if !ok {
		return nil
	}
	lister, ok := e.Photos.(mediaLister)
	if !ok {
		return nil
	}
	ids, err := e.Store.IDs()
	if err != nil {
		return err
	}
	type review struct {
		id        int64
		item      Item
		source    onedrive.Item
		candidate googlephotos.Media
		ambiguous bool
	}
	var reviews []*review
	for _, id := range ids {
		var item Item
		if err := e.Store.Load(id, &item); err != nil {
			return err
		}
		if item.State != "needs_review" {
			continue
		}
		source, err := sourceMetadata.StatMetadata(ctx, item.Path)
		if err != nil {
			return err
		}
		if !sameVersion(source, item.Source) || source.Width <= 0 || source.Height <= 0 || source.TakenTime == "" {
			continue
		}
		reviews = append(reviews, &review{id: id, item: item, source: source})
	}
	if len(reviews) == 0 {
		return nil
	}
	fmt.Fprintf(e.Progress, "Verifying %d review files by filename, MIME, dimensions and capture time...\n", len(reviews))
	if err := lister.ListAppMedia(ctx, func(m googlephotos.Media) error {
		for _, r := range reviews {
			if metadataMatches(r.source, m) {
				if r.candidate.ID != "" && r.candidate.ID != m.ID {
					r.ambiguous = true
				}
				r.candidate = m
			}
		}
		return nil
	}); err != nil {
		return err
	}
	for _, r := range reviews {
		if r.ambiguous || r.candidate.ID == "" {
			continue
		}
		current, err := e.Photos.Get(ctx, r.candidate.ID)
		if err != nil {
			return err
		}
		if current.ID != r.candidate.ID || !metadataMatches(r.source, current) {
			continue
		}
		r.item.MediaID = current.ID
		r.item.State = "created"
		r.item.Token = ""
		r.item.Upload = googlephotos.Session{}
		r.item.Verification = "filename_metadata"
		r.item.Reason = ""
		if err := e.Store.Save(r.id, r.item.State, &r.item); err != nil {
			return err
		}
		if err := e.cleanupCreated(r.id, &r.item, filepath.Join(e.Directory, fmt.Sprintf("stage-%d.bin", r.id))); err != nil {
			return err
		}
		fmt.Fprintf(e.Progress, "[%d] %q: verified by filename and metadata; staging released.\n", r.id, path.Base(r.item.Path))
	}
	return nil
}
