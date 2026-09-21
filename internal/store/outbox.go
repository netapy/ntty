package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var ErrIntentPending = errors.New("an earlier attempt is unresolved; inspect pending writes before retrying")

type Intent struct {
	ID           string    `json:"id"`
	Kind         string    `json:"kind"`
	Parent       string    `json:"parent,omitempty"`
	Title        string    `json:"title,omitempty"`
	Body         string    `json:"body"`
	PageID       string    `json:"page_id,omitempty"`
	DiscussionID string    `json:"discussion_id,omitempty"`
	Attempted    bool      `json:"attempted"`
	Created      time.Time `json:"created"`
}

var outboxMu sync.Mutex

func newIntentID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func (s *Store) loadIntents() ([]Intent, error) {
	data, err := os.ReadFile(filepath.Join(s.Dir, "outbox.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var intents []Intent
	err = json.Unmarshal(data, &intents)
	return intents, err
}

func (s *Store) saveIntents(intents []Intent) error {
	return write(filepath.Join(s.Dir, "outbox.json"), intents)
}

func (s *Store) Intents() ([]Intent, error) {
	outboxMu.Lock()
	defer outboxMu.Unlock()
	return s.loadIntents()
}

// SaveCommentDraft durably keeps editable text. An attempted draft is immutable
// until the user explicitly resolves it, because its POST may have succeeded.
func (s *Store) SaveCommentDraft(pageID, discussionID, body string) error {
	outboxMu.Lock()
	defer outboxMu.Unlock()
	intents, err := s.loadIntents()
	if err != nil {
		return err
	}
	for i := range intents {
		if intents[i].Kind != "comment" || intents[i].PageID != pageID || intents[i].DiscussionID != discussionID {
			continue
		}
		if intents[i].Attempted {
			return ErrIntentPending
		}
		if body == "" {
			intents = append(intents[:i], intents[i+1:]...)
		} else {
			intents[i].Body = body
		}
		return s.saveIntents(intents)
	}
	if body == "" {
		return nil
	}
	id, err := newIntentID()
	if err != nil {
		return err
	}
	intents = append(intents, Intent{ID: id, Kind: "comment", Body: body, PageID: pageID, DiscussionID: discussionID, Created: time.Now()})
	return s.saveIntents(intents)
}

// BeginComment marks the exact durable draft attempted before its POST.
func (s *Store) BeginComment(pageID, discussionID, body string) (Intent, error) {
	outboxMu.Lock()
	defer outboxMu.Unlock()
	intents, err := s.loadIntents()
	if err != nil {
		return Intent{}, err
	}
	for i := range intents {
		if intents[i].Kind != "comment" || intents[i].PageID != pageID || intents[i].DiscussionID != discussionID {
			continue
		}
		if intents[i].Attempted {
			return Intent{}, ErrIntentPending
		}
		intents[i].Body, intents[i].Attempted = body, true
		if err := s.saveIntents(intents); err != nil {
			return Intent{}, err
		}
		return intents[i], nil
	}
	id, err := newIntentID()
	if err != nil {
		return Intent{}, err
	}
	intent := Intent{ID: id, Kind: "comment", Body: body, PageID: pageID, DiscussionID: discussionID, Attempted: true, Created: time.Now()}
	intents = append(intents, intent)
	if err := s.saveIntents(intents); err != nil {
		return Intent{}, err
	}
	return intent, nil
}

// BeginCreate permits only one unresolved create. If explicitly unlocked, the
// exact saved request can be attempted again; different form values are refused.
func (s *Store) BeginCreate(parent, title, body string) (Intent, error) {
	outboxMu.Lock()
	defer outboxMu.Unlock()
	intents, err := s.loadIntents()
	if err != nil {
		return Intent{}, err
	}
	for i := range intents {
		if intents[i].Kind != "create" {
			continue
		}
		if intents[i].Attempted || intents[i].Parent != parent || intents[i].Title != title || intents[i].Body != body {
			return Intent{}, ErrIntentPending
		}
		intents[i].Attempted = true
		if err := s.saveIntents(intents); err != nil {
			return Intent{}, err
		}
		return intents[i], nil
	}
	id, err := newIntentID()
	if err != nil {
		return Intent{}, err
	}
	intent := Intent{ID: id, Kind: "create", Parent: parent, Title: title, Body: body, Attempted: true, Created: time.Now()}
	intents = append(intents, intent)
	if err := s.saveIntents(intents); err != nil {
		return Intent{}, err
	}
	return intent, nil
}

func (s *Store) CompleteIntent(id string) error { return s.removeIntent(id) }

func (s *Store) removeIntent(id string) error {
	outboxMu.Lock()
	defer outboxMu.Unlock()
	intents, err := s.loadIntents()
	if err != nil {
		return err
	}
	for i := range intents {
		if intents[i].ID == id {
			return s.saveIntents(append(intents[:i], intents[i+1:]...))
		}
	}
	return nil
}

// UnlockIntent is the user's explicit assertion that the request did not land.
func (s *Store) UnlockIntent(id string) error {
	outboxMu.Lock()
	defer outboxMu.Unlock()
	intents, err := s.loadIntents()
	if err != nil {
		return err
	}
	for i := range intents {
		if intents[i].ID == id {
			intents[i].Attempted = false
			return s.saveIntents(intents)
		}
	}
	return nil
}
