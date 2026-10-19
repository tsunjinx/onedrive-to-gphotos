package cli

import "fmt"

// Translate UI templates before formatting. Paths, IDs, command arguments,
// report data and provider diagnostics are never passed through this catalog.
func menuText(language, text string) string {
	if language == "vi" {
		if value, ok := vietnameseMenu[text]; ok {
			return value
		}
	}
	return text
}
func (a *menuApp) t(text string) string { return menuText(a.currentOptions().Language, text) }
func (a *menuApp) configureLanguage() {
	fmt.Fprintln(a.out, "Language / Ngôn ngữ\n1) English\n2) Tiếng Việt\n0) Back / Quay lại")
	for {
		choice, err := a.ask("Choose / Chọn", "0")
		if err != nil || choice == "0" {
			return
		}
		opts := a.currentOptions()
		switch choice {
		case "1":
			opts.Language = "en"
		case "2":
			opts.Language = "vi"
		default:
			fmt.Fprintln(a.out, "Choose 1, 2 or 0 / Chọn 1, 2 hoặc 0.")
			continue
		}
		if a.saveOptions != nil {
			if err := a.saveOptions(opts); err != nil {
				fmt.Fprintln(a.errOut, a.t("Could not save language. Previous language is kept."))
				return
			}
		}
		a.options = opts
		fmt.Fprintln(a.out, a.t("Language saved."))
		return
	}
}

