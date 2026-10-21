package store

import (
	"cmp"
	"slices"
	"sync"
	"time"
)

type Segment struct {
	Index  int    `json:"index"`
	CDNURL string `json:"cdn_url"`
}

type asset struct {
	mu          sync.RWMutex
	segments    []Segment
	lastUpdated time.Time
}

type Store struct {
	assets sync.Map
}

func New() *Store {
	return &Store{}
}

func (s *Store) Put(assetID string, seg Segment, receivedAt time.Time) {
	v, _ := s.assets.LoadOrStore(assetID, &asset{})
	a := v.(*asset)

	a.mu.Lock()
	defer a.mu.Unlock()

	i, found := slices.BinarySearchFunc(a.segments, seg.Index, func(x Segment, target int) int {
		return cmp.Compare(x.Index, target)
	})
	if found {
		a.segments[i] = seg
	} else {
		a.segments = slices.Insert(a.segments, i, seg)
	}

	if receivedAt.After(a.lastUpdated) {
		a.lastUpdated = receivedAt
	}
}

func (s *Store) Get(assetID string) ([]Segment, time.Time, bool) {
	v, ok := s.assets.Load(assetID)
	if !ok {
		return nil, time.Time{}, false
	}
	a := v.(*asset)

	a.mu.RLock()
	defer a.mu.RUnlock()

	return slices.Clone(a.segments), a.lastUpdated, true
}
