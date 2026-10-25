package migration

import "errors"

// IncreaseCache changes only the ceiling. The caller must own the job lock;
// transfer reservations still check actual free disk space before downloading.
func (e *Engine) IncreaseCache(meta *Meta, bytes int64) error {
	if bytes < 1<<30 || bytes > 1024<<30 {
		return errors.New("staging limit must be between 1 and 1024 GiB")
	}
	if bytes < meta.CacheBytes {
		return errors.New("staging limit can only be increased for a saved job")
	}
	updated := *meta
	updated.CacheBytes = bytes
	if err := e.Store.Save(0, "job", updated); err != nil {
		return err
	}
	*meta = updated
	return nil
}
