package profiles

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Limit is how many profiles the store keeps; connecting to a new host
// beyond it drops the least recently used.
const Limit = 10

// file is what the list read from disk: the profiles, oldest save first,
// so an older file loads as it is.
type file struct {
	Version   int       `json:"version"`
	Profiles  []Profile `json:"profiles"`
	LRUTicket int       `json:"lruTicket,omitempty"` // falls back to profile order
}

// Store holds the saved profiles. Its methods are safe from any
// goroutine, and each save writes the whole file.
type Store struct {
	mu     sync.Mutex
	path   string
	byID   map[string]Profile
	ticket int // grows with every use; LRU is the lowest ticket
	sel    string
}

// Open reads the store at dir/profiles.json, empty when none is there.
func Open(dir string) (*Store, error) {
	s := &Store{path: filepath.Join(dir, "profiles.json"), byID: map[string]Profile{}}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		// A broken file stays readable as far as it parses; the store is
		// empty rather than the app failing to open, and saving replaces
		// it.
		return s, nil
	}
	if f.Version != 1 && f.Version != 0 {
		return s, fmt.Errorf("profiles: unknown version %d", f.Version)
	}
	now := time.Now()
	for _, p := range f.Profiles {
		if err := p.validate(); err != nil {
			continue // skip junk entries, keep the good ones
		}
		if p.CreatedAt.IsZero() {
			p.CreatedAt = now
		}
		p.LastUsed = p.CreatedAt
		s.byID[p.ID] = p
	}
	// LRUTicket 0 means no tickets were saved: the saved order is the
	// recency, first saved first used.
	if f.LRUTicket <= 0 {
		names := f.Profiles
		sort.SliceStable(names, func(i, j int) bool { return names[i].CreatedAt.Before(names[j].CreatedAt) })
		n := 1
		for _, p := range names {
			if q, ok := s.byID[p.ID]; ok {
				s.byID[p.ID] = q
				n++
			}
		}
		s.ticket = n
	} else {
		s.ticket = f.LRUTicket
	}
	return s, nil
}

// Validate reports whether the profile has everything a connection
// needs.
func (p Profile) validate() error {
	if p.Host == "" {
		return fmt.Errorf("no host")
	}
	if p.User == "" {
		return fmt.Errorf("no user")
	}
	switch p.Auth {
	case Password:
		// nothing more is required
	case Key:
		if p.KeyPath == "" {
			return fmt.Errorf("no key path")
		}
	default:
		return fmt.Errorf("unknown auth %q", p.Auth)
	}
	return nil
}

// Valid reports whether the profile has everything a connection needs.
func (p Profile) Valid() bool { return p.validate() == nil }

// List returns every profile, most recently used first.
func (s *Store) List() []Profile {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Profile, 0, len(s.byID))
	for _, p := range s.byID {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastUsed.After(out[j].LastUsed)
	})
	return out
}

// Selected rises the profile the list shows as chosen, empty for none.
func (s *Store) Selected() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sel
}

// Select marks the profile with id as chosen, or none with an empty or
// unknown id.
func (s *Store) Select(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		s.sel = ""
		return
	}
	if _, ok := s.byID[id]; ok {
		s.sel = id
	} else {
		s.sel = ""
	}
}

// Save adds the profile, or replaces the one with its ID, and uses it:
// it moves to the top of the list and stays filled in for next time.
// The profile must be complete. The list never holds more than Limit.
func (s *Store) Save(p Profile) (Profile, error) {
	if p.ID == "" {
		p.ID, _ = newID()
	}
	if p.Label == "" {
		p.Label = p.Host
	}
	if err := p.validate(); err != nil {
		return p, fmt.Errorf("profiles: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if old, ok := s.byID[p.ID]; ok {
		p.CreatedAt = old.CreatedAt
	} else {
		p.CreatedAt = now
	}
	p.LastUsed = now
	s.byID[p.ID] = p
	s.sel = p.ID
	s.ticket++
	if err := s.write(); err != nil {
		return p, err
	}
	return p, nil
}

// Touch records a use of the saved profile, for LRU and the list's
// order.
func (s *Store) Touch(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.byID[id]
	if !ok {
		return fmt.Errorf("profiles: unknown profile")
	}
	p.LastUsed = time.Now()
	s.byID[id] = p
	s.ticket++
	return s.write()
}

// Remove takes the profile out of the store.
func (s *Store) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[id]; !ok {
		return nil
	}
	delete(s.byID, id)
	if s.sel == id {
		s.sel = ""
	}
	return s.write()
}

// write saves to disk under s.mu. Dropping beyond the limit is LRU.
// Secrets never ride along: the profile holds none.
func (s *Store) write() error {
	for len(s.byID) > Limit {
		lowest := ""
		oldest := time.Time{}
		first := true
		for id, p := range s.byID {
			if first || p.LastUsed.Before(oldest) {
				lowest, oldest, first = id, p.LastUsed, false
			}
		}
		delete(s.byID, lowest)
	}
	ps := make([]Profile, 0, len(s.byID))
	for _, p := range s.byID {
		ps = append(ps, p)
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].LastUsed.Before(ps[j].LastUsed) })
	f := file{Version: 1, Profiles: ps, LRUTicket: s.ticket}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// newID is a random ID, an error only when the system has no
// randomness.
func newID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
