package googlephotos

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
)

// ListAppMedia enumerates only the media exposed by the app-created-data scope.
func (c Client) ListAppMedia(ctx context.Context, visit func(Media) error) error {
	token := ""
	seen := map[string]bool{}
	for {
		address := base + "mediaItems?pageSize=100"
		if token != "" {
			address += "&pageToken=" + url.QueryEscape(token)
		}
		_, data, err := c.request(ctx, "GET", address, nil, nil)
		if err != nil {
			return err
		}
		var page struct {
			Items []Media `json:"mediaItems"`
			Next  string  `json:"nextPageToken"`
		}
		if json.Unmarshal(data, &page) != nil {
			return errors.New("invalid Google media listing")
		}
		for _, m := range page.Items {
			if m.ID == "" {
				return errors.New("Google media listing omitted ID")
			}
			if err := visit(m); err != nil {
				return err
			}
		}
		if page.Next == "" {
			return nil
		}
		if seen[page.Next] {
			return errors.New("Google media listing repeated page")
		}
		seen[page.Next] = true
		token = page.Next
	}
}
