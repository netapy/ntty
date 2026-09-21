package notion

import (
	"context"
	"fmt"
)

// PagePath skips layout blocks (columns, toggles) while retaining navigable parents.
func (c *Client) PagePath(ctx context.Context, id string) ([]Page, error) {
	var chain []Page
	kind := "page_id"
	seen := map[string]bool{}
	for id != "" && kind != "workspace" {
		if seen[id] || len(seen) >= 100 {
			return chain, fmt.Errorf("cyclic or excessively deep page ancestry")
		}
		seen[id] = true
		endpoint := "v1/pages/"
		switch kind {
		case "block_id":
			endpoint = "v1/blocks/"
		case "database_id":
			endpoint = "v1/databases/"
		case "data_source_id":
			endpoint = "v1/data_sources/"
		}
		var raw apiPage
		if err := c.api(ctx, "GET", endpoint+id, nil, true, &raw); err != nil {
			return chain, err
		}
		p := raw.page()
		if kind != "block_id" {
			chain = append(chain, p)
		}
		id, kind = p.ParentID, p.ParentKind
	}
	return chain, nil
}
