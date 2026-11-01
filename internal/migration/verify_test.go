package migration

import (
	"context"
	"od2gp/internal/googlephotos"
	"od2gp/internal/onedrive"
	"os"
	"path/filepath"
	"testing"
)

type metadataSource struct{ *sourceFake }

func (s metadataSource) StatMetadata(ctx context.Context, p string) (onedrive.Item, error) {
	return s.Stat(ctx, p)
}

type metadataPhotos struct {
	*photosFake
	items []googlephotos.Media
	fail  bool
}

func (p metadataPhotos) ListAppMedia(ctx context.Context, visit func(googlephotos.Media) error) error {
	for _, m := range p.items {
		if err := visit(m); err != nil {
			return err
		}
	}
	if p.fail {
		return &googlephotos.APIError{Code: 403}
	}
	return nil
}
func (p metadataPhotos) Get(ctx context.Context, id string) (googlephotos.Media, error) {
	for _, m := range p.items {
		if m.ID == id {
			return m, nil
		}
	}
	return googlephotos.Media{}, nil
}
func TestMetadataVerificationKeepsAmbiguousAndMissingFacts(t *testing.T) {
	for _, scenario := range []string{"match", "ambiguous", "missing", "listing fails"} {
		t.Run(scenario, func(t *testing.T) {
			e, meta, source, photos, close := fixture(t)
			defer close()
			source.item.Width = 400
			source.item.Height = 300
			source.item.TakenTime = "2020-01-01T00:00:00Z"
			source.item.MimeType = "image/jpeg"
			m := googlephotos.Media{ID: "known", Filename: source.item.Name, MIME: source.item.MimeType}
			m.Metadata.Width = "400"
			m.Metadata.Height = "300"
			m.Metadata.CreationTime = source.item.TakenTime
			items := []googlephotos.Media{m}
			if scenario == "ambiguous" {
				m.ID = "another"
				items = append(items, m)
			}
			if scenario == "missing" {
				source.item.TakenTime = ""
			}
			e.Source = metadataSource{source}
			e.Photos = metadataPhotos{photosFake: photos, items: items, fail: scenario == "listing fails"}
			var item Item
			e.Store.Load(1, &item)
			item.State = "needs_review"
			e.Store.Save(1, item.State, &item)
			stage := filepath.Join(e.Directory, "stage-1.bin")
			os.WriteFile(stage, []byte("retained"), 0600)
			err := e.VerifyReviews(context.Background(), meta)
			if scenario == "listing fails" && err == nil {
				t.Fatal("accepted incomplete listing")
			}
			e.Store.Load(1, &item)
			if scenario == "match" {
				if item.State != "completed" || item.MediaID != "known" || item.Verification != "filename_metadata" {
					t.Fatalf("%+v", item)
				}
				if _, err := os.Stat(stage); !os.IsNotExist(err) {
					t.Fatal("staging retained")
				}
			} else {
				if item.State != "needs_review" {
					t.Fatal("false confirmation")
				}
				if _, err := os.Stat(stage); err != nil {
					t.Fatal("staging lost")
				}
			}
		})
	}
}
