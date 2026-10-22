package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Store struct {
	mu        sync.RWMutex
	snaps     map[Vendor]Snapshot
	busy      map[Vendor]bool
	fetcher   *Fetcher
	cacheFile string
}

func NewStore() *Store {
	f := NewFetcher()
	cacheDir := filepath.Join(f.LocalAppData, "LimitBar")
	_ = os.MkdirAll(cacheDir, 0700)
	cacheFile := filepath.Join(cacheDir, "state.json")

	s := &Store{
		snaps:     make(map[Vendor]Snapshot),
		busy:      make(map[Vendor]bool),
		fetcher:   f,
		cacheFile: cacheFile,
	}

	s.loadCache()
	return s
}

func (s *Store) loadCache() {
	data, err := os.ReadFile(s.cacheFile)
	if err == nil {
		var cached map[string]Snapshot
		if json.Unmarshal(data, &cached) == nil {
			s.mu.Lock()
			for k, snap := range cached {
				if snap.State == StateOK {
					s.snaps[Vendor(k)] = snap
				}
			}
			s.mu.Unlock()
		}
	}
}

func (s *Store) saveCache() {
	s.mu.RLock()
	data, err := json.MarshalIndent(s.snaps, "", "  ")
	s.mu.RUnlock()
	if err == nil {
		_ = os.WriteFile(s.cacheFile, data, 0600)
	}
}

func (s *Store) Installed() []Vendor {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var list []Vendor
	for _, v := range AllVendors {
		if snap, ok := s.snaps[v]; ok && snap.State != StateMissing {
			list = append(list, v)
		}
	}
	return list
}

func (s *Store) Get(v Vendor) (Snapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap, ok := s.snaps[v]
	return snap, ok
}

func (s *Store) All() map[Vendor]Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make(map[Vendor]Snapshot, len(s.snaps))
	for k, v := range s.snaps {
		res[k] = v
	}
	return res
}

func (s *Store) RefreshVendor(v Vendor) Snapshot {
	s.mu.Lock()
	if s.busy[v] {
		s.mu.Unlock()
		s.mu.RLock()
		snap := s.snaps[v]
		s.mu.RUnlock()
		return snap
	}
	s.busy[v] = true
	prev := s.snaps[v]
	s.mu.Unlock()

	var next Snapshot
	switch v {
	case VendorCursor:
		next = s.fetcher.Cursor()
	case VendorGemini:
		next = s.fetcher.Antigravity()
	case VendorClaude:
		next = s.fetcher.Claude()
	case VendorCodex:
		next = s.fetcher.Codex()
	case VendorGrok:
		next = s.fetcher.Grok()
	case VendorCopilot:
		next = s.fetcher.Copilot()
	case VendorDeepSeek:
		next = s.fetcher.DeepSeek()
	case VendorKimi:
		next = s.fetcher.Kimi()
	case VendorReasonix:
		next = s.fetcher.Reasonix()
	case VendorMuse:
		next = s.fetcher.Muse()
	case VendorOllama:
		next = s.fetcher.Ollama()
	default:
		next = StatusSnapshot(v, StateMissing, "Unknown vendor")
	}

	s.mu.Lock()
	if next.State == StateFailed && prev.State == StateOK && len(prev.Windows) > 0 {
		prev.Error = "Using last known values (" + next.Error + ")"
		next = prev
	}
	s.snaps[v] = next
	delete(s.busy, v)
	s.mu.Unlock()

	s.saveCache()
	return next
}

func (s *Store) RefreshAll() {
	var wg sync.WaitGroup
	for _, v := range AllVendors {
		wg.Add(1)
		go func(ven Vendor) {
			defer wg.Done()
			s.RefreshVendor(ven)
		}(v)
	}
	wg.Wait()
}

func (s *Store) StartBackgroundLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		s.RefreshAll()
		for range ticker.C {
			s.RefreshAll()
		}
	}()
}
