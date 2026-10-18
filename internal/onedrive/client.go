package onedrive

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/zalando/go-keyring"
)

const remote = "onedrive"
const service = "od2gp"
const passwordAccount = "rclone-config-password"

type Client struct {
	Binary, Config, Helper string
	// Diagnostic output is opt-in for read-only source scans only.
	Diagnostics          io.Writer
	ExcludePersonalVault bool
}
type Item struct {
	Path     string            `json:"Path"`
	Name     string            `json:"Name"`
	Size     int64             `json:"Size"`
	MimeType string            `json:"MimeType"`
	ModTime  string            `json:"ModTime"`
	IsDir    bool              `json:"IsDir"`
	ID       string            `json:"ID"`
	Hashes   map[string]string `json:"Hashes"`
}
type Question struct {
	State  string
	Error  string
	Option struct {
		Name, Help string
		Type       string
		Exclusive  bool
		Default    any
		IsPassword bool
		Examples   []struct{ Value, Help string }
	}
}

func New(binary, config string) (Client, error) {
	helper, err := os.Executable()
	if err != nil {
		return Client{}, err
	}
	return Client{Binary: binary, Config: config, Helper: helper}, nil
}

// Password is only emitted to rclone's anonymous child-process pipe.
func Password() (string, error) {
	password, err := keyring.Get(service, passwordAccount)
	if err != nil || password == "" {
		return "", errors.New("cannot read rclone encryption key from the OS credential store")
	}
	return password, nil
}
func (c Client) command(ctx context.Context, args ...string) *exec.Cmd {
	helper := `"` + strings.ReplaceAll(c.Helper, `\`, `/`) + `" internal-rclone-password`
	global := []string{"--config", c.Config, "--password-command", helper, "--log-level", "ERROR"}
	command := exec.CommandContext(ctx, c.Binary, append(global, args...)...)
	// Do not let inherited rclone settings override this app's dedicated remote/security.
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.TrimLeft(strings.ToUpper(name), "_"), "RCLONE_") {
			command.Env = append(command.Env, entry)
		}
	}
	return command
}

type cappedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, errors.New("rclone output exceeds limit")
	}
	return b.Buffer.Write(p)
}
func (c Client) output(ctx context.Context, args ...string) ([]byte, error) {
	cmd := c.command(ctx, args...)
	out := &cappedBuffer{limit: 2 << 20}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, errors.New("rclone request failed; check source sign-in, connectivity and permissions")
	}
	return out.Bytes(), nil
}
func (c Client) Prepare(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(c.Config), 0700); err != nil {
		return err
	}
	if existing, err := os.ReadFile(c.Config); err == nil {
		if !encryptedHeader(existing) {
			return errors.New("refusing to use an unencrypted source config")
		}
		_, err := Password()
		return err
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("cannot inspect rclone config")
	}
	// Reuse an existing key after an interrupted initial setup.
	_, err := Password()
	if err != nil {
		var random [32]byte
		if _, err := rand.Read(random[:]); err != nil {
			return err
		}
		if err := keyring.Set(service, passwordAccount, hex.EncodeToString(random[:])); err != nil {
			return errors.New("cannot secure rclone configuration key")
		}
	}
	file, err := os.OpenFile(c.Config, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	file.Close()
	if _, err := c.output(ctx, "config", "encryption", "set"); err != nil {
		// Empty config contains no user data; remove only that known initial placeholder.
		if data, readErr := os.ReadFile(c.Config); readErr == nil && len(data) == 0 {
			os.Remove(c.Config)
		}
		return errors.New("cannot initialize encrypted rclone config; setup stopped before source login")
	}
	data, err := os.ReadFile(c.Config)
	if err != nil || !encryptedHeader(data) {
		return errors.New("rclone config encryption could not be verified")
	}
	return nil
}
func (c Client) Configure(ctx context.Context, ask func(Question) (string, error)) error {
	if err := c.Prepare(ctx); err != nil {
		return err
	}
	data, err := c.output(ctx, "listremotes")
	if err != nil {
		return err
	}
	exists := false
	for _, name := range strings.Fields(string(data)) {
		if name == remote+":" {
			exists = true
		}
	}
	if exists && c.Check(ctx) == nil {
		return nil
	}
	return configureRemote(ctx, exists, c.output, ask)
}
func configureRemote(ctx context.Context, exists bool, output func(context.Context, ...string) ([]byte, error), ask func(Question) (string, error), extra ...string) error {
	defaults := []string{"access_scopes", "Files.Read offline_access", "disable_site_permission", "false", "client_id", "", "client_secret", "", "config_is_local", "true"}
	if len(extra)%2 != 0 {
		return errors.New("invalid rclone configuration options")
	}
	for i := 0; i < len(extra); i += 2 {
		found := false
		for j := 0; j < len(defaults); j += 2 {
			if defaults[j] == extra[i] {
				defaults[j+1] = extra[i+1]
				found = true
				break
			}
		}
		if !found {
			defaults = append(defaults, extra[i:i+2]...)
		}
	}
	args := append([]string{"config", "create", remote, "onedrive", "--non-interactive"}, defaults...)
	if exists {
		args = append([]string{"config", "update", remote, "--non-interactive"}, defaults...)
	}
	for step := 0; step < 30; step++ {
		data, err := output(ctx, args...)
		if err != nil {
			return err
		}
		var question Question
		if json.Unmarshal(data, &question) != nil {
			return errors.New("unexpected rclone configuration response")
		}
		// ConfigError can contain an Error with a null Option and a state that
		// leads back to the drive picker. Never silently follow that transition.
		if question.Error != "" {
			if strings.Contains(strings.ToLower(question.Error), "root for drive") {
				return errors.New("OneDrive could not open the selected drive. Retry and choose another drive, usually OneDrive (personal). If every drive fails, reconnect OneDrive")
			}
			return errors.New("OneDrive configuration was rejected. Check the connection and source permissions, then retry or reconnect OneDrive")
		}
		if question.State == "" {
			return nil
		}
		// Advance successful transitions without inventing an empty prompt.
		if question.Option.Name == "" {
			args = append([]string{"config", "update", remote, "--non-interactive", "--continue", "--state", question.State, "--result", ""}, defaults...)
			continue
		}
		if question.Option.IsPassword || question.Option.Name == "config_token" || strings.Contains(strings.ToLower(question.Option.Name), "secret") {
			return errors.New("unexpected password prompt during OneDrive configuration")
		}
		answer, err := ask(question)
		if err != nil {
			return err
		}
		args = append([]string{"config", "update", remote, "--non-interactive", "--continue", "--state", question.State, "--result", answer}, defaults...)
	}
	return errors.New("rclone configuration did not finish within the supported number of questions")
}
func (c Client) Check(ctx context.Context) error {
	if err := c.validateConfig(); err != nil {
		return err
	}
	_, err := c.output(ctx, "lsjson", remote+":", "--stat")
	return err
}
func (c Client) Logout(ctx context.Context) error {
	_, err := c.output(ctx, "config", "delete", remote)
	return err
}
func (c Client) Walk(ctx context.Context, folder string, visit func(Item) error) error {
	return c.list(ctx, folder, []string{"--recursive", "--files-only", "--hash"}, visit)
}

// List streams metadata without hashes or content reads. The visitor may stop
// the listing early; recursive=false includes immediate directories and files.
func (c Client) List(ctx context.Context, folder string, recursive bool, visit func(Item) error) error {
	options := []string{"--no-modtime"}
	if recursive {
		options = append(options, "--recursive", "--files-only")
	}
	return c.list(ctx, folder, options, visit)
}

