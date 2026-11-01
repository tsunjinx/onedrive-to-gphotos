package msgraph

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"od2gp/internal/journal"
	"od2gp/internal/onedrive"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDeltaCheckpointReopenLastItemWinsAndLateParents(t *testing.T) {
	name := filepath.Join(t.TempDir(), "job.db")
	key := bytes.Repeat([]byte{1}, 32)
	store, err := journal.Open(name, key, true)
	if err != nil {
		t.Fatal(err)
	}
	next := endpoint + "/drives/drive/root/delta?page=2"
	calls := 0
	c := Client{Drive: "drive", HTTP: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return response(`{"value":[{"id":"photo","name":"old.jpg","file":{},"size":1,"eTag":"old","parentReference":{"id":"folder"}}],"@odata.nextLink":"` + next + `"}`), nil
		}
		return &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}}
	visit := func(onedrive.Item) error { t.Fatal("partial scan emitted item"); return nil }
	if err := c.WalkCheckpoint(context.Background(), "/", store, "binding", io.Discard, visit); err == nil {
		t.Fatal("expected interruption")
	}
	store.Close()
	store, err = journal.Open(name, key, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	c.HTTP.Transport = transport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != next {
			t.Fatalf("did not resume saved cursor: %s", r.URL)
		}
		return response(`{"value":[{"id":"root","root":{},"folder":{}},{"id":"folder","name":"Pictures","folder":{},"parentReference":{"id":"root"}},{"id":"photo","name":"new.jpg","file":{},"size":2,"eTag":"new","parentReference":{"id":"folder"}},{"id":"gone","deleted":{}}],"@odata.deltaLink":"` + endpoint + `/drives/drive/root/delta?token=finished"}`), nil
	})
	var got []onedrive.Item
	collect := func(i onedrive.Item) error { got = append(got, i); return nil }
	if err = c.WalkCheckpoint(context.Background(), "/", store, "binding", io.Discard, collect); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != "Pictures/new.jpg" || got[0].Size != 2 {
		t.Fatalf("bad inventory %+v", got)
	}
	c.HTTP.Transport = transport(func(*http.Request) (*http.Response, error) {
		t.Fatal("completed pages fetched again")
		return nil, errors.New("unexpected")
	})
	got = nil
	if err = c.WalkCheckpoint(context.Background(), "/", store, "binding", io.Discard, collect); err != nil || len(got) != 1 {
		t.Fatalf("replay: %v %+v", err, got)
	}
	if err = c.WalkCheckpoint(context.Background(), "/", store, "changed", io.Discard, collect); err == nil {
		t.Fatal("accepted changed binding")
	}
}

func TestDeltaHierarchyFailsClosedAndVaultExclusion(t *testing.T) {
	for _, tc := range []struct {
		name, items   string
		exclude, fail bool
		count         int
	}{
		{"vault", `{"id":"v","name":"Personal Vault","folder":{},"parentReference":{"id":"root"}},{"id":"p","name":"x.jpg","file":{},"eTag":"v1","parentReference":{"id":"v"}}`, true, false, 0},
		{"nested vault", `{"id":"f","name":"Pictures","folder":{},"parentReference":{"id":"root"}},{"id":"v","name":"Personal Vault","folder":{},"parentReference":{"id":"f"}},{"id":"p","name":"x.jpg","file":{},"eTag":"v1","parentReference":{"id":"v"}}`, true, false, 1},
		{"vault included", `{"id":"v","name":"Personal Vault","folder":{},"parentReference":{"id":"root"}}`, false, true, 0},
		{"missing parent", `{"id":"p","name":"x.jpg","file":{},"eTag":"v1","parentReference":{"id":"missing"}}`, false, true, 0},
		{"cycle", `{"id":"p","name":"x.jpg","file":{},"eTag":"v1","parentReference":{"id":"p"}}`, false, true, 0},
		{"missing version", `{"id":"p","name":"x.jpg","file":{},"parentReference":{"id":"root"}}`, false, true, 0},
		{"remote", `{"id":"p","name":"shared","remoteItem":{},"parentReference":{"id":"root"}}`, false, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, err := journal.Open(filepath.Join(t.TempDir(), "job"), bytes.Repeat([]byte{2}, 32), true)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			c := Client{Drive: "d", ExcludePersonalVault: tc.exclude, HTTP: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
				return response(`{"value":[{"id":"root","root":{},"folder":{}},` + tc.items + `],"@odata.deltaLink":"` + endpoint + `/drives/d/root/delta?token=end"}`), nil
			})}}
			count := 0
			err = c.WalkCheckpoint(context.Background(), "/", store, "binding", io.Discard, func(onedrive.Item) error { count++; return nil })
			if (err != nil) != tc.fail || count != tc.count {
				t.Fatalf("error=%v count=%d", err, count)
			}
		})
	}
}

