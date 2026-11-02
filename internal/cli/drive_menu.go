package cli

import (
	"bytes"
	"fmt"
	"io"
	"od2gp/internal/drivemigration"
	"od2gp/internal/journal"
	"od2gp/internal/migration"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (a *menuApp) driveRunArgs(id string) []string {
	workers := a.currentOptions().DriveWorkers
	if workers == "" {
		workers = "auto"
	}
	return []string{"drive", "resume", "--job", id, "--workers", workers}
}
func (a *menuApp) newDrivePlan() {
	if a.execute([]string{"source", "select-drive"}, a.out) != 0 {
		return
	}
	folder, e := a.ask("OneDrive folder", "/")
	if e != nil {
		return
	}
	name, e := a.ask(a.dt("New Google Drive root name", "Tên thư mục gốc mới trên Google Drive"), "OneDrive Backup")
	if e != nil {
		return
	}
	cache, ok := a.transferNumber("Maximum staging space in GiB", 30, 1, 1024)
	if !ok {
		return
	}
	args := []string{"drive", "plan", "--source", folder, "--name", name, "--cache-gib", strconv.Itoa(cache)}
	if normalizeMenuFolder(folder) == "" {
		exclude, ok := a.chooseVaultPolicy()
		if !ok {
			return
		}
		if exclude {
			args = append(args, "--exclude-personal-vault")
		}
	}
	var capture bytes.Buffer
	if a.execute(args, io.MultiWriter(a.out, &capture)) != 0 {
		return
	}
	for _, line := range strings.Split(capture.String(), "\n") {
		if id, ok := strings.CutPrefix(line, "Drive job: "); ok && migration.ValidID(id) {
			a.execute(a.driveRunArgs(id), a.out)
			return
		}
	}
}
func (a *menuApp) savedDriveAction(root, choice string) {
	entries, e := os.ReadDir(filepath.Join(root, "od2gp", "drive-jobs"))
	if e != nil {
		fmt.Fprintln(a.out, a.dt("No readable Drive jobs.", "Chưa có tác vụ Drive đọc được."))
		return
	}
	var ids []string
	var jobs []drivemigration.Meta
	for _, entry := range entries {
		if !entry.IsDir() || !migration.ValidID(entry.Name()) {
			continue
		}
		id := entry.Name()
		key, e := journal.Key(id, false)
		if e != nil {
			fmt.Fprintf(a.out, "%s: %s\n", id, e)
			continue
		}
		s, e := journal.Open(filepath.Join(root, "od2gp", "drive-jobs", id, "progress.db"), key, false)
		if e != nil {
			continue
		}
		var m drivemigration.Meta
		e = s.Load(0, &m)
		s.Close()
		if e != nil || m.Kind != drivemigration.Kind {
			continue
		}
		ids = append(ids, id)
		jobs = append(jobs, m)
		fmt.Fprintf(a.out, "%d) /%s → /%s (%s)\n", len(ids), m.Folder, m.RootName, id[:8])
	}
	if len(ids) == 0 {
		return
	}
	c, e := a.ask("Choose", "0")
	if e != nil {
		return
	}
	n, e := strconv.Atoi(c)
	if e != nil || n < 1 || n > len(ids) {
		return
	}
	id := ids[n-1]
	switch choice {
	case "4":
		args, ok := a.driveResumeScopeArgs(jobs[n-1])
		if ok {
			a.execute(args, a.out)
		}
	case "5":
		a.execute([]string{"drive", "status", "--job", id}, a.out)
	case "6":
		file, e := a.ask(a.dt("Report CSV path", "Đường dẫn báo cáo CSV"), "drive-"+id[:8]+".csv")
		if e != nil {
			return
		}
		f, e := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			fmt.Fprintln(a.errOut, e)
			return
		}
		code := a.execute([]string{"drive", "report", "--job", id, "--format", "csv"}, f)
		if e = f.Close(); e != nil {
			fmt.Fprintln(a.errOut, e)
			return
		}
		if code == 0 {
			fmt.Fprintln(a.out, a.dt("Report saved:", "Đã lưu báo cáo:"), file)
		}
	}
}
func (a *menuApp) configureDriveWorkers() {
	opts := a.currentOptions()
	current := opts.DriveWorkers
	if current == "" {
		current = "auto"
	}
	fmt.Fprintln(a.out, a.dt("Drive workers: auto starts at 3 and adjusts 1–8; a fixed count is also available.", "Workers Drive: auto bắt đầu 3, tự điều chỉnh 1–8; có thể chọn số cố định."))
	value, err := a.ask(a.dt("auto or 1–8", "auto hoặc 1–8"), current)
	if err != nil {
		return
	}
	opts.DriveWorkers = value
	if !opts.valid() {
		fmt.Fprintln(a.errOut, a.dt("Use auto or a whole number 1–8.", "Nhập auto hoặc số nguyên 1–8."))
		return
	}
	if a.saveOptions != nil {
		if err := a.saveOptions(opts); err != nil {
			fmt.Fprintln(a.errOut, err)
			return
		}
	}
	a.options = opts
}
func (a *menuApp) driveLogout() {
	fmt.Fprintln(a.out, a.dt("1) OneDrive\n2) Google Drive\n0) Back\nSaved jobs require their original accounts to resume. Cloud files will be kept.", "1) OneDrive\n2) Google Drive\n0) Quay lại\nJob đã lưu cần đúng tài khoản ban đầu để tiếp tục. File cloud được giữ nguyên."))
	choice, err := a.ask("Choose", "0")
	if err != nil || choice == "0" {
		return
	}
	provider := ""
	if choice == "1" {
		provider = "onedrive"
	}
	if choice == "2" {
		provider = "gdrive"
	}
	if provider == "" {
		return
	}
	answer, err := a.ask(a.dt("Remove local credential? Type yes", "Xóa credential cục bộ? Nhập yes"), "no")
	if err != nil || answer != "yes" {
		return
	}
	a.execute([]string{"auth", "logout", provider}, a.out)
}

func (a *menuApp) driveResumeScopeArgs(m drivemigration.Meta) ([]string, bool) {
	args := a.driveRunArgs(m.ID)
	if m.InventoryComplete {
		return args, true
	}
	fmt.Fprintln(a.out, a.dt("Inventory is incomplete.\n1) Continue the saved scope\n2) Exclude a specific source path, then continue\n0) Back", "Inventory chưa hoàn tất.\n1) Tiếp tục phạm vi đã lưu\n2) Loại trừ một đường dẫn nguồn rồi tiếp tục\n0) Quay lại"))
	choice, err := a.ask("Choose", "1")
	if err != nil {
		return nil, false
	}
	switch choice {
	case "1":
		return args, true
	case "2":
		fmt.Fprintln(a.out, a.dt("Enter the full OneDrive path, including the selected source folder. The path and all descendants will NOT be copied; count and size remain unknown. The choice is saved with this job. Cached root delta pages are retained.", "Nhập đường dẫn đầy đủ trong OneDrive, bao gồm thư mục nguồn đã chọn. Mục này và toàn bộ nội dung bên trong sẽ KHÔNG được chép; số file và dung lượng chưa xác định. Lựa chọn được lưu vào job. Các trang delta gốc đã tải được giữ lại."))
		p, err := a.ask(a.dt("Source path to exclude", "Đường dẫn nguồn cần loại trừ"), "")
		if err != nil || strings.TrimSpace(p) == "" {
			return nil, false
		}
		return append(args, "--exclude-path", p), true
	default:
		return nil, false
	}
}
