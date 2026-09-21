package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ntty/internal/notion"
)

const MaxRevisions = 50

// Revision preserves the original document state. Restoring one should copy its
// text into a local draft, not replace the current document's sync base.
type Revision struct {
	At     time.Time  `json:"at"`
	Reason string     `json:"reason"`
	Doc    notion.Doc `json:"doc"`
}

// The app's profile lock excludes other processes; this also serializes
// read-modify-write calls made by goroutines or separate Store values.
var revisionMu sync.Mutex

func (s *Store) revisionsPath(id string) (string, error) {
	path, err := s.docPath(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.Dir, "revisions", filepath.Base(path)), nil
}

// SaveRevision stores a snapshot, newest first, retaining at most MaxRevisions.
// Consecutive identical text is deduplicated without changing the older
// snapshot's timestamp, reason, or metadata. Revisiting text after another edit
// is a new revision, so a backup cannot disappear with the oldest retained entry.
// An unreadable history is never overwritten.
func (s *Store) SaveRevision(doc notion.Doc, reason string) error {
	path, err := s.revisionsPath(doc.Page.ID)
	if err != nil {
		return err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return fmt.Errorf("revision reason is required")
	}
	revisionMu.Lock()
	defer revisionMu.Unlock()

	revisions, err := s.Revisions(doc.Page.ID)
	if err != nil {
		return err
	}
	if len(revisions) > 0 && revisions[0].Doc.Text == doc.Text {
		return nil
	}
	revisions = append([]Revision{{At: time.Now().UTC(), Reason: reason, Doc: doc}}, revisions...)
	if len(revisions) > MaxRevisions {
		revisions = revisions[:MaxRevisions]
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create local revisions: %w", err)
	}
	if err := write(path, revisions); err != nil {
		return fmt.Errorf("save local revision: %w", err)
	}
	return nil
}

// Revisions returns independent snapshots in newest-first order. A page with
// no local history has no revisions; malformed or unreadable history is an error.
func (s *Store) Revisions(id string) ([]Revision, error) {
	path, err := s.revisionsPath(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read local revisions: %w", err)
	}
	var revisions []Revision
	if err := json.Unmarshal(data, &revisions); err != nil {
		return nil, fmt.Errorf("read local revisions: %w", err)
	}
	if revisions == nil || len(revisions) > MaxRevisions {
		return nil, fmt.Errorf("invalid local revision history for %s", id)
	}
	for i, r := range revisions {
		if r.At.IsZero() || strings.TrimSpace(r.Reason) == "" || r.Doc.Page.ID != id {
			return nil, fmt.Errorf("invalid local revision %d for %s", i+1, id)
		}
	}
	return revisions, nil
}
