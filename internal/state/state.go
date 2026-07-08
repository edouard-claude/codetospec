// Package state persists run progress and token costs as JSON, enabling
// resume after interruption.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// PhaseTokens accumulates token usage for one phase.
type PhaseTokens struct {
	Prompt     int `json:"prompt"`
	Completion int `json:"completion"`
}

// State is the persisted run state.
type State struct {
	Version       int                     `json:"version"`
	SrcPath       string                  `json:"src_path"`
	StartedAt     string                  `json:"started_at"`
	Phase         string                  `json:"phase"`
	ChunksDone    int                     `json:"chunks_done"`
	ChunksFailed  int                     `json:"chunks_failed"`
	ChunksTotal   int                     `json:"chunks_total"`
	DomainsDone   int                     `json:"domains_done"`
	DomainsFailed int                     `json:"domains_failed"`
	DomainsTotal  int                     `json:"domains_total"`
	Extractors    map[string]string       `json:"extractors"`
	Tokens        map[string]*PhaseTokens `json:"tokens"`
}

// Store persists State atomically after every mutation.
type Store struct {
	mu   sync.Mutex
	path string
	s    State
}

// Open loads the state file when present, otherwise initializes a fresh
// state for srcPath.
func Open(path, srcPath string) (*Store, error) {
	store := &Store{path: path}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if unmarshalErr := json.Unmarshal(data, &store.s); unmarshalErr != nil {
			return nil, fmt.Errorf("load state %s: %w", path, unmarshalErr)
		}
	case errors.Is(err, fs.ErrNotExist):
		store.s = State{
			Version:   1,
			StartedAt: time.Now().UTC().Format(time.RFC3339),
		}
	default:
		return nil, fmt.Errorf("load state %s: %w", path, err)
	}
	store.s.SrcPath = srcPath
	if store.s.Extractors == nil {
		store.s.Extractors = map[string]string{}
	}
	if store.s.Tokens == nil {
		store.s.Tokens = map[string]*PhaseTokens{}
	}
	for _, phase := range []string{"map", "reduce"} {
		if store.s.Tokens[phase] == nil {
			store.s.Tokens[phase] = &PhaseTokens{}
		}
	}
	return store, nil
}

// Load reads a state file without creating a store; used by the stats command.
func Load(path string) (State, error) {
	var s State
	data, err := os.ReadFile(path)
	if err != nil {
		return s, fmt.Errorf("load state %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("load state %s: %w", path, err)
	}
	return s, nil
}

// Update mutates the state under lock and saves it atomically.
func (st *Store) Update(fn func(*State)) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	fn(&st.s)
	return st.saveLocked()
}

// Save persists the current state atomically.
func (st *Store) Save() error {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.saveLocked()
}

// Snapshot returns a copy of the current state.
func (st *Store) Snapshot() State {
	st.mu.Lock()
	defer st.mu.Unlock()
	snapshot := st.s
	snapshot.Extractors = maps.Clone(st.s.Extractors)
	snapshot.Tokens = make(map[string]*PhaseTokens, len(st.s.Tokens))
	for k, v := range st.s.Tokens {
		copied := *v
		snapshot.Tokens[k] = &copied
	}
	return snapshot
}

func (st *Store) saveLocked() error {
	data, err := json.MarshalIndent(st.s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	return WriteFileAtomic(st.path, append(data, '\n'))
}

// WriteFileAtomic writes data to path via a temporary file and rename, so a
// crash never leaves a truncated file behind.
func WriteFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create dir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename to %s: %w", path, err)
	}
	return nil
}
