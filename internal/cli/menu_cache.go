package cli

import (
	"fmt"
	"strconv"
)

func (a *menuApp) resizeCache(job menuJob) {
	fmt.Fprintf(a.out, a.t("Current staging limit: %.1f GiB. This changes only this saved job's limit.\n"), float64(job.CacheBytes)/(1<<30))
	fmt.Fprintln(a.out, a.t("Completion history is kept. Disk space is checked during transfer; no uploads start here."))
	minimum := max(1, int((job.CacheBytes+(1<<30)-1)>>30))
	value, ok := a.transferNumber("New staging limit in GiB", minimum, minimum, 1024)
	if !ok {
		return
	}
	if a.execute([]string{"resize-cache", "--job", job.ID, "--cache-gib", strconv.Itoa(value)}, a.out) == 0 {
		fmt.Fprintln(a.out, a.t("Staging limit saved. Choose Continue when ready."))
	}
}