func (c Client) list(ctx context.Context, folder string, options []string, visit func(Item) error) error {
	if err := c.validateConfig(); err != nil {
		return err
	}
	if strings.HasPrefix(folder, "/") {
		folder = strings.TrimPrefix(folder, "/")
	}
	if strings.ContainsRune(folder, 0) {
		return errors.New("invalid source folder")
	}
	// The final command failure is logged at NOTICE by rclone, not ERROR.
	args, err := inventoryArgs(folder, options, c.ExcludePersonalVault)
	if err != nil {
		return err
	}
	cmd := c.command(ctx, args...)
	return runInventoryCommand(ctx, cmd, visit, c.Diagnostics)
}

func inventoryArgs(folder string, options []string, excludeVault bool) ([]string, error) {
	args := append([]string{"lsjson", remote + ":" + folder, "--log-level", "NOTICE"}, options...)
	if excludeVault {
		if folder == "Personal Vault" || strings.HasPrefix(folder, "Personal Vault/") {
			return nil, errors.New("Personal Vault is excluded; choose an accessible source folder")
		}
		if folder == "" {
			// Anchor to the actual drive root. Do not filter ordinary nested folders
			// named Personal Vault when listing a selected Pictures folder.
			args = append(args, "--disable", "ListR", "--exclude", "/Personal Vault/**")
		}
	}
	return args, nil
}

func runInventoryCommand(ctx context.Context, cmd *exec.Cmd, visit func(Item) error, diagnosticOut ...io.Writer) error {
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	diagnostic := &inventoryDiagnostics{}
	cmd.Stderr = diagnostic
	if err := cmd.Start(); err != nil {
		return errors.New("cannot start rclone")
	}
	finished := false
	defer func() {
		if !finished {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	count := 0
	visitorFailed := false
	stream := &inventoryReader{Reader: pipe}
	decodeErr := decodeInventory(stream, func(item Item) error {
		if err := visit(item); err != nil {
			visitorFailed = true
			return err
		}
		count++
		return nil
	})
	// A visitor may stop early (e.g. a bounded preview). Kill before waiting so
	// the producer cannot block forever writing to a pipe we no longer consume.
	if decodeErr != nil && !stream.eof {
		cmd.Process.Kill()
	}
	processErr := cmd.Wait()
	finished = true
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if visitorFailed {
		return decodeErr
	}
	if processErr != nil && len(diagnosticOut) > 0 && diagnosticOut[0] != nil {
		if len(diagnostic.data) > 0 {
			fmt.Fprintln(diagnosticOut[0], "Source diagnostic (may contain folder/file names; review before sharing):")
			fmt.Fprintln(diagnosticOut[0], redactInventoryDiagnostic(string(diagnostic.data)))
		} else {
			fmt.Fprintln(diagnosticOut[0], "Rclone produced no diagnostic output.")
		}
	}
	if processErr != nil && (decodeErr == nil || stream.eof) {
		return inventoryProcessError(diagnostic.data, count, processErr)
	}
	return decodeErr
}

var diagnosticURL = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)
var diagnosticCredential = regexp.MustCompile(`(?i)(["']?(?:access_token|refresh_token|client_secret|password|authorization|token|secret)["']?\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,}]+)`)
var diagnosticBearer = regexp.MustCompile(`(?i)bearer\s+[^\s,"'}]+`)

func redactInventoryDiagnostic(text string) string {
	text = diagnosticURL.ReplaceAllString(text, "[URL redacted]")
	text = diagnosticBearer.ReplaceAllString(text, "Bearer [redacted]")
	return diagnosticCredential.ReplaceAllString(text, "$1[redacted]")
}

// Keep only a bounded in-memory tail. Never forward raw diagnostics because
// provider messages can include filenames, URLs or credential material.
type inventoryDiagnostics struct{ data []byte }

func (b *inventoryDiagnostics) Write(p []byte) (int, error) {
	const limit = 16 << 10
	n := len(p)
	if n >= limit {
		b.data = append(b.data[:0], p[n-limit:]...)
		return n, nil
	}
	if len(b.data)+n > limit {
		b.data = b.data[len(b.data)+n-limit:]
	}
	b.data = append(b.data, p...)
	return n, nil
}

func inventoryProcessError(diagnostic []byte, count int, err error) error {
	text := strings.ToLower(string(diagnostic))
	reason := "source listing failed"
	switch {
	case strings.Contains(text, "personal vault") && strings.Contains(text, "objecthandle is invalid"):
		reason = "Personal Vault cannot be listed through this source connection; explicitly use --exclude-personal-vault to migrate the rest of the drive"
	case strings.Contains(text, "failed to marshal"), strings.Contains(text, "marshaljson"):
		reason = "rclone could not encode a source item's metadata"
	case strings.Contains(text, "invalid_grant"), strings.Contains(text, "invalidauthenticationtoken"), strings.Contains(text, "unauthenticated"), strings.Contains(text, "401 unauthorized"):
		reason = "OneDrive sign-in expired or was rejected; reconnect the same account"
	case strings.Contains(text, "accessdenied"), strings.Contains(text, "access denied"), strings.Contains(text, "403 forbidden"):
		reason = "OneDrive denied access to a source item or folder"
	case strings.Contains(text, "itemnotfound"), strings.Contains(text, "404 not found"), strings.Contains(text, "directory not found"):
		reason = "a source item or folder could not be found"
	case strings.Contains(text, "429"), strings.Contains(text, "throttl"), strings.Contains(text, "too many requests"):
		reason = "OneDrive throttled the listing; wait before retrying"
	case strings.Contains(text, "timeout"), strings.Contains(text, "timed out"), strings.Contains(text, "connection reset"), strings.Contains(text, "unexpected eof"), strings.Contains(text, "no such host"), strings.Contains(text, "503"), strings.Contains(text, "502"):
		reason = "the source connection or service was interrupted"
	}
	code := ""
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = fmt.Sprintf(" (rclone exit %d)", exit.ExitCode())
	}
	return fmt.Errorf("inventory stopped after %d items: %s%s. The scan is incomplete; no uploads were started by this scan", count, reason, code)
}

