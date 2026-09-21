package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"ntty/internal/notion"
)

type State struct {
	Pages            []notion.Page     `json:"pages"`
	Pins             []notion.Page     `json:"pins"`
	Recents          []notion.Page     `json:"recents,omitempty"`
	Last             string            `json:"last,omitempty"`
	WorkspaceIndexed time.Time         `json:"workspace_indexed,omitempty"`
	SidebarWidth     int               `json:"sidebar_width,omitempty"`
	SidebarHidden    bool              `json:"sidebar_hidden,omitempty"`
	FavoritesClosed  bool              `json:"favorites_closed,omitempty"`
	RecentsClosed    bool              `json:"recents_closed,omitempty"`
	WorkspaceClosed  bool              `json:"workspace_closed,omitempty"`
	ExpandedPages    []string          `json:"expanded_pages,omitempty"`
	FullWidth        bool              `json:"full_width,omitempty"`
	People           []notion.Person   `json:"people,omitempty"`
	DatabaseViews    map[string]string `json:"database_views,omitempty"`
	DatabaseGroups   map[string]string `json:"database_groups,omitempty"`
	DatabaseSorts    map[string]string `json:"database_sorts,omitempty"`
}

type Store struct{ Dir string }

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "pages"), 0700); err != nil {
		return nil, err
	}
	return &Store{Dir: dir}, nil
}

func write(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".draft-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

var validID = regexp.MustCompile(`^[a-zA-Z0-9-]+$`)

func (s *Store) docPath(id string) (string, error) {
	if !validID.MatchString(id) {
		return "", fmt.Errorf("invalid page ID")
	}
	return filepath.Join(s.Dir, "pages", id+".json"), nil
}

func (s *Store) SaveDoc(d notion.Doc) error {
	path, err := s.docPath(d.Page.ID)
	if err != nil {
		return err
	}
	return write(path, d)
}

func (s *Store) LoadDoc(id string) (notion.Doc, error) {
	var doc notion.Doc
	path, err := s.docPath(id)
	if err != nil {
		return doc, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return doc, err
	}
	err = json.Unmarshal(data, &doc)
	return doc, err
}

func (s *Store) Drafts() ([]notion.Doc, error) {
	files, err := filepath.Glob(filepath.Join(s.Dir, "pages", "*.json"))
	if err != nil {
		return nil, err
	}
	var docs []notion.Doc
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var d notion.Doc
		if err := json.Unmarshal(data, &d); err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		if d.Dirty || d.Pending != nil {
			docs = append(docs, d)
		}
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Fetched.After(docs[j].Fetched) })
	return docs, nil
}

func (s *Store) LoadState() (State, error) {
	var state State
	data, err := os.ReadFile(filepath.Join(s.Dir, "state.json"))
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	err = json.Unmarshal(data, &state)
	return state, err
}

func (s *Store) SaveState(state State) error { return write(filepath.Join(s.Dir, "state.json"), state) }
