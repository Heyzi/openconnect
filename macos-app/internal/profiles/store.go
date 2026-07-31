package profiles

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

type document struct {
	Version  int       `json:"version"`
	Profiles []Profile `json:"profiles"`
}
type Store struct {
	mu       sync.RWMutex
	path     string
	profiles []Profile
}

func NewStore(path string) (*Store, error) {
	s := &Store{path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var d document
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	s.profiles = d.Profiles
	return s, nil
}

func (s *Store) List() []Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Profile{}, s.profiles...)
}
func (s *Store) Get(id string) (Profile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.profiles {
		if p.ID == id {
			return p, true
		}
	}
	return Profile{}, false
}
func (s *Store) Save(p Profile) (Profile, error) {
	p.Password = ""
	if err := p.Validate(); err != nil {
		return Profile{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := append([]Profile{}, s.profiles...)
	found := false
	for i := range next {
		if next[i].ID == p.ID {
			next[i] = p
			found = true
			break
		}
	}
	if !found {
		next = append(next, p)
	}
	if err := s.persist(next); err != nil {
		return Profile{}, err
	}
	s.profiles = next
	return p, nil
}
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.profiles {
		if s.profiles[i].ID == id {
			next := append([]Profile{}, s.profiles[:i]...)
			next = append(next, s.profiles[i+1:]...)
			if err := s.persist(next); err != nil {
				return err
			}
			s.profiles = next
			return nil
		}
	}
	return os.ErrNotExist
}
func (s *Store) persist(profiles []Profile) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	clean := append([]Profile{}, profiles...)
	for i := range clean {
		clean[i].Password = ""
	}
	b, err := json.MarshalIndent(document{Version: 1, Profiles: clean}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
