package main

import (
	"context"
	"sync"

	"ntty/internal/notion"
	"ntty/internal/store"
)

type queuedDraft struct {
	seq uint64
	doc notion.Doc
}

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
	w.mu.Lock()
	w.next++
	w.latest[doc.Page.ID] = w.next
	w.pending[doc.Page.ID] = queuedDraft{w.next, doc}
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
			for id, q = range w.pending {
				delete(w.pending, id)
				break
			}
			w.mu.Unlock()
			if id == "" {
				break
			}
			err := w.store.SaveDoc(q.doc)
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
