package reviewpack

import (
	"bytes"
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
