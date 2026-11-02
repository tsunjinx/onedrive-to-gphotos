package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"od2gp/internal/identity"
	"od2gp/internal/settings"
	"path/filepath"
	"strings"
)

func driveCommand(ctx context.Context, args []string, input io.Reader, out, errOut io.Writer, getenv func(string) string, root string) int {
	if len(args) != 1 || args[0] != "menu" {
		env, err := settings.Env(root, getenv)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		return driveTask(ctx, args, input, out, errOut, env, root)
	}
	opts, _ := loadMenuOptions(root)
	a := menuApp{ctx: ctx, input: bufio.NewReader(input), out: out, errOut: errOut, options: opts}
	a.saveOptions = func(o transferOptions) error { return saveMenuOptions(root, o) }
	a.dispatch = func(args []string, in io.Reader, o, e io.Writer) int { return RunContext(ctx, args, in, o, e, getenv) }
	return a.driveMenu(root, getenv)
}
func (a *menuApp) dt(en, vi string) string {
	if a.options.Language == "vi" {
		return vi
	}
	return en
}
func (a *menuApp) driveMenu(root string, getenv func(string) string) int {
	for {
		fmt.Fprintln(a.out, a.dt("OneDrive → Google Drive\n1) Link accounts\n2) Check connections\n3) New migration\n4) Continue\n5) Progress\n6) Export report\n7) Transfer options\n8) Sign out an account\n0) Back", "OneDrive → Google Drive\n1) Liên kết tài khoản\n2) Kiểm tra kết nối\n3) Tạo tác vụ mới\n4) Tiếp tục\n5) Tiến trình\n6) Xuất báo cáo\n7) Tùy chọn truyền\n8) Đăng xuất tài khoản\n0) Quay lại"))
		c, e := a.ask("Choose", "0")
		if e != nil || c == "0" {
			return 0
		}
		switch c {
		case "1":
			a.driveAccounts(root, getenv)
		case "3":
			a.newDrivePlan()
		case "4", "5", "6":
			a.savedDriveAction(root, c)
		case "7":
			a.configureDriveWorkers()
		case "8":
			a.driveLogout()
		case "2":
			a.execute([]string{"auth", "check", "onedrive"}, a.out)
			a.execute([]string{"auth", "check", "gdrive"}, a.out)
		}
	}
}
func (a *menuApp) driveAccounts(root string, getenv func(string) string) {
	saved, e := settings.Load(root)
	if e != nil {
		fmt.Fprintln(a.errOut, e)
		return
	}
	steps := []struct{ url, en, vi string }{
		{microsoftConsole, "Microsoft: App registrations → your app (or New registration). Support personal Microsoft accounts. Authentication → Mobile/Desktop → http://localhost. API permissions → Microsoft Graph → Delegated → Files.Read. No OneDrive write permission is needed.", "Microsoft: App registrations → chọn/tạo app, hỗ trợ tài khoản Microsoft cá nhân. Authentication → Mobile/Desktop → http://localhost. API permissions → Microsoft Graph → Delegated → Files.Read (chỉ đọc)."},
		{"https://console.cloud.google.com/apis/library/drive.googleapis.com", "Google: select your project and Enable Google Drive API.", "Google: chọn đúng project rồi Enable Google Drive API."},
		{"https://console.cloud.google.com/auth/audience", "Google Auth Platform: configure Branding/Audience; personal accounts use External. If Testing, add your destination email as a test user. Drive refresh tokens in External/Testing normally expire after 7 days; review publishing status for long jobs. Production does not guarantee permanent credentials.", "Google Auth Platform: cấu hình Branding/Audience; tài khoản cá nhân chọn External. Nếu Testing, thêm email đích vào test users. Token Drive ở External/Testing thường hết hạn sau 7 ngày; kiểm tra publishing trước khi chạy dài ngày."},
		{googleConsole, "Google: create OAuth client → Desktop app, download JSON. Required Drive scope: https://www.googleapis.com/auth/drive.file (app-authorized destination files). This is separate from Photos login.", "Google: tạo OAuth client → Desktop app, tải JSON. Quyền Drive: https://www.googleapis.com/auth/drive.file (file/thư mục được cấp cho app). Đăng nhập riêng với Photos."},
	}
	for i := 0; i < len(steps); i++ {
		if i < saved.DriveSetupStep {
			continue
		}
		s := steps[i]
		for {
			fmt.Fprintln(a.out, a.dt(s.en, s.vi))
			fmt.Fprintln(a.out, s.url)
			c, e := a.ask(a.dt("o = open link, d = done, b = back", "o = mở link, d = đã xong, b = quay lại"), "b")
			if e != nil || c == "b" {
				return
			}
			if c == "o" {
				if e := identity.OpenSetupURL(s.url); e != nil {
					fmt.Fprintln(a.errOut, a.dt("Open the displayed URL manually.", "Mở link hiển thị bằng trình duyệt."))
				}
				continue
			}
			if c != "d" {
				continue
			}
			saved.DriveSetupStep = i + 1
			if e := settings.Save(root, saved); e != nil {
				fmt.Fprintln(a.errOut, e)
				return
			}
			break
		}
	}
	configuredID := getenv("OD2GP_MICROSOFT_CLIENT_ID")
	if configuredID == "" {
		configuredID = saved.MicrosoftClientID
	}
	if configuredID == "" {
		configuredID = settings.DefaultMicrosoftClientID
	}
	id, e := a.ask(a.dt("Microsoft client ID (Enter keeps configured ID)", "Microsoft client ID (Enter giữ cấu hình hiện tại)"), configuredID)
	if e != nil {
		return
	}
	if a.execute([]string{"setup", "microsoft", id}, a.out) != 0 {
		return
	}
	file, e := a.ask(a.dt("Google Desktop JSON path", "Đường dẫn Google Desktop JSON"), saved.DriveClientFile)
	if e != nil {
		return
	}
	file = strings.Trim(file, "\"")
	if checkGoogleClient(file) != nil {
		fmt.Fprintln(a.errOut, a.dt("Invalid Desktop JSON; credentials unchanged.", "JSON Desktop không hợp lệ; chưa đổi credentials."))
		return
	}
	file, e = filepath.Abs(file)
	if e != nil {
		return
	}
	// Reload so the Microsoft settings written above are not overwritten.
	saved, e = settings.Load(root)
	if e != nil {
		fmt.Fprintln(a.errOut, e)
		return
	}
	saved.DriveClientFile = file
	if e = settings.Save(root, saved); e != nil {
		fmt.Fprintln(a.errOut, e)
		return
	}
	fmt.Fprintln(a.out, a.dt("Sign in to each account. Login itself transfers no files.", "Đăng nhập từng tài khoản. Đăng nhập không chuyển file."))
	if a.execute([]string{"auth", "login", "onedrive"}, a.out) != 0 {
		return
	}
	if a.execute([]string{"auth", "login", "gdrive"}, a.out) != 0 {
		return
	}
	a.execute([]string{"source", "select-drive"}, a.out)
	a.execute([]string{"auth", "check", "gdrive"}, a.out)
	fmt.Fprintln(a.out, a.dt("Setup choices saved. Connection checks above are authoritative; console steps were acknowledged by you, not remotely verified.", "Đã lưu thiết lập. Kết quả kiểm tra kết nối ở trên mới xác nhận truy cập; các bước console do bạn xác nhận."))
}
