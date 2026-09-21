package notion

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestTrashRequestAndMetadata(t *testing.T) {
	c := NewClient()
	c.interval = 0
	c.run = func(_ context.Context, args []string, body []byte) ([]byte, error) {
		var request map[string]bool
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatal(err)
		}
		if args[1] != "v1/pages/p" || args[3] != "PATCH" || len(request) != 1 || !request["in_trash"] {
			t.Fatalf("unexpected trash request: %v %s", args, body)
		}
		return []byte(`{"object":"page","id":"p","in_trash":true}`), nil
	}
	p, err := c.SetTrash(context.Background(), "p", true)
	if err != nil || !p.InTrash {
		t.Fatalf("trash metadata lost: %+v %v", p, err)
	}
}

func TestLivePageTrashRestore(t *testing.T) {
	parent := os.Getenv("NTTY_TRASH_TEST_PARENT")
	if parent == "" {
		t.Skip("opt-in disposable trash test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := NewClient()
	p, _, err := c.Create(ctx, parent, "ntty trash lifecycle test", "Lifecycle test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, err := c.SetTrash(context.Background(), p.ID, true)
		if err != nil {
			t.Error(err)
		}
	}()
	for _, trash := range []bool{true, false} {
		if _, err := c.SetTrash(ctx, p.ID, trash); err != nil {
			t.Fatal(err)
		}
		read, err := c.Page(ctx, p.ID)
		if err != nil || read.InTrash != trash {
			t.Fatalf("trash read-back=%v want=%v err=%v", read.InTrash, trash, err)
		}
	}
}
