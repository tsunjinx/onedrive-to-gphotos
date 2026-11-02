package msgraph

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"od2gp/internal/journal"
	"od2gp/internal/onedrive"
	"strings"
	"sync"
	"time"
)

type deltaItem struct {
	resource
	Parent *struct {
		ID    string `json:"id"`
		Drive string `json:"driveId"`
	} `json:"parentReference"`
	Root    json.RawMessage `json:"root"`
	Deleted json.RawMessage `json:"deleted"`
}
type deltaCheckpoint struct {
	Binding, Next, Final string
	Pages                int
}

func present(raw json.RawMessage) bool { return len(raw) > 0 && string(raw) != "null" }

// WalkCheckpoint uses a disk-backed delta inventory for root migrations only.
func (c Client) WalkCheckpoint(ctx context.Context, folder string, store *journal.Store, binding string, out io.Writer, visit func(onedrive.Item) error) error {
	return c.walkCheckpoint(ctx, folder, store, binding, out, false, visit)
}
func (c Client) WalkTreeCheckpoint(ctx context.Context, folder string, store *journal.Store, binding string, out io.Writer, visit func(onedrive.Item) error) error {
	return c.walkCheckpoint(ctx, folder, store, binding, out, true, visit)
}
func (c Client) walkCheckpoint(ctx context.Context, folder string, store *journal.Store, binding string, out io.Writer, folders bool, visit func(onedrive.Item) error) error {
	if strings.Trim(folder, "/") != "" {
		if folders {
			return c.WalkTree(ctx, folder, visit)
		}
		return c.Walk(ctx, folder, visit)
	}
	if out == nil {
		out = io.Discard
	}
	var state deltaCheckpoint
	err := store.LoadScanState(&state)
	if errors.Is(err, sql.ErrNoRows) {
		state = deltaCheckpoint{Binding: binding, Next: endpoint + "/drives/" + url.PathEscape(c.Drive) + "/root/delta?$select=" + fields + ",parentReference,root,deleted"}
	} else if err != nil {
		return err
	}
	if state.Binding != binding {
		return errors.New("scan checkpoint scope or account changed; refusing to reuse it")
	}
	out = &scanWriter{Writer: out}
	c.scanOutput = out
	seen := map[string]bool{}
	for state.Final == "" {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !validAPI(state.Next) || seen[state.Next] {
			return errors.New("invalid or repeated delta continuation")
		}
		seen[state.Next] = true
		fmt.Fprintf(out, "Scanning OneDrive delta page %d...\n", state.Pages+1)
		started := time.Now()
		resp, err := c.deltaRequest(ctx, state.Next, out)
		if err != nil {
			return err
		}
		if resp.StatusCode == 410 {
			resp.Body.Close()
			if err := store.ResetScan(); err != nil {
				return err
			}
			return errors.New("OneDrive scan cursor expired; checkpoint reset. Continue the same job to rescan; no uploads started")
		}
		if resp.StatusCode != 200 {
			err := apiFailure(resp)
			resp.Body.Close()
			return err
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
		resp.Body.Close()
		if err != nil || len(data) > 8<<20 {
			return errors.New("delta page interrupted or too large")
		}
		var page struct {
			Value []json.RawMessage `json:"value"`
			Next  string            `json:"@odata.nextLink"`
			Final string            `json:"@odata.deltaLink"`
		}
		if json.Unmarshal(data, &page) != nil || page.Value == nil || (page.Next == "") == (page.Final == "") {
			return errors.New("invalid delta page; inventory incomplete")
		}
		link := page.Next
		if link == "" {
			link = page.Final
		}
		if !validAPI(link) || page.Next == state.Next {
			return errors.New("untrusted or repeated delta continuation")
		}
		entries := map[string]json.RawMessage{}
		for _, raw := range page.Value {
			var item deltaItem
			if json.Unmarshal(raw, &item) != nil || item.ID == "" {
				return errors.New("unidentified delta item")
			}
			entries[item.ID] = raw
		}
		state.Pages++
		state.Next = page.Next
		state.Final = page.Final
		if err := store.SaveScanPage(state, entries); err != nil {
			return err
		}
		fmt.Fprintf(out, "Saved delta page %d: %d entries, %s. Scan checkpoint retained.\n", state.Pages, len(page.Value), time.Since(started).Round(time.Millisecond))
	}
	fmt.Fprintf(out, "Resolving inventory from %d saved delta pages...\n", state.Pages)
	// Resolve each hierarchy from disk; bound recursion and never trust path text.
	var resolve func(deltaItem, map[string]bool) (string, bool, error)
	resolve = func(item deltaItem, chain map[string]bool) (string, bool, error) {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		if present(item.Deleted) {
			return "", true, nil
		}
		if chain[item.ID] || len(chain) > 512 {
			return "", false, errors.New("delta hierarchy cycle or excessive depth")
		}
		chain[item.ID] = true
		if present(item.Root) {
			if item.Parent != nil && item.Parent.Drive != "" && item.Parent.Drive != c.Drive {
				return "", false, errors.New("delta root drive mismatch")
			}
			return "", false, nil
		}
		if item.Name == "" || item.Name == "." || item.Name == ".." || strings.ContainsAny(item.Name, "/\\\x00") {
			return "", false, errors.New("unsafe delta item name")
		}
		if item.Parent == nil || item.Parent.ID == "" || (item.Parent.Drive != "" && item.Parent.Drive != c.Drive) {
			return "", false, errors.New("delta parent missing or drive mismatch")
		}
		var parent deltaItem
		if err := store.LoadScan(item.Parent.ID, &parent); err != nil {
			return "", false, errors.New("delta parent unavailable; inventory incomplete")
		}
		if present(parent.Deleted) {
			return "", false, errors.New("live delta item has a deleted parent; inventory incomplete")
		}
		prefix, excluded, err := resolve(parent, chain)
		if err != nil || excluded {
			return "", excluded, err
		}
		if !present(parent.Root) && parent.Folder == nil {
			return "", false, errors.New("delta parent is not a folder")
		}
		name := item.Name
		if prefix != "" {
			name = prefix + "/" + name
		}
		if c.excluded(name) {
			return "", true, nil
		}
		if name == "Personal Vault" {
			if c.ExcludePersonalVault {
				return "", true, nil
			}
			return "", false, errors.New("Personal Vault needs separate handling; use --exclude-personal-vault explicitly to migrate the rest")
		}
		if present(item.Remote) {
			return "", false, errors.New("delta inventory contains an unsupported remote shortcut")
		}
		return name, false, nil
	}
	return store.WalkScan(func(raw json.RawMessage) error {
		var item deltaItem
		if err := json.Unmarshal(raw, &item); err != nil {
			return err
		}
		name, excluded, err := resolve(item, map[string]bool{})
		if err != nil || excluded {
			return err
		}
		if present(item.Root) {
			return nil
		}
		if item.Folder != nil && item.Package == nil {
			if folders {
				return visit(item.item(name))
			}
			return nil
		}
		if folders && (item.Package != nil || item.File == nil || item.Size == nil) {
			return fmt.Errorf("unsupported object or missing size at %q; inventory incomplete", name)
		}
		if item.File != nil && item.ETag == "" && item.CTag == "" {
			return errors.New("delta omitted source version metadata; inventory incomplete")
		}
		return visit(item.item(name))
	})
}

// Serialize heartbeat and retry diagnostics without exposing cursor URLs.
type scanWriter struct {
	sync.Mutex
	io.Writer
}

func (w *scanWriter) Write(p []byte) (int, error) {
	w.Lock()
	defer w.Unlock()
	return w.Writer.Write(p)
}
func (c Client) deltaRequest(ctx context.Context, address string, out io.Writer) (*http.Response, error) {
	type result struct {
		response *http.Response
		err      error
	}
	done := make(chan result, 1)
	go func() { response, err := c.request(ctx, address); done <- result{response, err} }()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	started := time.Now()
	for {
		select {
		case r := <-done:
			return r.response, r.err
		case <-ticker.C:
			fmt.Fprintf(out, "Scan active: waiting for Graph response or retry pause (%s).\n", time.Since(started).Round(time.Second))
		}
	}
}
