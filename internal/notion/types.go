package notion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

func richTextValue(parts []richText) string {
	var out strings.Builder
	for _, part := range parts {
		if part.Plain != "" {
			out.WriteString(part.Plain)
		} else {
			out.WriteString(part.Text.Content)
		}
	}
	return out.String()
}

func parseAPIProperty(name string, raw json.RawMessage) Property {
	var value struct {
		ID       string     `json:"id"`
		Type     string     `json:"type"`
		Title    []richText `json:"title"`
		RichText []richText `json:"rich_text"`
		Number   *float64   `json:"number"`
		Checkbox bool       `json:"checkbox"`
		URL      *string    `json:"url"`
		Email    *string    `json:"email"`
		Phone    *string    `json:"phone_number"`
		Created  string     `json:"created_time"`
		Edited   string     `json:"last_edited_time"`
		Select   *struct {
			Name string `json:"name"`
		} `json:"select"`
		Status *struct {
			Name string `json:"name"`
		} `json:"status"`
		Multi []struct {
			Name string `json:"name"`
		} `json:"multi_select"`
		People    []struct{ ID, Name string }  `json:"people"`
		CreatedBy *struct{ ID, Name string }   `json:"created_by"`
		EditedBy  *struct{ ID, Name string }   `json:"last_edited_by"`
		Date      *struct{ Start, End string } `json:"date"`
		Relation  []struct {
			ID string `json:"id"`
		} `json:"relation"`
		UniqueID *struct {
			Prefix *string `json:"prefix"`
			Number int     `json:"number"`
		} `json:"unique_id"`
		Formula json.RawMessage `json:"formula"`
		Rollup  json.RawMessage `json:"rollup"`
	}
	_ = json.Unmarshal(raw, &value)
	property := Property{ID: value.ID, Name: name, Type: value.Type}
	switch value.Type {
	case "title":
		property.Text = richTextValue(value.Title)
	case "rich_text":
		property.Text = richTextValue(value.RichText)
	case "number":
		if value.Number != nil {
			property.Text = strconv.FormatFloat(*value.Number, 'f', -1, 64)
		}
	case "checkbox":
		property.Text = map[bool]string{true: "✓", false: "○"}[value.Checkbox]
	case "select":
		if value.Select != nil {
			property.Text = value.Select.Name
			property.Values = []string{value.Select.Name}
		}
	case "status":
		if value.Status != nil {
			property.Text = value.Status.Name
			property.Values = []string{value.Status.Name}
		}
	case "multi_select":
		for _, option := range value.Multi {
			property.Values = append(property.Values, option.Name)
		}
		property.Text = strings.Join(property.Values, ", ")
	case "people":
		for _, person := range value.People {
			if person.Name != "" {
				property.Values = append(property.Values, person.Name)
				property.People = append(property.People, Person{ID: person.ID, Name: person.Name})
			}
		}
		property.Text = strings.Join(property.Values, ", ")
	case "created_by":
		if value.CreatedBy != nil {
			property.Text = value.CreatedBy.Name
			property.Values = []string{value.CreatedBy.Name}
			property.People = []Person{{ID: value.CreatedBy.ID, Name: value.CreatedBy.Name}}
		}
	case "last_edited_by":
		if value.EditedBy != nil {
			property.Text = value.EditedBy.Name
			property.Values = []string{value.EditedBy.Name}
			property.People = []Person{{ID: value.EditedBy.ID, Name: value.EditedBy.Name}}
		}
	case "date":
		if value.Date != nil {
			property.Text = value.Date.Start
			if value.Date.End != "" {
				property.Text += " – " + value.Date.End
			}
		}
	case "url":
		if value.URL != nil {
			property.Text = *value.URL
		}
	case "email":
		if value.Email != nil {
			property.Text = *value.Email
		}
	case "phone_number":
		if value.Phone != nil {
			property.Text = *value.Phone
		}
	case "created_time":
		property.Text = value.Created
	case "last_edited_time":
		property.Text = value.Edited
	case "relation":
		property.Text = fmt.Sprintf("%d related", len(value.Relation))
	case "unique_id":
		if value.UniqueID != nil {
			prefix := ""
			if value.UniqueID.Prefix != nil {
				prefix = *value.UniqueID.Prefix + "-"
			}
			property.Text = prefix + strconv.Itoa(value.UniqueID.Number)
		}
	case "formula":
		property.Text = scalarPropertyText(value.Formula)
	case "rollup":
		property.Text = scalarPropertyText(value.Rollup)
	}
	return property
}

func scalarPropertyText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	for _, kind := range []string{"string", "number", "boolean", "date"} {
		if item, ok := value[kind]; ok && item != nil {
			if date, ok := item.(map[string]any); ok {
				if start, ok := date["start"].(string); ok {
					return start
				}
			}
			return fmt.Sprint(item)
		}
	}
	return ""
}

type Page struct {
	InTrash       bool   `json:"in_trash,omitempty"`
	ID            string `json:"id"`
	Title         string `json:"title"`
	TitleProperty string `json:"title_property,omitempty"`
	Kind          string `json:"kind"`
	ParentKind    string `json:"parent_kind,omitempty"`
	ParentID      string `json:"parent_id,omitempty"`
	URL           string `json:"url,omitempty"`
	Edited        string `json:"edited,omitempty"`
	Icon          string `json:"icon,omitempty"`
	PropertyData  string `json:"property_data,omitempty"`
}

type Property struct {
	ID     string   `json:"id,omitempty"`
	Name   string   `json:"name"`
	Type   string   `json:"type"`
	Text   string   `json:"text,omitempty"`
	Values []string `json:"values,omitempty"`
	People []Person `json:"people,omitempty"`
}