func TestDeltaEmptyPageCancellationAndExpiredCursor(t *testing.T) {
	store, err := journal.Open(filepath.Join(t.TempDir(), "job"), bytes.Repeat([]byte{3}, 32), true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	next := endpoint + "/drives/d/root/delta?page=2"
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	c := Client{Drive: "d", HTTP: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			cancel()
			return response(`{"value":[],"@odata.nextLink":"` + next + `"}`), nil
		}
		t.Fatal("request after cancellation")
		return nil, context.Canceled
	})}}
	if err = c.WalkCheckpoint(ctx, "/", store, "binding", io.Discard, func(onedrive.Item) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var state deltaCheckpoint
	if err = store.LoadScanState(&state); err != nil || state.Next != next || state.Pages != 1 {
		t.Fatalf("checkpoint: %+v %v", state, err)
	}
	c.HTTP.Transport = transport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != next {
			t.Fatal("lost empty-page continuation")
		}
		return &http.Response{StatusCode: 410, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})
	if err = c.WalkCheckpoint(context.Background(), "/", store, "binding", io.Discard, func(onedrive.Item) error { t.Fatal("expired inventory emitted"); return nil }); err == nil {
		t.Fatal("expected expired cursor error")
	}
	if err = store.LoadScanState(&state); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("checkpoint not reset: %v", err)
	}
}

func TestDeltaUntrustedContinuationDoesNotCommit(t *testing.T) {
	store, err := journal.Open(filepath.Join(t.TempDir(), "job"), bytes.Repeat([]byte{4}, 32), true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	c := Client{Drive: "d", HTTP: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		return response(`{"value":[],"@odata.nextLink":"https://evil.example/steal"}`), nil
	})}}
	if err = c.WalkCheckpoint(context.Background(), "/", store, "binding", io.Discard, func(onedrive.Item) error { return nil }); err == nil {
		t.Fatal("accepted hostile cursor")
	}
	var state deltaCheckpoint
	if err = store.LoadScanState(&state); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("committed invalid page")
	}
}

// Same wide hierarchy through both scanners: compare scope and request count,
// not synthetic wall time (a local transport cannot model service latency).
func TestDeltaMatchesChildrenWithFewerFolderRequests(t *testing.T) {
	var folders, all []json.RawMessage
	all = append(all, json.RawMessage(`{"id":"root","root":{},"folder":{}}`))
	files := map[string]json.RawMessage{}
	for i := 0; i < 40; i++ {
		folder := fmt.Sprintf(`{"id":"f%d","name":"folder%d","folder":{},"parentReference":{"id":"root"}}`, i, i)
		file := fmt.Sprintf(`{"id":"p%d","name":"photo.jpg","size":12,"eTag":"v1","file":{"mimeType":"image/jpeg"},"parentReference":{"id":"f%d"}}`, i, i)
		folders = append(folders, json.RawMessage(folder))
		all = append(all, json.RawMessage(folder), json.RawMessage(file))
		files[fmt.Sprintf("/v1.0/drives/d/items/f%d/children", i)] = json.RawMessage(file)
	}
	var childCalls atomic.Int32
	c := Client{Drive: "d", HTTP: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		childCalls.Add(1)
		values := folders
		if !strings.HasSuffix(r.URL.Path, "/root/children") {
			file, ok := files[r.URL.Path]
			if !ok {
				return nil, fmt.Errorf("unexpected path %s", r.URL.Path)
			}
			values = []json.RawMessage{file}
		}
		data, _ := json.Marshal(map[string]any{"value": values})
		return response(string(data)), nil
	})}}
	children := map[string]int64{}
	if err := c.Walk(context.Background(), "/", func(i onedrive.Item) error { children[i.Path] = i.Size; return nil }); err != nil {
		t.Fatal(err)
	}
	store, err := journal.Open(filepath.Join(t.TempDir(), "job"), bytes.Repeat([]byte{5}, 32), true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	deltaCalls := 0
	c.HTTP.Transport = transport(func(*http.Request) (*http.Response, error) {
		deltaCalls++
		data, _ := json.Marshal(map[string]any{"value": all, "@odata.deltaLink": endpoint + "/drives/d/root/delta?token=end"})
		return response(string(data)), nil
	})
	delta := map[string]int64{}
	if err = c.WalkCheckpoint(context.Background(), "/", store, "binding", io.Discard, func(i onedrive.Item) error { delta[i.Path] = i.Size; return nil }); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(children, delta) || len(delta) != 40 || childCalls.Load() != 41 || deltaCalls != 1 {
		t.Fatalf("scope or requests: children=%d delta=%d requests=%d/%d", len(children), len(delta), childCalls.Load(), deltaCalls)
	}
	t.Logf("40-folder fixture: children %d requests; delta %d; same 40 files", childCalls.Load(), deltaCalls)
}
