// Package msgraph provides read-only OneDrive access through Microsoft Graph.
package msgraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"od2gp/internal/onedrive"
)

const endpoint = "https://graph.microsoft.com/v1.0"
const fields = "id,name,size,file,folder,package,remoteItem,eTag,cTag,lastModifiedDateTime"

type Client struct {
	HTTP                 *http.Client
	Drive                string
	ExcludePersonalVault bool
	downloadTransport    http.RoundTripper
}
type Drive struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"driveType"`
}
type resource struct {
	ID, Name string
	Size     int64
	ETag     string `json:"eTag"`
	CTag     string `json:"cTag"`
	Modified string `json:"lastModifiedDateTime"`
	File     *struct {
		MIME string `json:"mimeType"`
	} `json:"file"`
	Folder  *struct{}       `json:"folder"`
	Package *struct{}       `json:"package"`
	Remote  json.RawMessage `json:"remoteItem"`
}

func (r resource) item(name string) onedrive.Item {
	item := onedrive.Item{ID: r.ID, Name: r.Name, Path: name, Size: r.Size, ModTime: r.Modified, IsDir: r.Folder != nil && r.Package == nil, Hashes: map[string]string{}}
	if r.File != nil {
		item.MimeType = r.File.MIME
	}
	if r.ETag != "" {
		item.Hashes["graphETag"] = r.ETag
	}
	if r.CTag != "" {
		item.Hashes["graphCTag"] = r.CTag
	}
	return item
}
func validAPI(address string) bool {
	u, err := url.Parse(address)
	return err == nil && u.Scheme == "https" && u.Host == "graph.microsoft.com" && u.User == nil && u.Fragment == "" && strings.HasPrefix(u.Path, "/v1.0/")
}
func (c Client) request(ctx context.Context, address string) (*http.Response, error) {
	if !validAPI(address) {
		return nil, errors.New("refusing an untrusted Graph API or pagination URL")
	}
	if c.HTTP == nil {
		return nil, errors.New("Microsoft Graph is not signed in")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, errors.New("invalid Graph request")
	}
	// Disallow authenticated redirects even if an injected client follows them.
	client := *c.HTTP
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("Microsoft Graph connection failed; saved progress is kept")
	}
	return resp, nil
}
func apiFailure(resp *http.Response) error {
	// Do not include response bodies, source paths or credential-bearing URLs.
	switch resp.StatusCode {
	case 401:
		return errors.New("Microsoft Graph sign-in expired; reconnect the same OneDrive account")
	case 403:
		return errors.New("Microsoft Graph denied source access; check Files.Read consent and the selected drive")
	case 404:
		return errors.New("Microsoft Graph source item or drive was not found")
	case 429, 503:
		return errors.New("Microsoft Graph requested a pause; retry later (no automatic early retry)")
	default:
		return fmt.Errorf("Microsoft Graph request failed (HTTP %d); saved progress is kept", resp.StatusCode)
	}
}
func (c Client) get(ctx context.Context, address string, value any) error {
	request, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := c.request(request, address)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return apiFailure(resp)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil || len(data) > 8<<20 {
		return errors.New("Graph metadata response interrupted or too large")
	}
	if json.Unmarshal(data, value) != nil {
		return errors.New("invalid Graph metadata response")
	}
	return nil
}
func (c Client) DefaultDrive(ctx context.Context) (Drive, error) {
	var drive Drive
	if err := c.get(ctx, endpoint+"/me/drive", &drive); err != nil {
		return drive, err
	}
	if drive.ID == "" {
		return drive, errors.New("Microsoft did not return a default drive; use source select-drive")
	}
	return drive, nil
}

func (c Client) Drives(ctx context.Context) ([]Drive, error) {
	var main Drive
	mainErr := c.get(ctx, endpoint+"/me/drive", &main)
	var drives []Drive
	if mainErr == nil && main.ID != "" {
		drives = append(drives, main)
	}
	address := endpoint + "/me/drives"
	seen := map[string]bool{}
	if main.ID != "" {
		seen[main.ID] = true
	}
	pages := map[string]bool{}
	for address != "" {
		if pages[address] {
			return nil, errors.New("Graph drive pagination repeated a page")
		}
		pages[address] = true
		var page struct {
			Value []Drive
			Next  string `json:"@odata.nextLink"`
		}
		if err := c.get(ctx, address, &page); err != nil {
			if len(drives) > 0 {
				return drives, nil
			}
			return nil, err
		}
		if page.Value == nil {
			return nil, errors.New("Graph did not return a drive collection")
		}
		for _, d := range page.Value {
			if d.ID != "" && !seen[d.ID] {
				drives = append(drives, d)
				seen[d.ID] = true
			}
		}
		address = page.Next
	}
	if len(drives) == 0 {
		if mainErr != nil {
			return nil, mainErr
		}
		return nil, errors.New("no OneDrive drives were returned")
	}
	return drives, nil
}
func (c Client) DriveID(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if c.Drive == "" {
		return "", errors.New("choose a OneDrive drive first")
	}
	return c.Drive, nil
}
func (c Client) root() string { return endpoint + "/drives/" + url.PathEscape(c.Drive) + "/root" }
func sourcePath(name string) (string, error) {
	name = strings.Trim(name, "/")
	if strings.ContainsAny(name, "\\\x00") {
		return "", errors.New("use a OneDrive path with forward slashes")
	}
	var escaped []string
	for _, part := range strings.Split(name, "/") {
		if part == ".." || part == "." {
			return "", errors.New("relative source path segments are not supported")
		}
		if part != "" {
			escaped = append(escaped, url.PathEscape(part))
		}
	}
	return strings.Join(escaped, "/"), nil
}
func (c Client) resourceURL(name string) (string, error) {
	if c.Drive == "" {
		return "", errors.New("choose a OneDrive drive first")
	}
	encoded, err := sourcePath(name)
	if err != nil {
		return "", err
	}
	if encoded == "" {
		return c.root(), nil
	}
	return c.root() + ":/" + encoded + ":", nil
}
func (c Client) Check(ctx context.Context) error {
	var r resource
	if err := c.get(ctx, c.root()+"?$select=id,folder", &r); err != nil {
		return err
	}
	if r.ID == "" || r.Folder == nil {
		return errors.New("Graph did not return an accessible drive root")
	}
	return nil
}
func (c Client) Stat(ctx context.Context, name string) (onedrive.Item, error) {
	address, err := c.resourceURL(name)
	if err != nil {
		return onedrive.Item{}, err
	}
	var r resource
	if err := c.get(ctx, address+"?$select="+fields, &r); err != nil {
		return onedrive.Item{}, err
	}
	if r.ID == "" || r.Folder != nil || r.File == nil || len(r.Remote) > 0 && string(r.Remote) != "null" {
		return onedrive.Item{}, errors.New("Graph source is not a supported ordinary file")
	}
	if r.ETag == "" && r.CTag == "" {
		return onedrive.Item{}, errors.New("Graph omitted file version metadata; refusing an unverified source")
	}
	return r.item(strings.Trim(name, "/")), nil
}
func (c Client) Walk(ctx context.Context, folder string, visit func(onedrive.Item) error) error {
	return c.List(ctx, folder, true, visit)
}
func (c Client) List(ctx context.Context, folder string, recursive bool, visit func(onedrive.Item) error) error {
	folder = strings.Trim(folder, "/")
	if c.ExcludePersonalVault && (folder == "Personal Vault" || strings.HasPrefix(folder, "Personal Vault/")) {
		return errors.New("Personal Vault is excluded from this job")
	}
	address, err := c.resourceURL(folder)
	if err != nil {
		return err
	}
	type directory struct{ address, prefix string }
	pending := []directory{{address, ""}}
	seen := map[string]bool{}
	for len(pending) > 0 {
		dir := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		next := dir.address + "/children?$top=200&$select=" + fields
		pages := map[string]bool{}
		for next != "" {
			if err := ctx.Err(); err != nil {
				return err
			}
			if pages[next] {
				return errors.New("Graph inventory pagination repeated a page")
			}
			pages[next] = true
			var page struct {
				Value []resource
				Next  string `json:"@odata.nextLink"`
			}
			if err := c.get(ctx, next, &page); err != nil {
				return err
			}
			if page.Value == nil {
				return errors.New("Graph did not return an inventory collection")
			}
			for _, r := range page.Value {
				if r.Name == "" || r.Name == "." || r.Name == ".." || strings.ContainsAny(r.Name, "/\\\x00") || r.ID == "" {
					return errors.New("Graph inventory contains an unsafe or unidentified source item")
				}
				name := path.Join(dir.prefix, r.Name)
				if folder == "" && name == "Personal Vault" {
					if c.ExcludePersonalVault {
						continue
					}
					return errors.New("Personal Vault needs separate handling; use --exclude-personal-vault explicitly to migrate the rest")
				}
				if len(r.Remote) > 0 && string(r.Remote) != "null" {
					return errors.New("Graph inventory contains a remote shortcut; choose an ordinary source folder (shared shortcuts are not supported)")
				}
				if seen[r.ID] {
					return errors.New("Graph inventory returned a repeated item; restart discovery rather than guessing")
				}
				seen[r.ID] = true
				if r.File != nil && r.ETag == "" && r.CTag == "" {
					return errors.New("Graph omitted file version metadata; scan is incomplete")
				}
				item := r.item(name)
				if item.IsDir {
					if !recursive {
						if err := visit(item); err != nil {
							return err
						}
					}
					if recursive {
						pending = append(pending, directory{endpoint + "/drives/" + url.PathEscape(c.Drive) + "/items/" + url.PathEscape(r.ID), name})
					}
				} else {
					if err := visit(item); err != nil {
						return err
					}
				}
			}
			next = page.Next
		}
	}
	return nil
}
func safeDownload(address string) bool {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	ip := net.ParseIP(host)
	return host != "localhost" && !strings.HasSuffix(host, ".localhost") && (ip == nil || !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsUnspecified() && !ip.IsLinkLocalUnicast())
}
func (c Client) Read(ctx context.Context, name string, offset int64, out io.Writer) error {
	if offset < 0 {
		return errors.New("invalid download offset")
	}
	address, err := c.resourceURL(name)
	if err != nil {
		return err
	}
	resp, err := c.request(ctx, address+"/content")
	if err != nil {
		return err
	}
	location := resp.Header.Get("Location")
	resp.Body.Close()
	if resp.StatusCode != 302 && resp.StatusCode != 303 && resp.StatusCode != 307 {
		return apiFailure(resp)
	}
	if !safeDownload(location) {
		return errors.New("Graph returned an unsafe download URL")
	}
	// Download URLs are preauthenticated. Never use the OAuth transport here.
	transport := c.downloadTransport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client := &http.Client{Timeout: 10 * time.Minute, Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !safeDownload(req.URL.String()) {
			return errors.New("unsafe download redirect")
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return errors.New("invalid download request")
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	content, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("OneDrive download interrupted; saved progress is kept")
	}
	defer content.Body.Close()
	if offset > 0 {
		expected := "bytes " + strconv.FormatInt(offset, 10) + "-"
		if content.StatusCode != 206 || !strings.HasPrefix(content.Header.Get("Content-Range"), expected) {
			return errors.New("source did not honor the download offset")
		}
	} else if content.StatusCode != 200 {
		return errors.New("OneDrive content download failed")
	}
	_, err = io.Copy(out, content.Body)
	if err != nil {
		return errors.New("OneDrive content stream or staging write failed")
	}
	return nil
}
