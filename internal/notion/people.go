package notion

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

type apiUser struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
	Bot  *struct {
		Owner struct {
			Type string   `json:"type"`
			User *apiUser `json:"user"`
		} `json:"owner"`
	} `json:"bot"`
}

func personFromUser(user apiUser) (Person, bool) {
	if user.Type == "person" && user.ID != "" && strings.TrimSpace(user.Name) != "" {
		return Person{ID: user.ID, Name: strings.TrimSpace(user.Name)}, true
	}
	if user.Bot != nil && user.Bot.Owner.Type == "user" && user.Bot.Owner.User != nil {
		return personFromUser(*user.Bot.Owner.User)
	}
	return Person{}, false
}

// People uses the official user directory when the connection permits it.
// Personal access tokens cannot list users, so the authenticated bot owner's
// person is returned as a dependable fallback instead of exposing credentials.
func (c *Client) People(ctx context.Context, cursor string) (PeopleListing, error) {
	query := url.Values{"page_size": {"100"}}
	if cursor != "" {
		query.Set("start_cursor", cursor)
	}
	var raw struct {
		Results []apiUser `json:"results"`
		Cursor  string    `json:"next_cursor"`
		More    bool      `json:"has_more"`
	}
	err := c.api(ctx, "GET", "v1/users?"+query.Encode(), nil, true, &raw)
	if err == nil {
		listing := PeopleListing{}
		for _, user := range raw.Results {
			if person, ok := personFromUser(user); ok {
				listing.People = append(listing.People, person)
			}
		}
		if raw.More {
			listing.Cursor = raw.Cursor
		}
		return listing, nil
	}
	if cursor != "" || !strings.Contains(strings.ToLower(err.Error()), "restricted_resource") {
		return PeopleListing{}, err
	}
	var me apiUser
	if fallbackErr := c.api(ctx, "GET", "v1/users/me", nil, true, &me); fallbackErr != nil {
		return PeopleListing{}, err
	}
	person, ok := personFromUser(me)
	if !ok {
		return PeopleListing{}, nil
	}
	return PeopleListing{People: []Person{person}}, nil
}

func (c *Client) Person(ctx context.Context, id string) (Person, error) {
	var user apiUser
	if err := c.api(ctx, "GET", "v1/users/"+id, nil, true, &user); err != nil {
		return Person{}, err
	}
	person, ok := personFromUser(user)
	if !ok {
		return Person{}, fmt.Errorf("user %s is not a visible person", id)
	}
	return person, nil
}