type inventoryReader struct {
	io.Reader
	eof bool
}

func (r *inventoryReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if errors.Is(err, io.EOF) {
		r.eof = true
	}
	return n, err
}

// decodeInventory never skips malformed entries: a partial scan cannot become a
// complete plan. Diagnostics expose positions and field names, not source data.
func decodeInventory(input io.Reader, visit func(Item) error) error {
	stream := &inventoryReader{Reader: input}
	decoder := json.NewDecoder(stream)
	token, err := decoder.Token()
	if err != nil {
		return inventoryDecodeErrorAtEOF(err, 0, stream.eof)
	}
	if token != json.Delim('[') {
		return errors.New("rclone inventory is not a JSON array")
	}
	count := 0
	for decoder.More() {
		var item Item
		if err := decoder.Decode(&item); err != nil {
			return inventoryDecodeErrorAtEOF(err, count, stream.eof)
		}
		if err := visit(item); err != nil {
			return err
		}
		count++
	}
	token, err = decoder.Token()
	if err != nil {
		return inventoryDecodeErrorAtEOF(err, count, stream.eof)
	}
	if token != json.Delim(']') {
		return errors.New("rclone inventory has no closing array")
	}
	return nil
}

func inventoryDecodeErrorAtEOF(err error, count int, eof bool) error {
	if eof {
		return inventoryDecodeError(io.ErrUnexpectedEOF, count)
	}
	return inventoryDecodeError(err, count)
}

