package msgraph

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"od2gp/internal/onedrive"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestWalkPaginatesTraversesAndExcludesOnlyRootVault(t *testing.T) {
	var requests []string
	client := Client{Drive: "drive", ExcludePersonalVault: true, HTTP: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.URL.Path)
		switch r.URL.Path {
		case "/v1.0/drives/drive/root/children":
			if r.URL.Query().Get("page") == "2" {
				return response(`{"value":[{"id":"rootphoto","name":"photo.jpg","size":10,"eTag":"v1","file":{"mimeType":"image/jpeg"}}]}`), nil
			}
			return response(`{"value":[{"id":"vault","name":"Personal Vault","folder":{}},{"id":"photos","name":"Pictures","folder":{}}],"@odata.nextLink":"https://graph.microsoft.com/v1.0/drives/drive/root/children?page=2"}`), nil
		case "/v1.0/drives/drive/items/photos/children":
			return response(`{"value":[{"id":"nested","name":"Personal Vault","folder":{}},{"id":"unicode","name":"ảnh #1.jpg","size":12,"eTag":"v1","cTag":"content1","file":{"mimeType":"image/jpeg"}}]}`), nil
		case "/v1.0/drives/drive/items/nested/children":
			return response(`{"value":[{"id":"ordinary","name":"ordinary.jpg","size":13,"eTag":"v1","file":{}}]}`), nil
		default:
			t.Fatalf("unexpected request: %s", r.URL)
			return nil, nil
		}
	})}}
	var items []onedrive.Item
	err := client.Walk(context.Background(), "", func(i onedrive.Item) error { items = append(items, i); return nil })
	if err != nil || len(items) != 3 {
		t.Fatalf("incomplete tree: %+v %v requests=%v", items, err, requests)
	}
	if items[1].Path != "Pictures/ảnh #1.jpg" || items[1].Hashes["graphCTag"] != "content1" {
		t.Fatalf("lost path/version: %+v", items[1])
	}
	if items[2].Path != "Pictures/Personal Vault/ordinary.jpg" {
		t.Fatal("ordinary nested folder omitted")
	}
}
func TestGraphRejectsHostilePaginationAndSourcePaths(t *testing.T) {
	calls := 0
	client := Client{Drive: "drive", HTTP: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		calls++
		return response(`{"value":[],"@odata.nextLink":"https://attacker.example/token"}`), nil
	})}}
	err := client.Walk(context.Background(), "", func(onedrive.Item) error { return nil })
	if err == nil || calls != 1 {
		t.Fatalf("followed untrusted pagination: %v calls=%d", err, calls)
	}
	for _, name := range []string{"../Pictures", "Pictures/../../secret", `Pictures\secret`, "bad\x00name"} {
		if _, err := client.Stat(context.Background(), name); err == nil {
			t.Fatalf("unsafe path accepted: %q", name)
		}
	}
	if calls != 1 {
		t.Fatal("unsafe paths reached the network")
	}
}
func TestGraphPathEncodingAndStatVersions(t *testing.T) {
	client := Client{Drive: "drive", HTTP: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.EscapedPath(), "%23") || !strings.Contains(r.URL.EscapedPath(), "%25") || r.URL.Query().Get("$select") != fields {
			t.Fatalf("bad escaping/query: %s", r.URL)
		}
		return response(`{"id":"photo","name":"ảnh #%?.jpg","size":12,"lastModifiedDateTime":"2026-10-18T00:00:00Z","eTag":"version1","file":{"mimeType":"image/jpeg"}}`), nil
	})}}
	item, err := client.Stat(context.Background(), "Pictures/ảnh #%?.jpg")
	if err != nil || item.Hashes["graphETag"] != "version1" || item.ID != "photo" || item.ModTime == "" {
		t.Fatalf("lost version: %+v %v", item, err)
	}
}
func TestDownloadDoesNotForwardGraphAuthorization(t *testing.T) {
	apiCalls, downloads := 0, 0
	client := Client{Drive: "drive", HTTP: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		apiCalls++
		resp := response("")
		resp.StatusCode = 302
		resp.Header.Set("Location", "https://cdn.example/file?private-signature=secret")
		return resp, nil
	})}, downloadTransport: transport(func(r *http.Request) (*http.Response, error) {
		downloads++
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.URL.Host != "cdn.example" {
			t.Fatal("credentials forwarded to download host")
		}
		if r.Header.Get("Range") != "bytes=2-" {
			t.Fatal("range was not sent to content URL")
		}
		resp := response("cdef")
		resp.StatusCode = 206
		resp.Header.Set("Content-Range", "bytes 2-5/6")
		return resp, nil
	})}
	var out bytes.Buffer
	if err := client.Read(context.Background(), "Pictures/photo.jpg", 2, &out); err != nil || out.String() != "cdef" || apiCalls != 1 || downloads != 1 {
		t.Fatalf("download failed: %v %q", err, out.String())
	}
}
func TestUnsafeDownloadAndIgnoredRangeFailClosed(t *testing.T) {
	for _, location := range []string{"http://cdn.example/file", "https://127.0.0.1/file", "https://localhost/file", "https://user:secret@cdn.example/file"} {
		client := Client{Drive: "drive", HTTP: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
			resp := response("")
			resp.StatusCode = 302
			resp.Header.Set("Location", location)
			return resp, nil
		})}}
		err := client.Read(context.Background(), "photo.jpg", 0, io.Discard)
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe URL accepted or leaked: %s %v", location, err)
		}
	}
	client := Client{Drive: "drive", HTTP: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		resp := response("")
		resp.StatusCode = 302
		resp.Header.Set("Location", "https://cdn.example/file")
		return resp, nil
	})}, downloadTransport: transport(func(*http.Request) (*http.Response, error) { return response("full body"), nil })}
	if err := client.Read(context.Background(), "photo.jpg", 3, io.Discard); err == nil {
		t.Fatal("ignored range accepted")
	}
}
func TestNativeInventoryPreservesVisitorStopAndVaultWarning(t *testing.T) {
	client := Client{Drive: "drive", HTTP: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		return response(`{"value":[{"id":"vault","name":"Personal Vault","folder":{}}]}`), nil
	})}}
	if err := client.Walk(context.Background(), "", func(onedrive.Item) error { return nil }); err == nil || !strings.Contains(err.Error(), "exclude-personal-vault") {
		t.Fatalf("vault silently omitted: %v", err)
	}
	stop := errors.New("stop")
	if err := client.List(context.Background(), "Pictures", false, func(onedrive.Item) error { return stop }); !errors.Is(err, stop) {
		t.Fatalf("lost visitor stop: %v", err)
	}
}

func TestNativeFileWithoutVersionProofStopsInventory(t *testing.T) {
	client := Client{Drive: "drive", HTTP: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		return response(`{"value":[{"id":"file","name":"photo.jpg","size":12,"file":{"mimeType":"image/jpeg"}}]}`), nil
	})}}
	visited := false
	err := client.Walk(context.Background(), "Pictures", func(onedrive.Item) error { visited = true; return nil })
	if err == nil || visited || !strings.Contains(err.Error(), "version") {
		t.Fatalf("accepted unversioned file: %v", err)
	}
}
