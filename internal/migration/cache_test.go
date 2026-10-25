package migration

import (
	"reflect"
	"testing"
)

func TestIncreaseCachePreservesJobHistoryAndRejectsShrink(t *testing.T) {
	e, meta, _, _, closeStore := fixture(t)
	defer closeStore()
	var item Item
	e.Store.Load(1, &item)
	item.State = "completed"
	item.MediaID = "confirmed"
	e.Store.Save(1, item.State, item)
	before := *meta
	if err := e.IncreaseCache(meta, 2<<30); err != nil {
		t.Fatal(err)
	}
	before.CacheBytes = 2 << 30
	var saved Meta
	if err := e.Store.Load(0, &saved); err != nil || !reflect.DeepEqual(saved, before) {
		t.Fatalf("job binding changed: %+v %v", saved, err)
	}
	var after Item
	e.Store.Load(1, &after)
	if !reflect.DeepEqual(after, item) {
		t.Fatal("completion history changed")
	}
	for _, invalid := range []int64{0, 1 << 30, 1025 << 30} {
		if err := e.IncreaseCache(meta, invalid); err == nil {
			t.Fatal("invalid resize accepted")
		}
		e.Store.Load(0, &saved)
		if !reflect.DeepEqual(saved, before) {
			t.Fatal("failed resize changed journal")
		}
	}
}
