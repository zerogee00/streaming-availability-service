package store

import (
	"fmt"
	"slices"
	"testing"
	"time"
)

// Keep each manifest bounded by restoring its baseline outside the timed region.
func BenchmarkPutOutOfOrder(b *testing.B) {
	for _, size := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			baseline := make([]Segment, size)
			for i := range baseline {
				baseline[i] = Segment{Index: 2 * i, CDNURL: "u"}
			}
			a := &asset{segments: make([]Segment, size, size+1)}
			copy(a.segments, baseline)
			s := New()
			s.assets.Store("a", a)
			b.ReportAllocs()
			for b.Loop() {
				s.Put("a", Segment{Index: size - 1, CDNURL: "inserted"}, time.Time{})
				b.StopTimer()
				a.segments = slices.Delete(a.segments, size/2, size/2+1)
				b.StartTimer()
			}
		})
	}
}
