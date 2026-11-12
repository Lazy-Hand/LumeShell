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

	"lumeshell/internal/atomicfile"
)

// file is what the list read from disk: the profiles, least recently
// used first, so an older file without LastUsed loads as it is.
type file struct {
	Version  int       `json:"version"`
	Profiles []Profile `json:"profiles"`
}

// Store holds the saved profiles. Its methods are safe from any
// goroutine, and each save writes the whole file.
type Store struct {
	mu   sync.Mutex
	path string
	byID map[string]Profile
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
		if p.ID == "" {
			continue
		}
		if p.CreatedAt.IsZero() {
			p.CreatedAt = now
		}
		if p.LastUsed.IsZero() {
			p.LastUsed = p.CreatedAt
		}
		s.byID[p.ID] = p
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

// Save adds the profile, or replaces the one with its ID, and uses it:
// it moves to the top of the list and stays filled in for next time.
// The profile must be complete.
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
	if err := s.write(); err != nil {
		return p, err
	}
	return p, nil
}

// Touch records a use of the saved profile, for the list's order.
func (s *Store) Touch(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.byID[id]
	if !ok {
		return fmt.Errorf("profiles: unknown profile")
	}
	p.LastUsed = time.Now()
	s.byID[id] = p
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
	return s.write()
}

// write saves to disk under s.mu. Secrets never ride along: the profile
// holds none.
func (s *Store) write() error {
	ps := make([]Profile, 0, len(s.byID))
	for _, p := range s.byID {
		ps = append(ps, p)
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].LastUsed.Before(ps[j].LastUsed) })
	return atomicfile.WriteJSON(s.path, file{Version: 1, Profiles: ps})
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
