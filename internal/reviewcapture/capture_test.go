package reviewcapture

import (
	"context"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestCaptureRetainsActualContextWithoutOverwriting(t *testing.T) {
	dir := t.TempDir()
	o := Options{URL: "http://localhost:3000/settings", Selector: "#settings", Width: 1440, Height: 900, Destination: ".lola/review/settings.png", Title: "Settings"}
	run := func(_ context.Context, workspace, script string, input []byte) ([]byte, error) {
		if workspace != dir || script == "" {
			t.Fatal("missing capture context")
		}
		var got Options
		json.Unmarshal(input, &got)
		if got.Selector != o.Selector || got.Width != 1440 || got.URL != o.URL {
			t.Fatalf("%+v", got)
		}
		f, err := os.Create(got.Destination)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		err = png.Encode(f, image.NewRGBA(image.Rect(0, 0, 10, 10)))
		return []byte(`{"url":"http://localhost:3000/settings?ready=1"}`), err
	}
	s, err := Capture(context.Background(), dir, o, run)
	if err != nil {
		t.Fatal(err)
	}
	if s.Selector != o.Selector || s.Viewport != "1440 × 900" || s.After != o.Destination || s.URL == o.URL {
		t.Fatalf("%+v", s)
	}
	if _, err := Capture(context.Background(), dir, o, run); err == nil {
		t.Fatal("overwrote existing evidence")
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "outside")); err != nil {
		t.Fatal(err)
	}
	o.Destination = "outside/screenshot.png"
	if _, err := Capture(context.Background(), dir, o, run); err == nil {
		t.Fatal("escaped capture root")
	}
}

func TestCaptureRejectsUnsafeInputBeforeLaunch(t *testing.T) {
	base := Options{URL: "https://example.test", Width: 1440, Height: 900, Destination: "capture.png", Title: "Page"}
	for _, change := range []func(*Options){func(o *Options) { o.URL = "file:///etc/passwd" }, func(o *Options) { o.URL = "https://user:password@example.test" }, func(o *Options) { o.Destination = "../capture.png" }, func(o *Options) { o.Width = 100000 }} {
		o := base
		change(&o)
		if _, err := Capture(context.Background(), t.TempDir(), o, func(context.Context, string, string, []byte) ([]byte, error) {
			t.Fatal("launched for invalid input")
			return nil, nil
		}); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
}
