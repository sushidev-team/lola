package reviewcapture

import (
	"context"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/reviewpack"
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

func pngRun(found string) Run {
	return func(_ context.Context, _, _ string, input []byte) ([]byte, error) {
		var got Options
		_ = json.Unmarshal(input, &got)
		f, err := os.Create(got.Destination)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, 10, 10))); err != nil {
			return nil, err
		}
		return []byte(`{"url":"http://localhost:3000/settings","found":` + found + `}`), nil
	}
}

func TestCaptureCarriesThePreparationAndRefusesAShotThatMissesAMark(t *testing.T) {
	dir := t.TempDir()
	o := Options{URL: "http://localhost:3000/settings", Width: 390, Height: 844, Destination: "a.png", Title: "Settings",
		Prep: reviewpack.Prep{Device: "mobile", WaitFor: "main", Marks: []reviewpack.Mark{{Selector: "#save", Label: "Save"}, {Text: "Danger zone"}}}}
	var sent Options
	run := func(ctx context.Context, w, s string, in []byte) ([]byte, error) {
		_ = json.Unmarshal(in, &sent)
		return pngRun("[true,true]")(ctx, w, s, in)
	}
	s, err := Capture(context.Background(), dir, o, run)
	if err != nil {
		t.Fatal(err)
	}
	if sent.PrepareJS == "" || !strings.Contains(sent.PrepareJS, "__lolaPrepare") || sent.WaitFor != "main" || len(sent.Marks) != 2 {
		t.Fatalf("script input: %+v", sent)
	}
	if s.Device != "mobile" || len(s.Marks) != 2 || s.Marks[0].Label != "Save" {
		t.Fatalf("the screen entry lost the preparation: %+v", s)
	}
	o.Destination = "b.png"
	_, err = Capture(context.Background(), dir, o, pngRun("[true,false]"))
	if err == nil || !strings.Contains(err.Error(), `text "Danger zone"`) {
		t.Fatalf("a shot missing a mark was kept: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "b.png")); statErr == nil {
		t.Fatal("a refused capture left a file behind")
	}
}

func TestCaptureRefusesAShotWhoseWaitForNeverAppeared(t *testing.T) {
	dir := t.TempDir()
	o := Options{URL: "http://localhost:3000/", Width: 390, Height: 844, Destination: "w.png", Title: "Home", Prep: reviewpack.Prep{WaitFor: ".ready"}}
	run := func(ctx context.Context, w, s string, in []byte) ([]byte, error) {
		out, err := pngRun("[]")(ctx, w, s, in)
		return []byte(strings.Replace(string(out), `"found":[]`, `"found":[],"waited":true`, 1)), err
	}
	_, err := Capture(context.Background(), dir, o, run)
	if err == nil || !strings.Contains(err.Error(), ".ready") {
		t.Fatalf("a shot of a page that never rendered was kept: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "w.png")); statErr == nil {
		t.Fatal("a refused capture left a file behind")
	}
}

func TestParseMark(t *testing.T) {
	for _, c := range []struct {
		in   string
		want reviewpack.Mark
	}{
		{"#save", reviewpack.Mark{Selector: "#save"}},
		{"text:Save changes", reviewpack.Mark{Text: "Save changes"}},
		{"The button => a[href|=en]", reviewpack.Mark{Selector: "a[href|=en]", Label: "The button"}},
		{"Where => text:Danger zone", reviewpack.Mark{Text: "Danger zone", Label: "Where"}},
	} {
		got, err := ParseMark(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseMark(%q) = %+v, %v", c.in, got, err)
		}
	}
	for _, bad := range []string{"", "text:", strings.Repeat("x", 501)} {
		if m, err := ParseMark(bad); err == nil {
			t.Errorf("ParseMark(%q) accepted: %+v", bad, m)
		}
	}
}

func TestDeviceSizeMatchesThePreviewPresets(t *testing.T) {
	if w, h, ok := DeviceSize("mobile"); !ok || w != 390 || h != 844 {
		t.Fatal("mobile")
	}
	if _, _, ok := DeviceSize("desktop"); ok {
		t.Fatal("desktop has no fixed size")
	}
}

func TestStepsRunOnlyAgainstLocalAddressesAndKeepTheStartingURL(t *testing.T) {
	steps := []reviewpack.Step{{Do: "fill", Selector: "#e", Value: "dev@shop.test"}, {Do: "click", Text: "Sign in"}}
	o := Options{URL: "https://example.com/login", Width: 1440, Height: 900, Destination: "a.png", Title: "Login", Prep: reviewpack.Prep{Steps: steps}}
	if _, err := Capture(context.Background(), t.TempDir(), o, func(context.Context, string, string, []byte) ([]byte, error) {
		t.Fatal("launched a browser to type into a public host")
		return nil, nil
	}); err == nil {
		t.Fatal("steps accepted for a public host")
	}
	dir := t.TempDir()
	o.URL = "https://shop.test/login"
	var sent Options
	run := func(ctx context.Context, w, s string, in []byte) ([]byte, error) {
		_ = json.Unmarshal(in, &sent)
		f, _ := os.Create(sent.Destination)
		defer f.Close()
		_ = png.Encode(f, image.NewRGBA(image.Rect(0, 0, 10, 10)))
		// The browser ended on the dashboard after signing in.
		return []byte(`{"url":"https://shop.test/dashboard","found":[]}`), nil
	}
	s, err := Capture(context.Background(), dir, o, run)
	if err != nil {
		t.Fatal(err)
	}
	if len(sent.Steps) != 2 || sent.Steps[0].Value != "dev@shop.test" {
		t.Fatalf("steps not passed to the script: %+v", sent.Steps)
	}
	if s.URL != "https://shop.test/login" || len(s.Steps) != 2 {
		t.Fatalf("the entry must name where the steps start: %+v", s)
	}
}

func TestParseStep(t *testing.T) {
	for _, c := range []struct {
		in   string
		want reviewpack.Step
	}{
		{"click text:Sign in", reviewpack.Step{Do: "click", Text: "Sign in"}},
		{"click button[type=submit]", reviewpack.Step{Do: "click", Selector: "button[type=submit]"}},
		{"fill #email = dev@shop.test", reviewpack.Step{Do: "fill", Selector: "#email", Value: "dev@shop.test"}},
		{"fill text:Password = a = b", reviewpack.Step{Do: "fill", Text: "Password", Value: "a = b"}},
		{"press Enter", reviewpack.Step{Do: "press", Key: "Enter"}},
		{"wait .dashboard", reviewpack.Step{Do: "wait", Selector: ".dashboard"}},
		{"wait 500ms", reviewpack.Step{Do: "wait", Ms: 500}},
		{"wait .items", reviewpack.Step{Do: "wait", Selector: ".items"}},
		{"wait #forms", reviewpack.Step{Do: "wait", Selector: "#forms"}},
		{"goto /cart?x=1", reviewpack.Step{Do: "goto", Path: "/cart?x=1"}},
	} {
		got, err := ParseStep(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseStep(%q) = %+v, %v", c.in, got, err)
		}
	}
	for _, bad := range []string{"", "eval 1+1", "click", "fill #e", "press F12", "wait", "wait 9999ms", "goto //evil.com", "goto https://evil.com"} {
		if st, err := ParseStep(bad); err == nil {
			t.Errorf("ParseStep(%q) accepted: %+v", bad, st)
		}
	}
}
