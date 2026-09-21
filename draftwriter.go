package main

import (
	"context"
	"maps"
	"slices"
	"sync"

	"ntty/internal/notion"
	"ntty/internal/store"
)

type queuedDraft struct {
	seq     uint64
	doc     notion.Doc
	state   *store.State
	comment *store.Intent
}

const stateWriteID = "workspace state" // Not a valid page ID.

// draftWriter serializes and coalesces atomic draft writes so disk sync never
// blocks typing. Flush is used before remote writes and shutdown.
type draftWriter struct {
	store     *store.Store
	mu        sync.Mutex
	wg        sync.WaitGroup
	cond      *sync.Cond
	wake      chan struct{}
	pending   map[string]queuedDraft
	latest    map[string]uint64
	completed map[string]uint64
	errs      map[string]error
	next      uint64
}

func newDraftWriter(ctx context.Context, s *store.Store) *draftWriter {
	w := &draftWriter{store: s, wake: make(chan struct{}, 1), pending: map[string]queuedDraft{}, latest: map[string]uint64{}, completed: map[string]uint64{}, errs: map[string]error{}}
	w.cond = sync.NewCond(&w.mu)
	w.wg.Add(1)
	go w.run(ctx)
	return w
}

func (w *draftWriter) Put(doc notion.Doc) {
	w.put(doc.Page.ID, queuedDraft{doc: doc})
}

func (w *draftWriter) PutState(state store.State) {
	// ponytail: reuse the atomic writer, with an immutable snapshot. Drafts
	// have priority; FlushAll also makes the latest workspace state durable.
	state.Pages = slices.Clone(state.Pages)
	state.Pins = slices.Clone(state.Pins)
	state.Recents = slices.Clone(state.Recents)
	state.People = slices.Clone(state.People)
	state.ExpandedPages = slices.Clone(state.ExpandedPages)
	state.DatabaseViews = maps.Clone(state.DatabaseViews)
	state.DatabaseGroups = maps.Clone(state.DatabaseGroups)
	state.DatabaseSorts = maps.Clone(state.DatabaseSorts)
	w.put(stateWriteID, queuedDraft{state: &state})
}

func commentDraftID(page, discussion string) string { return "comment:" + page + ":" + discussion }

func (w *draftWriter) PutCommentDraft(page, discussion, body string) {
	w.put(commentDraftID(page, discussion), queuedDraft{comment: &store.Intent{PageID: page, DiscussionID: discussion, Body: body}})
}

func (w *draftWriter) put(id string, q queuedDraft) {
	w.mu.Lock()
	w.next++
	q.seq = w.next
	w.latest[id] = w.next
	w.pending[id] = q
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *draftWriter) run(ctx context.Context) {
	defer w.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.wake:
		}
		for {
			w.mu.Lock()
			var id string
			var q queuedDraft
			for key, value := range w.pending {
				id, q = key, value
				if key != stateWriteID {
					break
				}
			}
			delete(w.pending, id)
			w.mu.Unlock()
			if id == "" {
				break
			}
			var err error
			if q.state != nil {
				err = w.store.SaveState(*q.state)
			} else if q.comment != nil {
				err = w.store.SaveCommentDraft(q.comment.PageID, q.comment.DiscussionID, q.comment.Body)
			} else {
				err = w.store.SaveDoc(q.doc)
			}
			w.mu.Lock()
			if q.seq > w.completed[id] {
				w.completed[id] = q.seq
				w.errs[id] = err
			}
			w.cond.Broadcast()
			w.mu.Unlock()
		}
	}
}

func (w *draftWriter) Wait() { w.wg.Wait() }

func (w *draftWriter) Flush(id string) error {
	w.mu.Lock()
	target := w.latest[id]
	select {
	case w.wake <- struct{}{}:
	default:
	}
	for w.completed[id] < target {
		w.cond.Wait()
	}
	err := w.errs[id]
	w.mu.Unlock()
	return err
}

func (w *draftWriter) FlushAll() error {
	w.mu.Lock()
	targets := make(map[string]uint64, len(w.latest))
	for id, seq := range w.latest {
		targets[id] = seq
	}
	select {
	case w.wake <- struct{}{}:
	default:
	}
	for id, target := range targets {
		for w.completed[id] < target {
			w.cond.Wait()
		}
		if err := w.errs[id]; err != nil {
			w.mu.Unlock()
			return err
		}
	}
	w.mu.Unlock()
	return nil
}

func (w *draftWriter) Errors() map[string]error {
	w.mu.Lock()
	defer w.mu.Unlock()
	errs := map[string]error{}
	for id, err := range w.errs {
		if err != nil && w.completed[id] == w.latest[id] {
			errs[id] = err
		}
	}
	return errs
}
