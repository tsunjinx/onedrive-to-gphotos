package msgraph

import (
	"context"
	"fmt"
	"net/http"
	"od2gp/internal/onedrive"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestInventoryPrefetchOverlapsThreeFoldersButSerializesVisits(t *testing.T) {
	started := make(chan struct{}, 3)
	release := make(chan struct{})
	var active, peak atomic.Int32
	c := Client{Drive: "drive", HTTP: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/root/") {
			return response(`{"value":[{"id":"a","name":"a","folder":{}},{"id":"b","name":"b","folder":{}},{"id":"c","name":"c","folder":{}}]}`), nil
		}
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		id := strings.Split(r.URL.Path, "/")[5]
		return response(fmt.Sprintf(`{"value":[{"id":"file-%s","name":"p.jpg","eTag":"v1","file":{},"size":1}]}`, id)), nil
	})}}
	done := make(chan error, 1)
	var paths []string
	go func() {
		done <- c.Walk(context.Background(), "", func(i onedrive.Item) error { paths = append(paths, i.Path); return nil })
	}()
	for i := 0; i < 3; i++ {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			close(release)
			<-done
			t.Fatal("folder requests did not overlap")
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if peak.Load() != 3 || strings.Join(paths, ",") != "c/p.jpg,b/p.jpg,a/p.jpg" {
		t.Fatalf("peak=%d paths=%v", peak.Load(), paths)
	}
}
