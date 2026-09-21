package notion

import (
	"context"
	"fmt"
	"os"
	"testing"
)

func TestPagePathThroughLayoutBlocks(t *testing.T) {
	c := NewClient()
	c.interval = 0
	responses := map[string]string{
		"v1/pages/child":    `{"id":"child","object":"page","parent":{"type":"block_id","block_id":"column"}}`,
		"v1/blocks/column":  `{"id":"column","object":"block","parent":{"type":"block_id","block_id":"columns"}}`,
		"v1/blocks/columns": `{"id":"columns","object":"block","parent":{"type":"page_id","page_id":"root"}}`,
		"v1/pages/root":     `{"id":"root","object":"page","parent":{"type":"workspace","workspace":true}}`,
	}
	c.run = func(_ context.Context, args []string, _ []byte) ([]byte, error) {
		if response, ok := responses[args[1]]; ok {
			return []byte(response), nil
		}
		return nil, fmt.Errorf("unexpected request %v", args)
	}
	chain, err := c.PagePath(context.Background(), "child")
	if err != nil || len(chain) != 2 || chain[0].ID != "child" || chain[1].ID != "root" {
		t.Fatalf("path=%+v err=%v", chain, err)
	}
}

func TestLiveConformityPath(t *testing.T) {
	if os.Getenv("NTTY_LIVE_PATH") != "1" {
		t.Skip("opt-in read-only ancestry check")
	}
	id := os.Getenv("NTTY_LIVE_PATH_PAGE")
	if id == "" {
		t.Skip("set NTTY_LIVE_PATH_PAGE")
	}
	chain, err := NewClient().PagePath(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) < 2 {
		t.Fatalf("unexpected path: %+v", chain)
	}
	for _, p := range chain {
		t.Log(p.Title, p.ID)
	}
}
