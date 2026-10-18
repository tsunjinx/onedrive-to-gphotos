package cli

import (
	"fmt"
	"path"
	"strings"
)

func normalizeMenuFolder(folder string) string {
	folder = strings.Trim(strings.TrimPrefix(folder, "onedrive:"), "/")
	if folder == "" {
		return ""
	}
	return strings.Trim(path.Clean(folder), "/")
}

// No default scope change: Enter and 0 both cancel without dispatching a command.
func (a *menuApp) chooseVaultPolicy() (exclude, proceed bool) {
	fmt.Fprintln(a.out, "This job scans the drive root. Personal Vault may be inaccessible through the API.\nExcluding it leaves its files uncopied, with unknown count and size. The choice will be saved with this job.\n1) Exclude /Personal Vault and scan the rest\n2) Keep Vault included (the scan may stop if inaccessible)\n0) Cancel without changing the job")
	for {
		answer, err := a.ask("Choose Personal Vault handling", "0")
		if err != nil {
			return false, false
		}
		switch answer {
		case "1":
			return true, true
		case "2":
			return false, true
		case "0":
			return false, false
		default:
			fmt.Fprintln(a.out, "Choose 1, 2 or 0.")
		}
	}
}
func foldersOverlap(a, b string) bool {
	a, b = strings.ToLower(normalizeMenuFolder(a)), strings.ToLower(normalizeMenuFolder(b))
	return a == "" || b == "" || a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}
func (a *menuApp) confirmOverlap(candidate menuJob, jobs []menuJob) bool {
	var matches []menuJob
	uncertain := false
	for _, job := range jobs {
		if job.ID == candidate.ID {
			continue
		}
		if job.Problem != "" {
			uncertain = true
			continue
		}
		if candidate.DriveID != "" && job.DriveID != "" && candidate.DriveID != job.DriveID {
			continue
		}
		if candidate.GoogleSubject != "" && job.GoogleSubject != "" && candidate.GoogleSubject != job.GoogleSubject {
			continue
		}
		if foldersOverlap(candidate.Folder, job.Folder) {
			matches = append(matches, job)
		}
	}
	if len(matches) == 0 && !uncertain {
		return true
	}
	fmt.Fprintln(a.out, "Potential overlap with saved migration history. Jobs do not share completion records; already copied files may be uploaded again.\nVerify the source drive and destination account. Use Continue on the original job to preserve its history.")
	for i, job := range matches {
		if i == 5 {
			fmt.Fprintf(a.out, "  ...and %d more possibly overlapping jobs.\n", len(matches)-5)
			break
		}
		fmt.Fprintf(a.out, "  %s: %q — %d confirmed completed\n", job.ID, "/"+job.Folder, job.Completed)
	}
	if uncertain {
		fmt.Fprintln(a.out, "Some saved jobs could not be read, so their overlap cannot be checked.")
	}
	answer, err := a.ask("Proceed with this separate job despite possible duplicates? Type yes to proceed", "no")
	if err != nil || answer != "yes" {
		fmt.Fprintln(a.out, "No uploads started. Saved migration progress is kept.")
		return false
	}
	return true
}
