package cli

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestMenuRootVaultChoices(t *testing.T) {
	for _, choice := range []string{"1", "2", "0", ""} {
		for _, existing := range []bool{false, true} {
			t.Run(fmt.Sprintf("choice=%s/existing=%v", choice, existing), func(t *testing.T) {
				var out bytes.Buffer
				id := strings.Repeat("a", 32)
				input := "2\n/\n10\n" + choice + "\n\n0\n"
				if existing {
					input = "3\n1\n" + choice + "\n\n0\n"
				}
				var calls [][]string
				app := menuApp{ctx: context.Background(), input: bufio.NewReader(strings.NewReader(input)), out: &out, errOut: &out,
					jobs: func() ([]menuJob, error) {
						if existing {
							return []menuJob{{ID: id, Incomplete: true}}, nil
						}
						return nil, nil
					},
					dispatch: func(args []string, _ io.Reader, w, _ io.Writer) int {
						calls = append(calls, args)
						if args[0] == "plan" {
							fmt.Fprintln(w, "Job:", id)
						}
						return 0
					}}
				app.run()
				var transfer, plan []string
				for _, args := range calls {
					if args[0] == "run" {
						transfer = args
					}
					if args[0] == "plan" {
						plan = args
					}
				}
				accepted := choice == "1" || choice == "2"
				if (transfer != nil) != accepted {
					t.Fatalf("wrong dispatch: %v", calls)
				}
				if !accepted && plan != nil {
					t.Fatalf("cancel created job: %v", calls)
				}
				scopeArgs := plan
				if existing {
					scopeArgs = transfer
				}
				if accepted && strings.Contains(strings.Join(scopeArgs, " "), "--exclude-personal-vault") != (choice == "1") {
					t.Fatalf("wrong policy: %v", calls)
				}
				if existing && accepted && !strings.Contains(strings.Join(transfer, " "), id) {
					t.Fatal("changed saved job")
				}
			})
		}
	}
}

func TestMenuVaultPromptOnlyForUnresolvedRoot(t *testing.T) {
	for _, job := range []menuJob{{Folder: "Pictures"}, {InventoryComplete: true}, {ExcludePersonalVault: true}, {Started: true}} {
		var out bytes.Buffer
		job.ID = strings.Repeat("b", 32)
		job.Incomplete = true
		calls := 0
		app := menuApp{ctx: context.Background(), input: bufio.NewReader(strings.NewReader("3\n1\n\n0\n")), out: &out, errOut: &out, jobs: func() ([]menuJob, error) { return []menuJob{job}, nil }, dispatch: func([]string, io.Reader, io.Writer, io.Writer) int { calls++; return 0 }}
		app.run()
		if calls != 1 || strings.Contains(out.String(), "Choose Personal Vault handling") {
			t.Fatalf("unexpected prompt: %+v %s", job, out.String())
		}
	}
}

func TestFolderOverlapBoundaries(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{{"/", "Pictures", true}, {"onedrive:/", "Pictures", true}, {"Pictures", "pictures/Trip", true}, {"Pictures", "Pictures2", false}, {"Pictures/A", "Pictures/B", false}} {
		if got := foldersOverlap(tc.a, tc.b); got != tc.want {
			t.Fatalf("%q %q: %v", tc.a, tc.b, got)
		}
	}
}

func TestOverlapRequiresExplicitConsentAndRespectsBindings(t *testing.T) {
	candidate := menuJob{ID: "new", Folder: "Pictures", DriveID: "drive", GoogleSubject: "google"}
	for _, tc := range []struct {
		name, answer string
		other        menuJob
		warn, allow  bool
	}{
		{"cancel", "\n", menuJob{ID: "old", Completed: 13}, true, false},
		{"eof", "", menuJob{ID: "old"}, true, false},
		{"accept", "yes\n", menuJob{ID: "old"}, true, true},
		{"same job", "", candidate, false, true},
		{"other drive", "", menuJob{ID: "old", DriveID: "other"}, false, true},
		{"other destination", "", menuJob{ID: "old", GoogleSubject: "other"}, false, true},
		{"unreadable", "no\n", menuJob{ID: "old", Problem: "locked"}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			app := menuApp{ctx: context.Background(), input: bufio.NewReader(strings.NewReader(tc.answer)), out: &out}
			got := app.confirmOverlap(candidate, []menuJob{tc.other})
			if got != tc.allow || strings.Contains(out.String(), "Potential overlap") != tc.warn {
				t.Fatalf("allow=%v output=%s", got, out.String())
			}
		})
	}
}

func TestNewPlanOverlapRejectionKeepsPlanWithoutUploading(t *testing.T) {
	var out bytes.Buffer
	var calls []string
	app := menuApp{ctx: context.Background(), input: bufio.NewReader(strings.NewReader("/Pictures\n10\nno\n")), out: &out, errOut: &out, jobs: func() ([]menuJob, error) { return []menuJob{{ID: "old", Completed: 13}}, nil }, dispatch: func(args []string, _ io.Reader, w, _ io.Writer) int {
		calls = append(calls, args[0])
		if args[0] == "plan" {
			fmt.Fprintln(w, "Job:", strings.Repeat("a", 32))
		}
		return 0
	}}
	app.newPlan()
	if strings.Join(calls, ",") != "source,plan" || !strings.Contains(out.String(), "13 confirmed completed") {
		t.Fatalf("calls=%v output=%s", calls, out.String())
	}
}
