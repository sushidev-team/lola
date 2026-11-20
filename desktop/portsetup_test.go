package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPortSetupReviewAndApply(t *testing.T) {
	root := projectGitRepo(t)
	writeTestConfig(t, minimalConfig+fmt.Sprintf("\n[defaults.env]\nSHARED='keep me'\n\n[[project]]\nname='demo'\npath=%q\ndev_commands=['npm run dev','keep existing']\n", root))
	const file = "vite.config.ts"
	const before = "    port: 5173,"
	writeFile := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		return string(out)
	}
	writeFile(file, "  server: {\r\n"+before+"\r\n  },\r\n")
	writeFile(".env", "APP_URL=http://localhost:5173\nAPI_TOKEN=supersecret\n")
	git("add", file)
	git("commit", "-m", "dev setup")
	svc := ProjectGitService{draftCommit: func(_ context.Context, _, input string) (string, error) {
		if !strings.Contains(input, before) || !strings.Contains(input, "APP_URL=http://localhost:5173") || strings.Contains(input, "supersecret") || strings.Contains(input, "keep me") {
			t.Fatalf("unexpected AI context %s", input)
		}
		return `{"note":"Vite serves the frontend.","changes":[{"kind":"ports","after":"2"},{"kind":"command","line":1,"after":"npm run dev -- --port $LOLA_PORT_2"},{"kind":"env","target":"APP_URL","after":"http://localhost:$LOLA_PORT_2"},{"kind":"file","target":"vite.config.ts","line":2,"after":"    port: Number(process.env.LOLA_PORT_2 || 5173),"}]}`, nil
	}}
	plan, err := svc.SuggestPortSetup("demo")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Changes[2].After != "http://localhost:$LOLA_PORT_2" || plan.Changes[3].Before != before || plan.Changes[1].Before != "npm run dev" {
		t.Fatal(plan)
	}
	// Invalid subsets fail before any files or settings change.
	if err := svc.ApplyPortSetup("demo", plan.Token, plan.Changes[1:]); err == nil {
		t.Fatal("missing port allocation accepted")
	}
	if got := git("status", "--porcelain", "--untracked-files=no"); got != "" {
		t.Fatalf("planning or invalid selection wrote a file: %s", got)
	}
	writeFile(file, "  server: {\r\n    port: 3000,\r\n  },\r\n")
	if err := svc.ApplyPortSetup("demo", plan.Token, plan.Changes); err == nil {
		t.Fatal("stale evidence accepted")
	}
	writeFile(file, "  server: {\r\n"+before+"\r\n  },\r\n")
	git("switch", "-c", "other-branch")
	if err := svc.ApplyPortSetup("demo", plan.Token, plan.Changes); err == nil {
		t.Fatal("applied plan to a different branch")
	}
	git("switch", "main")
	// A human edits the count and declines the command replacement.
	chosen := slices.Delete(slices.Clone(plan.Changes), 1, 2)
	chosen[0].After = "3"
	if err := svc.ApplyPortSetup("demo", plan.Token, chosen); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.ProjectByName("demo")
	if p.PortRange != "auto" || p.PortsPerSession != 3 || p.Inherits.Env || p.Env["SHARED"] != "keep me" || p.Env["APP_URL"] != "http://localhost:{{.Port2}}" || !slices.Equal(p.DevCommands, []string{"npm run dev", "keep existing"}) {
		t.Fatal(p)
	}
	raw, err := os.ReadFile(filepath.Join(root, file))
	if err != nil || string(raw) != "  server: {\r\n"+chosen[2].After+"\r\n  },\r\n" {
		t.Fatalf("file=%q err=%v", raw, err)
	}
	if staged := git("diff", "--cached", "--name-only"); staged != "" {
		t.Fatal("unexpected staged file", staged)
	}
	if err := svc.ApplyPortSetup("demo", plan.Token, chosen); err == nil {
		t.Fatal("stale retry accepted")
	}
}

func TestParsePortSetupPlan(t *testing.T) {
	const plan = `{"note":"Use Lola’s session port.","changes":[{"kind":"ports","after":"1"},{"kind":"env","target":"WAILS_VITE_PORT","after":"{{.Port}}"}]}`
	for _, response := range []string{plan, "```json\n" + plan + "\n```", "\r\n```\r\n" + plan + "\r\n```\r\n"} {
		got, err := parsePortSetupPlan(response)
		if err != nil || len(got.Changes) != 2 || got.Changes[1].After != "{{.Port}}" {
			t.Fatalf("plan = %+v, err = %v", got, err)
		}
	}
	for _, response := range []string{"```json\n" + plan, "```json\n{}\n```", plan + plan} {
		if _, err := parsePortSetupPlan(response); err == nil {
			t.Fatal("accepted a partial, missing, or ambiguous plan")
		}
	}
}
