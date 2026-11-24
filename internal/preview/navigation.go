package preview

import (
	"bytes"
	"encoding/json"
	"html"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// Statamic fetches its tree and entry listing as ordinary JSON between Inertia
// visits. The CP prefix is configurable.
var collectionListingPath = regexp.MustCompile(`/collections/[^/]+/(tree|entries)/?$`)

// Statamic embeds the first Inertia response in a quoted data-page attribute.
var inertiaMount = regexp.MustCompile(`(?i)\sdata-page\s*=\s*(?:"([^"]*)"|'([^']*)')`)

func rewriteStatamicMount(body []byte, origin, self *url.URL, rewriteMarkup func([]byte) []byte) []byte {
	if !bytes.Contains(body, []byte("_statamic")) {
		return rewriteMarkup(body)
	}
	match := inertiaMount.FindSubmatchIndex(body)
	if match == nil {
		return rewriteMarkup(body)
	}
	start, end := match[2], match[3]
	if start < 0 {
		start, end = match[4], match[5]
	}
	page := []byte(html.UnescapeString(string(body[start:end])))
	if !statamicPage(page) {
		return rewriteMarkup(body)
	}
	updated := rewriteNavigationJSON(page, origin, self, inertiaPage)
	attribute := body[start:end]
	if !bytes.Equal(page, updated) {
		attribute = []byte(html.EscapeString(string(updated)))
	}
	return concat(rewriteMarkup(body[:start]), attribute, rewriteMarkup(body[end:]))
}

type navigationFields uint8

const (
	ignoreField navigationFields = iota
	inertiaPage
	collectionListing
	pageProps
	statamicShared
	sidebar
	listingRow
	urlMap
	urlField
)

func (fields navigationFields) child(key string) navigationFields {
	switch fields {
	case inertiaPage:
		if key == "props" {
			return pageProps
		}
		if key == "url" {
			return urlField
		}
	case collectionListing:
		if key == "data" || key == "pages" {
			return listingRow
		}
	case listingRow:
		// Entry rows can also contain arbitrary columns from the blueprint.
		switch key {
		case "edit_url", "create_url":
			return urlField
		case "children", "collection":
			return listingRow
		}
	case pageProps:
		switch {
		case key == "_statamic":
			return statamicShared
		case key == "url", key == "href", strings.HasSuffix(key, "Url"), strings.HasSuffix(key, "_url"):
			return urlField
		case key == "actions", strings.HasSuffix(key, "Urls"):
			return urlMap
		case key == "collections", key == "blueprints", key == "localizations", key == "titleFormat":
			return pageProps
		}
	case statamicShared:
		if key == "nav" {
			return sidebar
		}
	case sidebar:
		// Authenticated pages carry sections, each with items and nested children.
		switch key {
		case "items", "children":
			return sidebar
		case "url":
			return urlField
		}
	case urlMap:
		return urlField
	}
	return ignoreField
}

// The list of shared props identifies partial reloads that omit _statamic.
func statamicPage(body []byte) bool {
	var page struct {
		Props struct {
			Statamic json.RawMessage `json:"_statamic"`
		} `json:"props"`
		SharedProps []string `json:"sharedProps"`
	}
	return json.Unmarshal(body, &page) == nil && (page.Props.Statamic != nil || slices.Contains(page.SharedProps, "_statamic"))
}

// Change only link strings in validated JSON. Splicing their tokens preserves
// the order of site maps and original editor values, including numeric precision.
func rewriteNavigationJSON(body []byte, origin, self *url.URL, fields navigationFields) []byte {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var result bytes.Buffer
	var copied int64
	var walk func(navigationFields)
	walk = func(fields navigationFields) {
		before := decoder.InputOffset()
		token, _ := decoder.Token() // The document has already been validated.
		switch token {
		case json.Delim('{'):
			for decoder.More() {
				key, _ := decoder.Token()
				child := fields.child(key.(string))
				if child == ignoreField {
					var skip json.RawMessage
					_ = decoder.Decode(&skip)
				} else {
					walk(child)
				}
			}
			_, _ = decoder.Token()
		case json.Delim('['):
			for decoder.More() {
				walk(fields)
			}
			_, _ = decoder.Token()
		default:
			raw, ok := token.(string)
			if !ok || fields != urlField {
				return
			}
			u, err := url.Parse(raw)
			if err != nil || u.User != nil || u.Scheme != origin.Scheme || canonicalOrigin(u).Host != origin.Host {
				return
			}
			u.Scheme, u.Host = self.Scheme, self.Host
			end := decoder.InputOffset()
			start := before + int64(bytes.IndexByte(body[before:end], '"'))
			result.Write(body[copied:start])
			quoted, _ := json.Marshal(u.String())
			result.Write(quoted)
			copied = end
		}
	}
	walk(fields)
	if copied == 0 {
		return body
	}
	result.Write(body[copied:])
	return result.Bytes()
}
