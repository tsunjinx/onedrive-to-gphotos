package googlephotos

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCreateBatchHandlesPartialAndUncertainResults(t *testing.T) {
	client := Client{HTTP: &http.Client{Transport: fakeTransport(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		if strings.Count(string(body), "uploadToken") != 3 {
			t.Errorf("wrong request: %s", body)
		}
		return &http.Response{StatusCode: 207, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"newMediaItemResults":[{"uploadToken":"one","status":{},"mediaItem":{"id":"created"}},{"uploadToken":"two","status":{"code":3,"message":"secret-provider-payload"}}]}`))}, nil
	})}}
	results, err := client.CreateBatch(context.Background(), []NewItem{{Token: "one", Name: "a.jpg"}, {Token: "two", Name: "b.jpg"}, {Token: "three", Name: "c.jpg"}})
	if err != nil || len(results) != 3 {
		t.Fatalf("result unavailable: %v %v", results, err)
	}
	if results[0].Media.ID != "created" || results[0].Err != nil {
		t.Fatal("lost successful item")
	}
	var rejected *ItemError
	if !errors.As(results[1].Err, &rejected) || rejected.Code != 3 {
		t.Fatal("lost per-item rejection")
	}
	var unknown *OutcomeUnknownError
	if !errors.As(results[2].Err, &unknown) {
		t.Fatal("missing result falsely resolved")
	}
	if strings.Contains(results[1].Err.Error(), "secret") {
		t.Fatal("leaked provider details")
	}
}

func TestCreateBatchValidatesLimitBeforeRequest(t *testing.T) {
	client := Client{HTTP: &http.Client{Transport: fakeTransport(func(*http.Request) (*http.Response, error) { t.Fatal("invalid batch reached network"); return nil, nil })}}
	for _, n := range []int{0, 51} {
		if _, err := client.CreateBatch(context.Background(), make([]NewItem, n)); err == nil {
			t.Fatalf("accepted size %d", n)
		}
	}
}

func TestCreateBatchDoesNotAssignReorderedOrMissingIDs(t *testing.T) {
	client := Client{HTTP: &http.Client{Transport: fakeTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"newMediaItemResults":[{"uploadToken":"two","mediaItem":{"id":"wrong"}},{"uploadToken":"two"}]}`))}, nil
	})}}
	results, err := client.CreateBatch(context.Background(), []NewItem{{Token: "one"}, {Token: "two"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		var unknown *OutcomeUnknownError
		if !errors.As(r.Err, &unknown) || r.Media.ID != "" {
			t.Fatalf("unsafe result: %+v", r)
		}
	}
}

func TestAPIErrorReportsOperationWithoutProviderDetails(t *testing.T) {
	client := Client{HTTP: &http.Client{Transport: fakeTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("private-token provider-message"))}, nil
	})}}
	_, err := client.CreateBatch(context.Background(), []NewItem{{Token: "private-token"}})
	if err == nil || !strings.Contains(err.Error(), "HTTP 429") || !strings.Contains(err.Error(), "media creation") || strings.Contains(err.Error(), "private-token") {
		t.Fatalf("unsafe diagnostic: %v", err)
	}
}
