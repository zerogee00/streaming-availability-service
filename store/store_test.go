package store_test

import (
	"fmt"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"sas/store"
)

var t0 = time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC)

func TestPut(t *testing.T) {
	t.Run("keeps segments sorted", func(t *testing.T) {
		s := store.New()
		for _, i := range []int{3, 0, 2, 1} {
			s.Put("a", store.Segment{Index: i, CDNURL: fmt.Sprint(i)}, t0)
		}

		got, _, _ := s.Get("a")
		want := []store.Segment{{Index: 0, CDNURL: "0"}, {Index: 1, CDNURL: "1"}, {Index: 2, CDNURL: "2"}, {Index: 3, CDNURL: "3"}}
		if !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("duplicate index updates in place", func(t *testing.T) {
		s := store.New()
		s.Put("a", store.Segment{Index: 0, CDNURL: "old"}, t0)
		s.Put("a", store.Segment{Index: 0, CDNURL: "new"}, t0)

		got, _, _ := s.Get("a")
		want := []store.Segment{{Index: 0, CDNURL: "new"}}
		if !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("last updated never moves backwards", func(t *testing.T) {
		s := store.New()
		s.Put("a", store.Segment{Index: 1, CDNURL: "u"}, t0.Add(time.Minute))
		s.Put("a", store.Segment{Index: 0, CDNURL: "u"}, t0)

		_, last, _ := s.Get("a")
		if !last.Equal(t0.Add(time.Minute)) {
			t.Errorf("got %v, want %v", last, t0.Add(time.Minute))
		}
	})
}

func TestGet(t *testing.T) {
	t.Run("unknown asset", func(t *testing.T) {
		if _, _, ok := store.New().Get("missing"); ok {
			t.Error("got ok for an asset that was never stored")
		}
	})

	t.Run("returns a copy", func(t *testing.T) {
		s := store.New()
		s.Put("a", store.Segment{Index: 0, CDNURL: "original"}, t0)

		got, _, _ := s.Get("a")
		got[0].CDNURL = "mutated"

		again, _, _ := s.Get("a")
		if again[0].CDNURL != "original" {
			t.Errorf("caller mutation leaked into the store: got %q", again[0].CDNURL)
		}
	})
}

func BenchmarkPutDuplicateParallel(b *testing.B) {
	s := store.New()
	ids := make([]string, 500)
	for i := range ids {
		ids[i] = fmt.Sprintf("show/bench/ep%d", i)
	}

	var n atomic.Int64
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			i := int(n.Add(1))
			s.Put(ids[i%len(ids)], store.Segment{Index: i % 1000, CDNURL: "u"}, t0)
		}
	})
}

func BenchmarkGet(b *testing.B) {
	s := store.New()
	for i := range 1000 {
		s.Put("a", store.Segment{Index: i, CDNURL: "u"}, t0)
	}

	b.ReportAllocs()
	for b.Loop() {
		s.Get("a")
	}
}
