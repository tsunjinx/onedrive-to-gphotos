package cli

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"od2gp/internal/journal"
	"od2gp/internal/migration"
)

type menuJob struct {
	ID, Folder, Problem                     string
	Created                                 time.Time
	Total, Completed, Processing, Skipped   int
	Incomplete, Started                     bool
	InventoryComplete, ExcludePersonalVault bool
	DriveID, GoogleSubject                  string
}

func loadMenuJobs(root string) ([]menuJob, error) {
	directory := filepath.Join(root, "od2gp", "jobs")
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("cannot list saved jobs; check local data directory access")
	}
	var jobs []menuJob
	for _, entry := range entries {
		if !entry.IsDir() || !migration.ValidID(entry.Name()) {
			continue
		}
		file := filepath.Join(directory, entry.Name(), "progress.db")
		if _, err := os.Stat(file); errors.Is(err, os.ErrNotExist) {
			continue
		}
		job, err := loadMenuJob(file, entry.Name())
		if err != nil {
			job.Problem = "Details unavailable; check job data and OS credential-store access."
		}
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(i, j int) bool {
		if (jobs[i].Problem == "") != (jobs[j].Problem == "") {
			return jobs[i].Problem == ""
		}
		if jobs[i].Incomplete != jobs[j].Incomplete {
			return jobs[i].Incomplete
		}
		if !jobs[i].Created.Equal(jobs[j].Created) {
			return jobs[i].Created.After(jobs[j].Created)
		}
		return jobs[i].ID < jobs[j].ID
	})
	return jobs, nil
}

func loadMenuJob(file, id string) (menuJob, error) {
	job := menuJob{ID: id}
	key, err := journal.Key(id, false)
	if err != nil {
		return job, err
	}
	store, err := journal.Open(file, key, false)
	if err != nil {
		return job, err
	}
	defer store.Close()
	var meta migration.Meta
	if err := store.Load(0, &meta); err != nil {
		return job, err
	}
	if meta.ID != id {
		return job, errors.New("saved job identity mismatch")
	}
	job.Folder, job.Created, job.Incomplete = meta.Folder, meta.Created, !meta.InventoryComplete
	job.InventoryComplete, job.ExcludePersonalVault = meta.InventoryComplete, meta.ExcludePersonalVault
	job.DriveID, job.GoogleSubject = meta.DriveID, meta.GoogleSubject
	ids, err := store.IDs()
	if err != nil {
		return job, err
	}
	job.Total = len(ids)
	for _, id := range ids {
		var item migration.Item
		if err := store.Load(id, &item); err != nil {
			return job, err
		}
		switch item.State {
		case "completed":
			job.Completed++
		case "skipped":
			job.Skipped++
		case "processing_failed":
		default:
			job.Incomplete = true
		}
		if item.State == "processing" {
			job.Processing++
		}
		if item.State != "discovered" && item.State != "skipped" {
			job.Started = true
		}
	}
	return job, nil
}
