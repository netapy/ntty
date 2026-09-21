package notion

import "context"

type TrashBackend interface {
	SetTrash(context.Context, string, bool) (Page, error)
}

func (c *Client) SetTrash(ctx context.Context, id string, trash bool) (Page, error) {
	var raw apiPage
	err := c.api(ctx, "PATCH", "v1/pages/"+id, map[string]bool{"in_trash": trash}, false, &raw)
	return raw.page(), err
}
