package main

import (
	"strings"
	"testing"

	"ntty/internal/store"
)

func TestIntentDetailsShowExactRequestWithoutClaimingReconciliation(t *testing.T) {
	intent := store.Intent{Kind: "create", Parent: "page:abc", Title: "Same title", Body: "exact\nbody", Attempted: true}
	details := intentDetails(intent)
	if !strings.Contains(details, "Parent: page:abc") || !strings.Contains(details, "Title: Same title") || !strings.Contains(details, "exact\nbody") {
		t.Fatalf("inspection omitted exact request: %q", details)
	}
	if !strings.Contains(intentDescription(intent), "outcome unknown") {
		t.Fatal("attempted request was presented as resolved")
	}
}
