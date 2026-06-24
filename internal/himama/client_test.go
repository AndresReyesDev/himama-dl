package himama

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// The CSRF token meta tag is served in different shapes depending on how the
// markup is minified: lillio currently emits it unquoted, but Rails' default
// csrf_meta_tags helper emits it double-quoted. Both must parse to the token.
func TestExtractCSRFToken(t *testing.T) {
	const token = "S6RZN2HUF6sIC7pzk9HE1gUXLghHI3fTXxQAgvn38Z0QzJhTpn7Gpw879IssIcNQqAa-IzRJSV2Hkr0jyfsJEg"

	cases := map[string]string{
		"unquoted (live lillio format)": `<head><meta name=csrf-token content=` + token + ` /></head>`,
		"double-quoted (rails default)": `<head><meta name="csrf-token" content="` + token + `" /></head>`,
		"attributes reordered":          `<head><meta content="` + token + `" name="csrf-token"></head>`,
	}

	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := extractCSRFToken(strings.NewReader(doc))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != token {
				t.Fatalf("got %q, want %q", got, token)
			}
		})
	}
}

func TestExtractCSRFTokenMissing(t *testing.T) {
	if _, err := extractCSRFToken(strings.NewReader(`<head></head>`)); err == nil {
		t.Fatal("expected an error when the csrf-token meta tag is absent")
	}
}

// --- activity row parsing -------------------------------------------------

func elem(tag string, attrs ...html.Attribute) *html.Node {
	return &html.Node{Type: html.ElementNode, Data: tag, Attr: attrs}
}

func textNode(s string) *html.Node {
	return &html.Node{Type: html.TextNode, Data: s}
}

func withChildren(n *html.Node, kids ...*html.Node) *html.Node {
	for _, k := range kids {
		n.AppendChild(k)
	}
	return n
}

// buildRow mimics how the activities table parses: real cells live at the odd
// child indices because whitespace text nodes occupy the even ones. overrides
// places a node at a specific child index; everything else is whitespace.
func buildRow(size int, overrides map[int]*html.Node) *html.Node {
	row := elem("tr")
	for i := 0; i < size; i++ {
		if n, ok := overrides[i]; ok {
			row.AppendChild(n)
		} else {
			row.AppendChild(textNode("\n"))
		}
	}
	return row
}

func mediaCell(href string) *html.Node {
	// matches nthChild(row,17).FirstChild.NextSibling == <a href=...>
	return withChildren(elem("td"), textNode("\n"), elem("a", html.Attribute{Key: "href", Val: href}))
}

// A normal row: title cell has an element whose text child is the title.
func TestParseActivityRowNormal(t *testing.T) {
	row := buildRow(18, map[int]*html.Node{
		1:  withChildren(elem("td"), textNode("Ms. Smith")),
		3:  withChildren(elem("td"), textNode("06/24/26")),
		5:  withChildren(elem("td"), textNode("\n"), withChildren(elem("span"), textNode("Look what I'm doing today"))),
		17: mediaCell("https://s3.example.com/photo.jpg"),
	})

	act, ok := parseActivityRow(row)
	if !ok {
		t.Fatal("expected row with a media URL to parse")
	}
	if act.MediaURL != "https://s3.example.com/photo.jpg" {
		t.Errorf("MediaURL = %q", act.MediaURL)
	}
	if act.Title != "Look what I'm doing today" {
		t.Errorf("Title = %q", act.Title)
	}
	if act.AddedBy != "Ms. Smith" {
		t.Errorf("AddedBy = %q", act.AddedBy)
	}
}

// The regression: a row whose title-cell element has no text child used to
// panic on n.FirstChild.Data (client.go:112) and abort the whole download.
// It must now parse, keep the media URL, and fall back to an empty title.
func TestParseActivityRowMalformedTitleDoesNotPanic(t *testing.T) {
	row := buildRow(18, map[int]*html.Node{
		1:  withChildren(elem("td"), textNode("Ms. Smith")),
		3:  withChildren(elem("td"), textNode("06/24/26")),
		5:  withChildren(elem("td"), textNode("\n"), elem("span")), // span has NO child
		17: mediaCell("https://s3.example.com/video.mov"),
	})

	act, ok := parseActivityRow(row)
	if !ok {
		t.Fatal("expected row with a media URL to parse despite malformed title")
	}
	if act.MediaURL != "https://s3.example.com/video.mov" {
		t.Errorf("MediaURL = %q", act.MediaURL)
	}
	if act.Title != "" {
		t.Errorf("expected empty title fallback, got %q", act.Title)
	}
}

// A header/short row with no media link must be skipped, not panic.
func TestParseActivityRowNoMediaSkipped(t *testing.T) {
	row := buildRow(3, map[int]*html.Node{
		1: withChildren(elem("th"), textNode("Added by")),
	})
	if _, ok := parseActivityRow(row); ok {
		t.Fatal("expected row without a media URL to be skipped")
	}
}
