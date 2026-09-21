package store

import (
	"errors"
	"testing"
)

func TestOutboxSurvivesRestartAndLocksAttemptedWrites(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveCommentDraft("page", "discussion", "editable"); err != nil {
		t.Fatal(err)
	}
	reopened, _ := Open(dir)
	intents, err := reopened.Intents()
	if err != nil || len(intents) != 1 || intents[0].Body != "editable" || intents[0].Attempted {
		t.Fatalf("draft did not survive restart: %+v, %v", intents, err)
	}
	started, err := reopened.BeginComment("page", "discussion", "exact body")
	if err != nil {
		t.Fatal(err)
	}
	reopened, _ = Open(dir)
	if _, err := reopened.BeginComment("page", "discussion", "exact body"); !errors.Is(err, ErrIntentPending) {
		t.Fatalf("ambiguous comment could be posted twice: %v", err)
	}
	if err := reopened.SaveCommentDraft("page", "discussion", "changed"); !errors.Is(err, ErrIntentPending) {
		t.Fatalf("attempted request was editable: %v", err)
	}
	if err := reopened.UnlockIntent(started.ID); err != nil {
		t.Fatal(err)
	}
	if err := reopened.SaveCommentDraft("page", "discussion", "changed"); err != nil {
		t.Fatalf("explicitly unlocked draft was not editable: %v", err)
	}
}

func TestCreateIntentRequiresExplicitResolution(t *testing.T) {
	s, _ := Open(t.TempDir())
	intent, err := s.BeginCreate("page:parent", "Title", "body")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginCreate("page:parent", "Title", "body"); !errors.Is(err, ErrIntentPending) {
		t.Fatalf("create could be posted twice: %v", err)
	}
	if err := s.UnlockIntent(intent.ID); err != nil {
		t.Fatal(err)
	}
	second, err := s.BeginCreate("page:parent", "Title", "body")
	if err != nil || second.ID != intent.ID {
		t.Fatalf("explicit retry did not reuse exact intent: %+v, %v", second, err)
	}
	if err := s.CompleteIntent(intent.ID); err != nil {
		t.Fatal(err)
	}
	if intents, _ := s.Intents(); len(intents) != 0 {
		t.Fatalf("known success was not cleared: %+v", intents)
	}
}