func inventoryDecodeError(err error, count int) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("rclone inventory stream ended early after %d items; resume the saved job to restart the scan (no files were uploaded by planning)", count)
	}
	var field *json.UnmarshalTypeError
	if errors.As(err, &field) {
		return fmt.Errorf("rclone inventory item %d has an unexpected value for field %q (expected %s); scan stopped without skipping it", count+1, field.Field, field.Type)
	}
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		return fmt.Errorf("rclone inventory contains invalid JSON near item %d at byte %d; scan stopped without skipping it", count+1, syntax.Offset)
	}
	return fmt.Errorf("cannot read rclone inventory after %d items; check connectivity and resume the saved job", count)
}

func (c Client) Read(ctx context.Context, path string, offset int64, writer io.Writer) error {
	if err := c.validateConfig(); err != nil {
		return err
	}
	if offset < 0 || strings.ContainsRune(path, 0) {
		return errors.New("invalid source read")
	}
	cmd := c.command(ctx, "cat", remote+":"+strings.TrimPrefix(path, "/"), "--offset", fmt.Sprint(offset))
	cmd.Stdout = writer
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return errors.New("source download interrupted")
	}
	return nil
}

func encryptedHeader(data []byte) bool {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		return line == "RCLONE_ENCRYPT_V0:"
	}
	return false
}

func (c Client) validateConfig() error {
	file, err := os.Open(c.Config)
	if err != nil {
		return errors.New("OneDrive is not configured; run auth login onedrive")
	}
	defer file.Close()
	header := make([]byte, 256)
	n, _ := file.Read(header)
	if !encryptedHeader(header[:n]) {
		return errors.New("source config is not encrypted; refusing access")
	}
	return nil
}

// DriveID is obtained from the dedicated decrypted config only in memory.
func (c Client) DriveID(ctx context.Context) (string, error) {
	drive, err := c.driveSettings(ctx)
	if err != nil {
		return "", err
	}
	return drive["drive_id"], nil
}

// rawSettings returns the raw key/value map for the remote without validating
// drive_id or scope. Use when a valid token is needed before a drive is chosen.
func (c Client) rawSettings(ctx context.Context) (map[string]string, error) {
	if err := c.validateConfig(); err != nil {
		return nil, err
	}
	return c.rawSettingsFromOutput(ctx, c.output)
}

func (c Client) rawSettingsFromOutput(ctx context.Context, output func(context.Context, ...string) ([]byte, error)) (map[string]string, error) {
	data, err := output(ctx, "config", "dump")
	if err != nil {
		return nil, err
	}
	var config map[string]map[string]string
	if json.Unmarshal(data, &config) != nil {
		return nil, errors.New("invalid source identity response")
	}
	drive := config[remote]
	if drive["type"] != "onedrive" {
		return nil, errors.New("OneDrive is not configured; run auth login onedrive")
	}
	return drive, nil
}

func (c Client) driveSettings(ctx context.Context) (map[string]string, error) {
	if err := c.validateConfig(); err != nil {
		return nil, err
	}
	data, err := c.output(ctx, "config", "dump")
	if err != nil {
		return nil, err
	}
	var config map[string]map[string]string
	if json.Unmarshal(data, &config) != nil {
		return nil, errors.New("invalid source identity response")
	}
	drive := config[remote]
	if drive["type"] != "onedrive" || drive["drive_id"] == "" {
		return nil, errors.New("source drive identity unavailable")
	}
	if drive["access_scopes"] != "Files.Read offline_access" || drive["disable_site_permission"] == "true" {
		return nil, errors.New("source permissions changed; rerun auth login onedrive with read-only configuration")
	}
	return drive, nil
}
func (c Client) Stat(ctx context.Context, path string) (Item, error) {
	var item Item
	data, err := c.output(ctx, "lsjson", remote+":"+strings.TrimPrefix(path, "/"), "--stat", "--hash")
	if err != nil {
		return item, err
	}
	if json.Unmarshal(data, &item) != nil || item.IsDir || item.ID == "" {
		return item, errors.New("source file metadata unavailable")
	}
	return item, nil
}
