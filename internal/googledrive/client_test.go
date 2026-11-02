package googledrive

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestUploadRangeAndFinalResponse(t *testing.T) {
	n := 0
	c := Client{HTTP: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		n++
		status := 308
		body := ""
		h := http.Header{"Range": []string{"bytes=0-262143"}}
		if n == 2 {
			status = 200
			body = `{"id":"target","size":"262145","name":"ảnh.txt"}`
		}
		return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	off, f, e := c.Query(context.Background(), "https://www.googleapis.com/upload/drive/v3/files?upload_id=test", 262145)
	if e != nil || off != 262144 || f != nil {
		t.Fatalf("bad query %d %v", off, e)
	}
	_, f, e = c.Chunk(context.Background(), "https://www.googleapis.com/upload/drive/v3/files?upload_id=test", off, 262145, []byte{1})
	if e != nil || f.ID != "target" {
		t.Fatalf("bad final %v", e)
	}
}
func TestRejectUntrustedSessionAndClassifyErrors(t *testing.T) {
	c := Client{HTTP: http.DefaultClient}
	if _, _, e := c.Query(context.Background(), "https://evil.example/upload", 1); e == nil {
		t.Fatal("accepted untrusted session")
	}
	if !(&Error{Code: 503}).Temporary() || (&Error{Code: 403, Reason: "storageQuotaExceeded"}).Temporary() {
		t.Fatal("bad retry classification")
	}
}
