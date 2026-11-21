package reviewpack

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) (string, string, Manifest) {
	t.Helper()
	home, work := t.TempDir(), t.TempDir()
	var b bytes.Buffer
	im := image.NewRGBA(image.Rect(0, 0, 3, 2))
	im.Set(0, 0, color.White)
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "after.png"), b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return home, work, Manifest{Version: 1, Title: "Settings", Body: "## Change\nSpacing corrected", Base: strings.Repeat("a", 40), Head: strings.Repeat("b", 40), Screens: []Screen{{Title: "Settings", After: "after.png"}}}
}

func TestRetainedImmutableRetryAndIsolation(t *testing.T) {
	home, work, m := fixture(t)
	p, err := Publish(home, "s", work, "acme/app", 42, m)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := Publish(home, "s", work, "acme/app", 42, m)
	if err != nil || retry.ID != p.ID || !retry.CreatedAt.Equal(p.CreatedAt) {
		t.Fatalf("retry: %+v %v", retry, err)
	}
	m.Body = "New write-up"
	newer, err := Publish(home, "s", work, "acme/app", 42, m)
	if err != nil || newer.ID == p.ID {
		t.Fatalf("new package: %v", err)
	}
	if err := os.Remove(filepath.Join(work, "after.png")); err != nil {
		t.Fatal(err)
	}
	list, err := List(home, "s")
	if err != nil || len(list) != 2 || list[0].ID != newer.ID {
		t.Fatalf("history: %+v %v", list, err)
	}
	data, mime, err := Image(home, "s", p.ID, p.Screens[0].After)
	if err != nil || len(data) == 0 || mime != "image/png" {
		t.Fatalf("retained image: %s %v", mime, err)
	}
	if _, err := Read(home, "other", p.ID); err == nil {
		t.Fatal("cross-session read")
	}
	if _, _, err := Image(home, "s", p.ID, "../../outside"); err == nil {
		t.Fatal("arbitrary path read")
	}
	if err := os.WriteFile(filepath.Join(sessionDir(home, "s"), p.ID, p.Screens[0].After), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Image(home, "s", p.ID, p.Screens[0].After); err == nil {
		t.Fatal("tampered image accepted")
	}
}

func TestRejectUnsafeOrInvalidImagesWithoutPublication(t *testing.T) {
	for _, kind := range []string{"escape", "symlink", "directory", "text", "truncated", "oversized", "bad-commit", "missing"} {
		t.Run(kind, func(t *testing.T) {
			home, work, m := fixture(t)
			switch kind {
			case "escape":
				m.Screens[0].After = "../after.png"
			case "symlink":
				if err := os.Symlink("after.png", filepath.Join(work, "link.png")); err != nil {
					t.Fatal(err)
				}
				m.Screens[0].After = "link.png"
			case "directory":
				m.Screens[0].After = "."
			case "text":
				os.WriteFile(filepath.Join(work, "after.png"), []byte("<svg/>"), 0600)
			case "truncated":
				b, _ := os.ReadFile(filepath.Join(work, "after.png"))
				os.WriteFile(filepath.Join(work, "after.png"), b[:len(b)/2], 0600)
			case "oversized":
				os.WriteFile(filepath.Join(work, "after.png"), make([]byte, MaxImage+1), 0600)
			case "bad-commit":
				m.Head = "main"
			case "missing":
				m.Screens[0].Before = "missing.png"
			}
			if _, err := Publish(home, "s", work, "acme/app", 1, m); err == nil {
				t.Fatal("accepted invalid material")
			}
			list, err := List(home, "s")
			if err != nil || len(list) != 0 {
				t.Fatalf("partial publication: %+v %v", list, err)
			}
		})
	}
}

func TestPackageLimitAndManifestContract(t *testing.T) {
	home, work, m := fixture(t)
	m.Screens = nil
	for i := 0; i < MaxPackages; i++ {
		m.Title = strings.Repeat("x", i+1)
		if _, err := Publish(home, "s", work, "r", 1, m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Publish(home, "s", work, "r", 1, m); err != nil {
		t.Fatal("retry at limit", err)
	}
	m.Title = "one too many"
	if _, err := Publish(home, "s", work, "r", 1, m); err == nil {
		t.Fatal("unbounded history")
	}
	for _, raw := range []string{`{}`, `{"version":1,"extra":true}`, strings.Repeat(" ", MaxManifest+1)} {
		if _, err := Decode([]byte(raw)); err == nil {
			t.Fatal("accepted invalid manifest")
		}
	}
}

func TestStackHistoryRemainsDiscoverableAfterTeardown(t *testing.T) {
	home, work, m := fixture(t)
	web, err := Publish(home, "s~web", work, "acme/web", 1, m)
	if err != nil {
		t.Fatal(err)
	}
	api, err := Publish(home, "s~api", work, "acme/api", 2, m)
	if err != nil {
		t.Fatal(err)
	}
	all, err := List(home, "s")
	if err != nil || len(all) != 2 {
		t.Fatalf("root history: %+v %v", all, err)
	}
	member, err := List(home, "s~web")
	if err != nil || len(member) != 1 || member[0].ID != web.ID {
		t.Fatalf("member history: %+v %v", member, err)
	}
	if _, err := Read(home, "s~web", api.ID); err == nil {
		t.Fatal("member read another member")
	}
	if _, _, err := Image(home, "s", web.ID, web.Screens[0].After); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir(home, "s"), web.ID, "package.json"), []byte(`{"id":"`+web.ID+`","session":"s~web","body":"tampered"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(home, "s", web.ID); err == nil {
		t.Fatal("tampered metadata accepted")
	}
}

func TestLivePagesAreBoundedHTTPAddresses(t *testing.T) {
	_, _, m := fixture(t)
	m.Pages = []Page{{Title: "Settings", URL: "http://localhost:3000/settings", Caption: "spacing"}}
	if err := Validate(m); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"file:///etc/passwd", "javascript:alert(1)", "http://u:p@localhost:3000/", "http:///x", "localhost:3000"} {
		m.Pages = []Page{{Title: "x", URL: bad}}
		if Validate(m) == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	m.Pages = make([]Page, MaxPages+1)
	for i := range m.Pages {
		m.Pages[i] = Page{Title: "p", URL: "http://localhost:3000/"}
	}
	if Validate(m) == nil {
		t.Error("accepted too many pages")
	}
}

func TestPreparationIsBoundedAndTravelsOnPagesAndScreens(t *testing.T) {
	_, _, m := fixture(t)
	ok := Prep{Device: "tablet", WaitFor: "main", ScrollY: 400, Marks: []Mark{{Selector: "#a", Label: "A"}, {Text: "Save"}}}
	m.Pages = []Page{{Title: "p", URL: "http://localhost:3000/", Prep: ok}}
	m.Screens[0].Prep = ok
	if err := Validate(m); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(m)
	back, err := Decode(raw)
	if err != nil || back.Pages[0].Marks[1].Text != "Save" || back.Screens[0].Device != "tablet" {
		t.Fatalf("round trip lost the preparation: %+v, %v", back, err)
	}
	for _, bad := range []Prep{
		{Device: "watch"}, {ScrollY: -1}, {ScrollY: 100_001}, {WaitFor: strings.Repeat("x", 501)},
		{Marks: make([]Mark, MaxMarks+1)}, {Marks: []Mark{{Label: "no target"}}}, {Marks: []Mark{{Text: "x", Label: strings.Repeat("l", 81)}}},
	} {
		if len(bad.Marks) > MaxMarks {
			for i := range bad.Marks {
				bad.Marks[i] = Mark{Selector: "a"}
			}
		}
		m.Pages = []Page{{Title: "p", URL: "http://localhost:3000/", Prep: bad}}
		if Validate(m) == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

// LocalHostCases is mirrored in desktop/frontend/src/lib/prepare.test.ts: the
// Go rule and the script's rule must agree.
var localHostCases = map[string]bool{
	"localhost": true, "app.localhost": true, "shop.test": true, "SHOP.TEST": true, "127.0.0.1": true, "[::1]": true,
	"10.1.2.3": true, "172.16.0.9": true, "172.31.255.1": true, "192.168.1.20": true, "169.254.1.1": true, "fd12::1": true,
	"example.com": false, "shop.test.evil.com": false, "evil-test": false, "8.8.8.8": false, "172.32.0.1": false,
	"192.169.1.1": false, "11.0.0.1": false, "localhost.evil.com": false, "": false, "intranet": false, "app.local": false,
}

func TestLocalHost(t *testing.T) {
	for host, want := range localHostCases {
		if got := LocalHost(host); got != want {
			t.Errorf("LocalHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestStepsAreBoundedAndOnlyOnLocalHosts(t *testing.T) {
	_, _, m := fixture(t)
	login := []Step{
		{Do: StepFill, Selector: "#email", Value: "dev@shop.test"}, {Do: StepFill, Text: "Password", Value: "dev-only"},
		{Do: StepClick, Text: "Sign in"}, {Do: StepWait, Selector: ".dashboard"}, {Do: StepPress, Key: "Enter"}, {Do: StepGoto, Path: "/cart?x=1"}, {Do: StepWait, Ms: 500},
	}
	m.Pages = []Page{{Title: "Cart", URL: "https://shop.test/cart", Prep: Prep{Steps: login}}}
	if err := Validate(m); err != nil {
		t.Fatal(err)
	}
	for _, url := range []string{"https://example.com/cart", "http://8.8.8.8/", "https://shop.test.evil.com/", ""} {
		m.Pages[0].URL = url
		if err := Validate(m); err == nil {
			t.Errorf("steps accepted for %q", url)
		}
	}
	// The same rule holds for a screenshot entry.
	m.Pages = nil
	m.Screens[0].URL, m.Screens[0].Prep = "https://example.com/", Prep{Steps: login}
	if Validate(m) == nil {
		t.Error("steps accepted on a screen of a public host")
	}
	m.Screens[0].URL = "http://192.168.1.5:3000/"
	if err := Validate(m); err != nil {
		t.Errorf("private address refused: %v", err)
	}
	for _, bad := range []Step{
		{Do: "eval", Text: "alert(1)"}, {Do: StepClick}, {Do: StepFill, Value: "x"}, {Do: StepPress, Key: "F12"}, {Do: StepWait},
		{Do: StepWait, Ms: 3001}, {Do: StepGoto, Path: "//evil.com/"}, {Do: StepGoto, Path: "https://evil.com/"}, {Do: StepGoto, Path: "/a\\b"},
		{Do: StepFill, Selector: "#p", Value: strings.Repeat("x", 201)},
	} {
		m.Screens[0].Prep = Prep{Steps: []Step{bad}}
		if Validate(m) == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	m.Screens[0].Prep = Prep{Steps: make([]Step, MaxSteps+1)}
	for i := range m.Screens[0].Steps {
		m.Screens[0].Steps[i] = Step{Do: StepWait, Ms: 1}
	}
	if Validate(m) == nil {
		t.Error("accepted too many steps")
	}
}
