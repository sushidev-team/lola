package reviewprofile

import (
	"path/filepath"
	"testing"
)

func fixtures() (Profile, Inputs) {
	return Profile{ID: "backend", Kind: "backend", Provider: "codex-session", Version: "1", Enabled: true},
		Inputs{Repo: "owner/repo", Head: "head", Base: "base", Contract: "Concurrent saves do not lose writes", Paths: []string{"internal/store.go"}}
}

const clean = `{"complete":true,"summary":"Checked the save contract","findings":[],"evidence":[{"criterion":"Concurrent saves preserve writes","observation":"Save uses a single mutex around copy and rename"}]}`

func TestProfileKeysSeparateSameProviderAndExactInputs(t *testing.T) {
	p, in := fixtures()
	key := p.Key(in)
	for _, change := range []string{"id", "version", "rubric", "head", "base", "contract", "artifact", "knowledge"} {
		p2, in2 := p, in
		switch change {
		case "id":
			p2.ID = "security"
		case "version":
			p2.Version = "2"
		case "rubric":
			p2.Rubric = "Check retries"
		case "head":
			in2.Head = "other-head"
		case "base":
			in2.Base = "other-base"
		case "contract":
			in2.Contract = "different acceptance"
		case "artifact":
			in2.Artifacts = []Artifact{{ID: "a", SHA256: "different"}}
		case "knowledge":
			in2.Knowledge = "new lesson"
		}
		if p2.Key(in2) == key {
			t.Fatalf("%s did not invalidate verdict", change)
		}
	}
}

func TestSpecialistIncompleteNeverClean(t *testing.T) {
	p, in := fixtures()
	for _, bad := range []string{"", "looks good", `{}`, `{"complete":true}`, `{"complete":false,"findings":[]}`, clean + " trailing", `{"complete":true,"findings":[{"severity":"major"}],"evidence":[{"criterion":"c","observation":"o"}]}`} {
		if _, err := Parse(bad, in, p); err == nil {
			t.Fatalf("accepted incomplete response %q", bad)
		}
	}
	if _, err := Parse(clean, in, p); err != nil {
		t.Fatal(err)
	}
	p.Kind = "uiux"
	if err := p.CheckInputs(in); err == nil {
		t.Fatal("UI review accepted without runtime artifacts")
	}
	in.Artifacts = []Artifact{{ID: "screen", Kind: "screenshot", Path: "screen.png", SHA256: "hash"}, {ID: "flow", Kind: "interaction", Path: "keyboard.txt", SHA256: "hash2"}}
	if err := p.CheckInputs(in); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(clean, in, p); err == nil {
		t.Fatal("UI reviewer omitted the required artifact observations")
	}
}

func TestProfileStoreCrashAndAttemptCaps(t *testing.T) {
	p, in := fixtures()
	s, err := Open(filepath.Join(t.TempDir(), "profiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Begin(p, in, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Begin(p, in, true); err == nil {
		t.Fatal("concurrent duplicate attempt")
	}
	s, err = Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	old, _ := s.Get(r.Key)
	if old.State != "error" {
		t.Fatal("interrupted review looked complete")
	}
	for attempt := 2; attempt <= 3; attempt++ {
		r, err = s.Begin(p, in, true)
		if err != nil || r.Attempt != attempt {
			t.Fatalf("retry: %+v %v", r, err)
		}
		r.State = "error"
		if err := s.Finish(r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Begin(p, in, true); err == nil {
		t.Fatal("attempt budget bypassed")
	}
}