func (p Page) PropertyValues() []Property {
	var properties []Property
	_ = json.Unmarshal([]byte(p.PropertyData), &properties)
	return properties
}

type Person struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type PeopleListing struct {
	People []Person
	Cursor string
}

type PeopleBackend interface {
	People(context.Context, string) (PeopleListing, error)
}

type PersonBackend interface {
	Person(context.Context, string) (Person, error)
}

type Listing struct {
	Pages  []Page `json:"pages"`
	Cursor string `json:"cursor,omitempty"`
}

type Content struct {
	Object     string   `json:"object"`
	ID         string   `json:"id"`
	Markdown   string   `json:"markdown"`
	Truncated  bool     `json:"truncated"`
	Unknown    []string `json:"unknown_block_ids"`
	Normalized bool     `json:"-"`
}

func (c Content) Editable() bool {
	if c.Object != "page_markdown" || c.Truncated || len(c.Unknown) != 0 {
		return false
	}
	_, err := inspectMarkdown(c.Markdown)
	return err == nil
}

var ErrConflict = errors.New("sync paused — local draft kept; use Compare with Notion from Ctrl+K")
var ErrIncomplete = errors.New("Notion returned incomplete content; this page is read-only")

// SaveAttemptError retains the exact preflight base and merged draft used by an
// ambiguous PATCH. Keeping this pair lets the next preflight reconcile safely
// whether the write reached Notion or not.
type SaveAttemptError struct {
	Err  error
	Base Content
	Text string
}

func (e *SaveAttemptError) Error() string { return e.Err.Error() }
func (e *SaveAttemptError) Unwrap() error { return e.Err }

type Backend interface {
	Search(context.Context, string, string) (Listing, error)
	Query(context.Context, string, string) (Listing, error)
	Page(context.Context, string) (Page, error)
	Read(context.Context, string) (Content, error)
	Save(context.Context, string, string, string) (Content, error)
	Create(context.Context, string, string, string) (Page, Content, error)
}

type RenameBackend interface {
	Rename(context.Context, Page, string) (Page, error)
}

// PendingSave is the exact read/target pair retained when a PATCH response is
// ambiguous. It is persisted with the draft so a restart can reconcile the
// write without repeating or guessing it.
type PendingSave struct {
	Base  Content `json:"base"`
	Text  string  `json:"text"`
	Draft string  `json:"draft"`
}

type Doc struct {
	Page    Page         `json:"page"`
	Base    Content      `json:"base"`
	Text    string       `json:"text"`
	Fetched time.Time    `json:"fetched"`
	Dirty   bool         `json:"dirty"`
	Pending *PendingSave `json:"pending_save,omitempty"`
}

type richText struct {
	Plain string `json:"plain_text"`
	Text  struct {
		Content string `json:"content"`
	} `json:"text"`
}

type apiPage struct {
	InTrash  bool       `json:"in_trash"`
	Archived bool       `json:"archived"`
	ID       string     `json:"id"`
	Object   string     `json:"object"`
	URL      string     `json:"url"`
	Edited   string     `json:"last_edited_time"`
	Title    []richText `json:"title"`
	Icon     *struct {
		Type  string `json:"type"`
		Emoji string `json:"emoji"`
	} `json:"icon"`
	Parent struct {
		Type         string `json:"type"`
		Workspace    bool   `json:"workspace"`
		PageID       string `json:"page_id"`
		BlockID      string `json:"block_id"`
		DatabaseID   string `json:"database_id"`
		DataSourceID string `json:"data_source_id"`
	} `json:"parent"`
	Properties map[string]json.RawMessage `json:"properties"`
}

func (p apiPage) page() Page {
	title := p.Title
	titleProperty := ""
	properties := make([]Property, 0, len(p.Properties))
	if p.Object == "page" {
		for name, raw := range p.Properties {
			property := parseAPIProperty(name, raw)
			properties = append(properties, property)
			if property.Type == "title" {
				var value struct {
					Title []richText `json:"title"`
				}
				_ = json.Unmarshal(raw, &value)
				title = value.Title
				titleProperty = property.ID
				if titleProperty == "" {
					titleProperty = name
				}
			}
		}
	}
	var b strings.Builder
	for _, t := range title {
		if t.Plain != "" {
			b.WriteString(t.Plain)
		} else {
			b.WriteString(t.Text.Content)
		}
	}
	name := strings.TrimSpace(b.String())
	if name == "" {
		name = "Untitled"
	}
	parentKind, parentID := p.Parent.Type, ""
	if p.Parent.Workspace {
		parentKind = "workspace"
	}
	switch parentKind {
	case "block_id":
		parentID = p.Parent.BlockID
	case "page_id":
		parentID = p.Parent.PageID
	case "database_id":
		parentID = p.Parent.DatabaseID
	case "data_source_id":
		parentID = p.Parent.DataSourceID
	}
	icon := ""
	if p.Icon != nil && p.Icon.Type == "emoji" {
		icon = p.Icon.Emoji
	}
	sort.Slice(properties, func(i, j int) bool {
		if properties[i].Type == "title" {
			return true
		}
		if properties[j].Type == "title" {
			return false
		}
		return strings.ToLower(properties[i].Name) < strings.ToLower(properties[j].Name)
	})
	propertyData := ""
	if len(properties) > 0 {
		if encoded, err := json.Marshal(properties); err == nil {
			propertyData = string(encoded)
		}
	}
	return Page{ID: p.ID, InTrash: p.InTrash || p.Archived, Title: name, TitleProperty: titleProperty, Kind: p.Object, ParentKind: parentKind, ParentID: parentID, URL: p.URL, Edited: p.Edited, Icon: icon, PropertyData: propertyData}
}
