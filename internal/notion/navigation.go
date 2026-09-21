package notion

import "context"

type DatabaseBackend interface {
	DataSources(context.Context, string) ([]Page, error)
}

func (c *Client) DataSources(ctx context.Context, id string) ([]Page, error) {
	var database struct {
		DataSources []struct{ ID, Name string } `json:"data_sources"`
	}
	if err := c.api(ctx, "GET", "v1/databases/"+id, nil, true, &database); err != nil {
		return nil, err
	}
	pages := make([]Page, 0, len(database.DataSources))
	for _, source := range database.DataSources {
		pages = append(pages, Page{ID: source.ID, Title: source.Name, Kind: "data_source"})
	}
	return pages, nil
}
