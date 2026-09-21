package notion

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveNativeViewQuery(t *testing.T) {
	id := os.Getenv("NTTY_LIVE_VIEW_SOURCE")
	if id == "" {
		t.Skip("opt-in live view query")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := NewClient()
	views, err := c.Views(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) == 0 {
		t.Fatal("no native views")
	}
	rows, err := c.QueryView(ctx, views[0].ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("native view %q (%s): %d rows, incomplete=%v", views[0].Name, views[0].Type, len(rows.Pages), rows.Incomplete)
}

func TestNativeViewsAndQueryPagination(t *testing.T) {
	c := NewClient()
	c.interval = 0
	var paths []string
	c.run = func(_ context.Context, args []string, body []byte) ([]byte, error) {
		path := args[1]
		paths = append(paths, path)
		switch {
		case strings.HasPrefix(path, "v1/views?data_source_id=source"):
			return []byte(`{"results":[{"id":"view"}],"has_more":false}`), nil
		case path == "v1/views/view":
			return []byte(`{"id":"view","name":"Open tasks","type":"board","configuration":{"group_by":{"property_name":"Status"}}}`), nil
		case path == "v1/views/view/queries":
			if args[3] != "POST" || !strings.Contains(string(body), `"page_size":25`) {
				t.Fatalf("query request: %v %s", args, body)
			}
			return []byte(`{"id":"query","results":[{"id":"p1"}],"has_more":true,"next_cursor":"next"}`), nil
		case path == "v1/views/view/queries/query?page_size=25&start_cursor=next":
			if args[3] != "GET" || body != nil {
				t.Fatalf("pagination request: %v %s", args, body)
			}
			return []byte(`{"results":[{"id":"p2"}],"has_more":false,"request_status":{"type":"incomplete"}}`), nil
		case strings.HasPrefix(path, "v1/pages/"):
			id := strings.TrimPrefix(path, "v1/pages/")
			return []byte(`{"id":"` + id + `","object":"page","properties":{"Name":{"type":"title","title":[{"plain_text":"Task"}]}}}`), nil
		default:
			t.Fatalf("unexpected request: %s", path)
			return nil, nil
		}
	}
	views, err := c.Views(context.Background(), "source")
	if err != nil || len(views) != 1 || views[0].Configuration.GroupBy.PropertyName != "Status" {
		t.Fatalf("views: %+v %v", views, err)
	}
	first, err := c.QueryView(context.Background(), "view", "", "")
	if err != nil || first.QueryID != "query" || first.Cursor != "next" || len(first.Pages) != 1 || first.Pages[0].ID != "p1" {
		t.Fatalf("first: %+v %v", first, err)
	}
	second, err := c.QueryView(context.Background(), "view", first.QueryID, first.Cursor)
	if err != nil || second.QueryID != "query" || second.Cursor != "" || !second.Incomplete || len(second.Pages) != 1 || second.Pages[0].ID != "p2" {
		t.Fatalf("second: %+v %v", second, err)
	}
	if len(paths) != 6 {
		t.Fatalf("requests: %v", paths)
	}
}

func TestViewQueryRejectsBrokenPaginationAndRowErrors(t *testing.T) {
	for _, response := range []string{
		`{"id":"query","results":[],"has_more":true}`,
		`{"id":"query","results":[{"id":"denied"}],"has_more":false}`,
	} {
		c := NewClient()
		c.interval = 0
		c.run = func(_ context.Context, args []string, _ []byte) ([]byte, error) {
			if args[1] == "v1/pages/denied" {
				return []byte(`{"object":"error","status":403,"code":"restricted_resource","message":"No access"}`), nil
			}
			return []byte(response), nil
		}
		result, err := c.QueryView(context.Background(), "view", "", "")
		if err == nil || len(result.Pages) != 0 {
			t.Fatalf("must not present partial results as complete: %+v %v", result, err)
		}
	}
}
