package notion

import (
	"context"
	"fmt"
	"net/url"
)

// ViewBackend is optional: offline backends can keep their local layouts.
type ViewBackend interface {
	Views(context.Context, string) ([]DatabaseView, error)
	QueryView(context.Context, string, string, string) (ViewListing, error)
}

type DatabaseView struct {
	ID, Name, Type string
	Configuration  struct {
		GroupBy struct {
			PropertyName string `json:"property_name"`
		} `json:"group_by"`
	} `json:"configuration"`
}

type ViewListing struct {
	Listing
	QueryID    string
	Incomplete bool
}

func (c *Client) Views(ctx context.Context, source string) ([]DatabaseView, error) {
	var views []DatabaseView
	cursor := ""
	for {
		var raw struct {
			Results []DatabaseView
			More    bool   `json:"has_more"`
			Cursor  string `json:"next_cursor"`
		}
		path := "v1/views?data_source_id=" + url.QueryEscape(source) + "&page_size=100"
		if cursor != "" {
			path += "&start_cursor=" + url.QueryEscape(cursor)
		}
		if err := c.api(ctx, "GET", path, nil, true, &raw); err != nil {
			return nil, err
		}
		for _, ref := range raw.Results {
			var view DatabaseView
			if err := c.api(ctx, "GET", "v1/views/"+url.PathEscape(ref.ID), nil, true, &view); err != nil {
				return nil, err
			}
			views = append(views, view)
		}
		if !raw.More {
			return views, nil
		}
		if raw.Cursor == "" || raw.Cursor == cursor {
			return nil, fmt.Errorf("views: missing or repeated pagination cursor")
		}
		cursor = raw.Cursor
	}
}

// QueryView delegates saved filters and sorts to Notion, rather than trying to
// reproduce relative dates, formulas or compound filters on a partial local set.
func (c *Client) QueryView(ctx context.Context, view, query, cursor string) (ViewListing, error) {
	var raw struct {
		ID      string
		Results []struct{ ID string }
		More    bool                  `json:"has_more"`
		Cursor  string                `json:"next_cursor"`
		Status  struct{ Type string } `json:"request_status"`
	}
	path := "v1/views/" + url.PathEscape(view) + "/queries"
	method := "POST"
	// ponytail: hydrate 25 references through the existing rate-limited client;
	// use a bulk endpoint if the public API eventually provides one.
	var body any = map[string]int{"page_size": 25}
	if query != "" {
		method, body = "GET", nil
		path += "/" + url.PathEscape(query) + "?page_size=25&start_cursor=" + url.QueryEscape(cursor)
	}
	if err := c.api(ctx, method, path, body, true, &raw); err != nil {
		return ViewListing{}, err
	}
	if query == "" {
		query = raw.ID
	}
	result := ViewListing{QueryID: query, Incomplete: raw.Status.Type == "incomplete"}
	if raw.More {
		if raw.Cursor == "" || raw.Cursor == cursor {
			return ViewListing{}, fmt.Errorf("view query: missing or repeated pagination cursor")
		}
		result.Cursor = raw.Cursor
	}
	for _, ref := range raw.Results {
		page, err := c.Page(ctx, ref.ID)
		if err != nil {
			return ViewListing{}, fmt.Errorf("view row %s: %w", ref.ID, err)
		}
		result.Pages = append(result.Pages, page)
	}
	return result, nil
}
