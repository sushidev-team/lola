package preview

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestStatamicNavigationAfterInertiaVisit(t *testing.T) {
	const pages = "/admin/collections/pages"
	const edit = pages + "/entries/home/edit?site=de#content"
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := "http://" + r.Host
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		switch r.URL.Path {
		case pages + "/tree":
			fmt.Fprintf(w, `{"pages":[{"children":[{"edit_url":%q,"title":%q}]}]}`, origin+edit, origin+edit)
		case pages + "/entries":
			fmt.Fprintf(w, `{"data":[{"edit_url":%q,"custom_url":%q}]}`, origin+edit, origin+edit)
		default:
			if r.Header.Get("X-Inertia") != "true" {
				http.Error(w, "expected an Inertia visit", http.StatusBadRequest)
				return
			}
			w.Header().Set("X-Inertia", "true")
			switch r.URL.Path {
			case "/admin/collections":
				fmt.Fprintf(w, `{"component":"collections/Index","props":{"_statamic":{},"collections":[{"entries_url":%q}]}}`, origin+pages)
			case pages:
				fmt.Fprintf(w, `{"component":"collections/Show","sharedProps":["_statamic"],"props":{"structurePagesUrl":%q,"createUrls":{"de":%q}}}`, origin+pages+"/tree", origin+pages+"/entries/create/de")
			case pages + "/entries/home/edit":
				fmt.Fprintf(w, `{"component":"entries/Edit","props":{"_statamic":{},"actions":{"save":%q},"values":{"edit_url":%q},"externalUrl":"https://example.com/","id":9007199254740993}}`, origin+pages+"/save", origin+edit)
			default:
				http.NotFound(w, r)
			}
		}
	}))
	defer up.Close()
	s := New()
	defer s.Close()
	initial, err := s.Open("session", up.URL+"/admin/collections")
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimSuffix(initial, "/admin/collections")
	read := func(target string, inertia bool, out any) {
		t.Helper()
		if !strings.HasPrefix(target, base+"/") {
			t.Fatalf("navigation left preview: %s", target)
		}
		resp := get(t, target, func(req *http.Request) {
			if inertia {
				req.Header.Set("X-Inertia", "true")
			}
		})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s returned %v", target, resp.Status)
		}
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
	var index struct {
		Props struct{ Collections []map[string]string }
	}
	read(initial, true, &index)
	var show struct {
		Props struct {
			StructurePagesURL string
			CreateURLs        map[string]string
		}
	}
	read(index.Props.Collections[0]["entries_url"], true, &show)
	if got := show.Props.CreateURLs["de"]; got != base+pages+"/entries/create/de" {
		t.Fatalf("create URL = %s", got)
	}
	var tree struct {
		Pages []struct{ Children []map[string]string }
	}
	read(show.Props.StructurePagesURL, false, &tree)
	child := tree.Pages[0].Children[0]
	if child["edit_url"] != base+edit || child["title"] != up.URL+edit {
		t.Fatalf("tree row = %v", child)
	}
	var editor struct {
		Props struct {
			Actions, Values map[string]string
			ExternalURL     string
			ID              int64
		}
	}
	read(child["edit_url"], true, &editor)
	if p := editor.Props; p.Actions["save"] != base+pages+"/save" || p.Values["edit_url"] != up.URL+edit || p.ExternalURL != "https://example.com/" || p.ID != 9007199254740993 {
		t.Fatalf("editor props = %+v", p)
	}
	var list struct{ Data []map[string]string }
	read(base+pages+"/entries", false, &list)
	if row := list.Data[0]; row["edit_url"] != base+edit || row["custom_url"] != up.URL+edit {
		t.Fatalf("entry row = %v", row)
	}
}

func TestStatamicInitialPageUsesSameNavigationURLs(t *testing.T) {
	origin, _ := url.Parse("https://site.test")
	self, _ := url.Parse("http://p-site.localhost:4567")
	page := `{"props":{"_statamic":{},"createUrls":{"de":"https:\/\/site.test:443\/cp?site=de&new=1"},"values":{"url":"https://site.test/editorial"}}}`
	document := `<head><script src="https://site.test/cp.js"></script></head><div data-page="` + html.EscapeString(page) + `"></div>`
	got := string(htmlPageRewriter(origin, self)([]byte(document)))
	if !strings.Contains(got, self.String()+"/cp.js") || !strings.Contains(got, ScriptPath) {
		t.Fatalf("missing preview scripts: %s", got)
	}
	parts := inertiaMount.FindStringSubmatch(got)
	if parts == nil {
		t.Fatal("lost the Inertia mount")
	}
	var data struct {
		Props struct{ CreateUrls, Values map[string]string }
	}
	if err := json.Unmarshal([]byte(html.UnescapeString(parts[1])), &data); err != nil {
		t.Fatal(err)
	}
	if data.Props.CreateUrls["de"] != self.String()+"/cp?site=de&new=1" || data.Props.Values["url"] != "https://site.test/editorial" {
		t.Fatalf("bootstrap props = %+v", data.Props)
	}
}
