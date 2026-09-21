package notion

import (
	"context"
	"strings"
	"testing"
)

func TestPeopleListsWorkspaceUsers(t *testing.T) {
	c := NewClient()
	c.interval = 0
	c.run = func(_ context.Context, args []string, _ []byte) ([]byte, error) {
		if !strings.Contains(args[1], "v1/users?") {
			t.Fatalf("unexpected request: %v", args)
		}
		return []byte(`{"object":"list","results":[{"object":"user","id":"person-a","name":"Ada","type":"person","avatar_url":null,"person":{"email":"ada@example.com","email_verified":true}},{"object":"user","id":"bot","name":"Bot","type":"bot","avatar_url":null,"bot":{}}],"has_more":true,"next_cursor":"next","type":"user","user":{}}`), nil
	}
	listing, err := c.People(context.Background(), "")
	if err != nil || len(listing.People) != 1 || listing.People[0].ID != "person-a" || listing.People[0].Name != "Ada" || listing.Cursor != "next" {
		t.Fatalf("people listing: %+v err=%v", listing, err)
	}
}

func TestPeopleFallsBackToPersonalTokenOwner(t *testing.T) {
	c := NewClient()
	c.interval = 0
	requests := 0
	c.run = func(_ context.Context, args []string, _ []byte) ([]byte, error) {
		requests++
		if strings.Contains(args[1], "v1/users?") {
			return []byte(`{"object":"error","status":403,"code":"restricted_resource","message":"Personal access tokens cannot list users."}`), nil
		}
		if args[1] != "v1/users/me" {
			t.Fatalf("unexpected fallback: %v", args)
		}
		return []byte(`{"object":"user","id":"bot","name":"Notion CLI","type":"bot","avatar_url":null,"bot":{"owner":{"type":"user","user":{"object":"user","id":"owner","name":"Alex","type":"person","avatar_url":null,"person":{"email":"alex@example.com","email_verified":true}}}}}`), nil
	}
	listing, err := c.People(context.Background(), "")
	if err != nil || requests != 2 || len(listing.People) != 1 || listing.People[0] != (Person{ID: "owner", Name: "Alex"}) {
		t.Fatalf("owner fallback: requests=%d listing=%+v err=%v", requests, listing, err)
	}
}

func TestPersonRetrievesKnownMentionTarget(t *testing.T) {
	c := NewClient()
	c.interval = 0
	c.run = func(_ context.Context, args []string, _ []byte) ([]byte, error) {
		if args[1] != "v1/users/person-a" {
			t.Fatalf("unexpected request: %v", args)
		}
		return []byte(`{"object":"user","id":"person-a","name":"Ada","type":"person","avatar_url":null,"person":{"email":"ada@example.com","email_verified":true}}`), nil
	}
	person, err := c.Person(context.Background(), "person-a")
	if err != nil || person != (Person{ID: "person-a", Name: "Ada"}) {
		t.Fatalf("person: %+v err=%v", person, err)
	}
}
