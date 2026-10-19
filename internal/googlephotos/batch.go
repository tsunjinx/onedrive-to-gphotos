package googlephotos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type NewItem struct{ Token, Name string }
type CreateResult struct {
	Media Media
	Err   error
}

// ItemError represents an explicit rejection in an otherwise valid response.
// Provider messages are omitted because they may contain filenames or secrets.
type ItemError struct{ Code int }

func (e *ItemError) Error() string {
	return fmt.Sprintf("Google Photos rejected media creation (item status %d)", e.Code)
}

type OutcomeUnknownError struct{}

func (*OutcomeUnknownError) Error() string {
	return "media creation outcome uncertain; review status and use resume --reconcile"
}

func (c Client) CreateBatch(ctx context.Context, items []NewItem) ([]CreateResult, error) {
	if len(items) < 1 || len(items) > 50 {
		return nil, errors.New("media creation batch must contain 1-50 items")
	}
	payload := struct {
		Items []any `json:"newMediaItems"`
	}{Items: make([]any, len(items))}
	for i, item := range items {
		if item.Token == "" {
			return nil, errors.New("media creation upload token missing")
		}
		payload.Items[i] = map[string]any{"simpleMediaItem": map[string]string{"uploadToken": item.Token, "fileName": item.Name}}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	_, body, err := c.request(ctx, "POST", base+"mediaItems:batchCreate", map[string]string{"Content-Type": "application/json"}, data)
	if err != nil {
		return nil, err
	}
	var response struct {
		Results []struct {
			Token  string `json:"uploadToken"`
			Status struct {
				Code int `json:"code"`
			} `json:"status"`
			Media Media `json:"mediaItem"`
		} `json:"newMediaItemResults"`
	}
	if json.Unmarshal(body, &response) != nil || len(response.Results) > len(items) {
		return nil, &OutcomeUnknownError{}
	}
	results := make([]CreateResult, len(items))
	for i, item := range items {
		results[i].Err = &OutcomeUnknownError{}
		if i >= len(response.Results) {
			continue
		}
		result := response.Results[i]
		// Google promises input order. Never assign an ID when echoed tokens differ.
		// Retain compatibility with single-item responses that omit the echo.
		if result.Token != item.Token && !(len(items) == 1 && result.Token == "") {
			continue
		}
		if result.Status.Code != 0 {
			results[i].Err = &ItemError{Code: result.Status.Code}
			continue
		}
		if result.Media.ID == "" {
			continue
		}
		results[i] = CreateResult{Media: result.Media}
	}
	return results, nil
}