var vietnameseMenu = map[string]string{
	"OneDrive → Google Photos\nCopy photos and videos. OneDrive originals are kept.": "OneDrive → Google Photos\nSao chép ảnh và video. Giữ nguyên tệp gốc trên OneDrive.",
	"Settings:": "Cài đặt:",
	"\n1) Check both account connections\n2) New migration (choose drive, then preview)\n3) Continue a saved migration\n4) View job progress\n5) Export a report\n6) Set up or reconnect accounts\n7) Choose OneDrive drive\n8) Transfer speed & options\n9) Language / Ngôn ngữ\n10) Source folder filters\n0) Exit": "\n1) Kiểm tra kết nối hai tài khoản\n2) Tạo tác vụ mới (chọn ổ đĩa, rồi xem trước)\n3) Tiếp tục tác vụ đã lưu\n4) Xem tiến trình tác vụ\n5) Xuất báo cáo\n6) Thiết lập hoặc đăng nhập lại\n7) Chọn ổ đĩa OneDrive\n8) Tốc độ truyền & tùy chọn\n9) Language / Ngôn ngữ\n10) Lọc thư mục nguồn\n0) Thoát",
	"\nReady to continue: %q (%d/%d eligible completed, %d processing)\n": "\nCó thể tiếp tục: %q (%d/%d tệp đủ điều kiện đã xong, %d đang xử lý)\n",
	"Choose":                            "Chọn",
	"Choose a number from 0 to 9.":      "Chọn một số từ 0 đến 9.",
	"Press Enter to return to the menu": "Nhấn Enter để quay lại menu",
	"Transfer stopped. Review the message above; choose Continue to use the same saved job.":                                     "Đã dừng truyền. Xem thông báo phía trên; chọn Tiếp tục để dùng lại tác vụ đã lưu.",
	"Videos are still processing. Choose Continue later to check them again.":                                                    "Video vẫn đang được xử lý. Chọn Tiếp tục sau để kiểm tra lại.",
	"Some videos failed processing. View job progress or export a report for details.":                                           "Một số video xử lý thất bại. Xem tiến trình hoặc xuất báo cáo để biết chi tiết.",
	"Both connections work. You can plan or continue a migration.":                                                               "Cả hai tài khoản đều kết nối được. Bạn có thể tạo hoặc tiếp tục tác vụ.",
	"Choose a OneDrive folder. / means the entire drive.\nA new plan has separate history; use Continue for an interrupted job.": "Chọn thư mục OneDrive. / nghĩa là toàn bộ ổ đĩa.\nTác vụ mới có lịch sử riêng; hãy chọn Tiếp tục cho tác vụ bị gián đoạn.",
	"OneDrive folder":                                         "Thư mục OneDrive",
	"Maximum staging space in GiB":                            "Dung lượng bộ đệm tạm tối đa (GiB)",
	"Enter a whole number from 1 to 1024. The default is 30.": "Nhập số nguyên từ 1 đến 1024. Mặc định là 30.",
	"Cannot check existing migration history; plan is saved. Resolve job access before starting uploads.": "Không kiểm tra được lịch sử cũ; tác vụ đã được lưu. Hãy khắc phục quyền truy cập tác vụ trước khi tải lên.",
	"Plan saved. Choose Continue from the menu when ready.":                                               "Đã lưu tác vụ. Chọn Tiếp tục trong menu khi sẵn sàng.",
	"%q — %d/%d eligible completed, %d processing, %d skipped — %s (%s)":                                  "%q — %d/%d tệp đủ điều kiện đã xong, %d đang xử lý, %d bỏ qua — %s (%s)",
	" — inventory incomplete": " — chưa quét xong",
	" — needs recovery (%d)":  " — cần phục hồi (%d)",
	" — Vault excluded":       " — đã loại trừ Vault",
	"No matching saved jobs. Choose Plan a new migration to begin.": "Không có tác vụ phù hợp. Chọn Tạo tác vụ mới để bắt đầu.",
	"0) Back":      "0) Quay lại",
	"Choose a job": "Chọn tác vụ",
	"Choose one of the displayed job numbers.":                     "Chọn một trong các số tác vụ đang hiển thị.",
	"Report format: csv or json":                                   "Định dạng báo cáo: csv hoặc json",
	"Enter csv or json.":                                           "Nhập csv hoặc json.",
	"Save report to":                                               "Lưu báo cáo vào",
	"That file already exists. Choose a different report name.":    "Tệp đã tồn tại. Hãy chọn tên báo cáo khác.",
	"Cannot create that report; check the folder and permissions.": "Không tạo được báo cáo; kiểm tra thư mục và quyền truy cập.",
	"Report export failed; incomplete output was removed.":         "Xuất báo cáo thất bại; đã xóa tệp báo cáo chưa hoàn chỉnh.",
	"Report saved: %q\n":                                           "Đã lưu báo cáo: %q\n",
	"1) Configure Google client JSON\n2) Sign in to OneDrive\n3) Sign in to Google\n4) Check both connections\n5) Choose OneDrive drive\n0) Back": "1) Thiết lập tệp JSON ứng dụng Google\n2) Đăng nhập OneDrive\n3) Đăng nhập Google\n4) Kiểm tra hai kết nối\n5) Chọn ổ đĩa OneDrive\n0) Quay lại",
	"Choose a number from 0 to 5 next time.":              "Lần sau hãy chọn một số từ 0 đến 5.",
	"%d files at once | batch up to %d | %d retries | %s": "%d tệp cùng lúc | tối đa %d mục/lô | thử lại %d lần | %s",
	"check videos once":                                   "kiểm tra video một lần",
	"wait for video processing":                           "chờ xử lý video",
	"Transfer settings:":                                  "Cài đặt truyền:",
	"Ctrl+C saves progress and exits. Open the menu and choose Continue to pick up this job later.": "Ctrl+C lưu tiến trình và thoát. Mở lại menu, chọn Tiếp tục để chạy tiếp tác vụ này.",
	"%s (%d-%d, b to cancel)":            "%s (%d-%d, b để hủy)",
	"Enter a number from %d to %d.\n":    "Nhập một số từ %d đến %d.\n",
	"Transfer speed & options\nCurrent:": "Tốc độ truyền & tùy chọn\nHiện tại:",
	"1) Recommended: 3 files, batch 50, 5 retries, wait for videos\n2) Gentle: 1 file at once (try this if requests keep failing)\n3) Faster: 6 files at once\n4) Custom settings\n0) Back": "1) Khuyến nghị: 3 tệp, lô 50, thử lại 5 lần, chờ video\n2) Nhẹ: 1 tệp cùng lúc (dùng khi yêu cầu liên tục gặp lỗi)\n3) Nhanh: 6 tệp cùng lúc\n4) Tùy chỉnh\n0) Quay lại",
	"Settings are remembered for New migration and Continue. Your saved folder and staging limit stay the same.":                                                                            "Cài đặt được nhớ cho Tạo tác vụ mới và Tiếp tục. Thư mục và giới hạn bộ đệm của tác vụ đã lưu vẫn giữ nguyên.",
	"Choose a preset": "Chọn chế độ",
	"Press Enter to keep a value, or type b to cancel without saving.": "Nhấn Enter để giữ giá trị, hoặc nhập b để hủy mà không lưu.",
	"Files to upload at once":                  "Số tệp tải lên cùng lúc",
	"Maximum files to create per batch":        "Số mục tạo tối đa mỗi lô",
	"Retries after a temporary failure":        "Số lần thử lại khi gặp lỗi tạm thời",
	"Wait up to 30 minutes for videos? yes/no": "Chờ video tối đa 30 phút? yes/có hoặc no/không",
	"Enter yes or no.":                         "Nhập yes/có hoặc no/không.",
	"Choose a number from 0 to 4.":             "Chọn một số từ 0 đến 4.",
	"Could not save transfer settings. Previous settings are kept; check local folder permissions and disk space.": "Không lưu được cài đặt truyền. Giữ nguyên cài đặt cũ; hãy kiểm tra quyền thư mục và dung lượng ổ đĩa.",
	"Saved:": "Đã lưu:",
	"%d file(s) need creation recovery for this job. Google may already have accepted them before the response was lost.\n":                                 "Tác vụ có %d tệp cần phục hồi kết quả tạo. Google có thể đã nhận chúng trước khi phản hồi bị mất.\n",
	"Recovery verifies the retained original files, retries those uncertain items, then continues this same job. Confirmed completed files remain skipped.": "Phục hồi sẽ kiểm tra tệp gốc đã giữ lại, thử lại các mục chưa rõ kết quả, rồi tiếp tục cùng tác vụ. Các tệp đã xác nhận hoàn tất vẫn được bỏ qua.",
	"1) Review job progress first\n2) Retry uncertain items using the verified original files\n0) Back without retrying":                                    "1) Xem tiến trình tác vụ trước\n2) Thử lại các mục chưa rõ kết quả bằng tệp gốc đã kiểm tra\n0) Quay lại, không thử lại",
	"Choose recovery action": "Chọn cách phục hồi",
	"Choose 1, 2 or 0.":      "Chọn 1, 2 hoặc 0.",
	"This job scans the drive root. Personal Vault may be inaccessible through the API.\nExcluding it leaves its files uncopied, with unknown count and size. The choice will be saved with this job.\n1) Exclude /Personal Vault and scan the rest\n2) Keep Vault included (the scan may stop if inaccessible)\n0) Cancel without changing the job": "Tác vụ này quét toàn bộ ổ đĩa. API có thể không truy cập được Personal Vault.\nNếu loại trừ, các tệp trong đó sẽ không được sao chép và chưa biết số lượng, dung lượng. Lựa chọn được lưu cùng tác vụ.\n1) Loại trừ /Personal Vault và quét phần còn lại\n2) Vẫn quét Vault (có thể dừng nếu không truy cập được)\n0) Hủy, không thay đổi tác vụ",
	"Choose Personal Vault handling": "Chọn cách xử lý Personal Vault",
	"Potential overlap with saved migration history. Jobs do not share completion records; already copied files may be uploaded again.\nVerify the source drive and destination account. Use Continue on the original job to preserve its history.": "Có thể trùng phạm vi với tác vụ đã lưu. Các tác vụ không chia sẻ lịch sử hoàn tất; tệp đã sao chép có thể được tải lên lại.\nKiểm tra ổ đĩa nguồn và tài khoản đích. Chọn Tiếp tục trên tác vụ gốc để giữ lịch sử của nó.",
	"  ...and %d more possibly overlapping jobs.\n":                                                             "  ...và %d tác vụ khác có thể trùng phạm vi.\n",
	"  %s: %q — %d confirmed completed\n":                                                                       "  %s: %q — %d tệp đã xác nhận hoàn tất\n",
	"Some saved jobs could not be read, so their overlap cannot be checked.":                                    "Không đọc được một số tác vụ đã lưu nên chưa kiểm tra được phạm vi trùng nhau.",
	"Proceed with this separate job despite possible duplicates? Type yes to proceed":                           "Tiếp tục tác vụ riêng này dù có thể trùng tệp? Gõ yes để tiếp tục",
	"No uploads started. Saved migration progress is kept.":                                                     "Chưa bắt đầu tải lên. Tiến trình đã lưu được giữ nguyên.",
	"Details unavailable; check job data and OS credential-store access.":                                       "Không đọc được chi tiết; kiểm tra dữ liệu tác vụ và quyền truy cập kho thông tin xác thực của hệ điều hành.",
	"cannot list saved jobs; check local data directory access":                                                 "không liệt kê được tác vụ; hãy kiểm tra quyền truy cập thư mục dữ liệu cục bộ",
	"Saved transfer settings could not be loaded. Using recommended settings; choose option 8 to replace them.": "Không tải được cài đặt truyền đã lưu. Đang dùng cấu hình khuyến nghị; chọn mục 8 để lưu lại.",
	"Could not save language. Previous language is kept.":                                                       "Không lưu được ngôn ngữ. Giữ nguyên ngôn ngữ trước đó.",
	"Language saved.":               "Đã lưu ngôn ngữ Tiếng Việt.",
	"Choose a number from 0 to 10.": "Chọn một số từ 0 đến 10.",
	"Source folder filters (new migrations only). Continue keeps its saved filter.":                               "Lọc thư mục nguồn (chỉ áp dụng cho tác vụ mới). Tiếp tục giữ nguyên bộ lọc đã lưu.",
	"Enter drive-root folders separated by |, e.g. /Documents/Projects | /Apps. Type - to clear, or b to cancel.": "Nhập đường dẫn từ gốc ổ đĩa, ngăn cách bằng |, ví dụ /Documents/Projects | /Apps. Nhập - để xóa, hoặc b để hủy.",
	"Files are still scanned and shown as skipped in reports. No folders are excluded automatically.":             "Các tệp vẫn được quét và ghi là bỏ qua trong báo cáo. Không có thư mục nào tự động bị loại.",
	"Folders to exclude": "Thư mục cần loại trừ",
	"Use absolute folder paths such as /Documents/Projects; do not use . or .. segments.": "Dùng đường dẫn tuyệt đối như /Documents/Projects; không dùng đoạn . hoặc ...",
	"Saved filters for new migrations:":                                                   "Đã lưu bộ lọc cho tác vụ mới:",
	"Excluded from uploads:":                                                              "Không tải lên:",
}
