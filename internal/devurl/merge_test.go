package devurl

import (
	"slices"
	"testing"
)

func TestMergeRanksAcrossTabsNotByTabOrder(t *testing.T) {
	website := "  ➜  Local:   http://localhost:5175/\n"
	app := "[vite]   ➜  Local:   http://localhost:5190/\n" +
		"[vite]   ➜  APP_URL: http://localhost:8002\n" +
		"[app]    INFO  Server running on [http://127.0.0.1:8002].\n"
	got := Merge([]string{website, app})
	if len(got) == 0 || got[0] != "http://127.0.0.1:8002" {
		t.Fatalf("the announced server must lead, got %v", got)
	}
	if slices.Contains(got, "http://localhost:8002") {
		t.Errorf("one port listed under two names: %v", got)
	}
	if !slices.Contains(got, "http://localhost:5175") {
		t.Errorf("the other tab's address is lost: %v", got)
	}
}

func TestMergeKeepsPaneOrderOnATie(t *testing.T) {
	got := Merge([]string{"Local: http://localhost:5175\n", "Local: http://localhost:5176\n"})
	if !slices.Equal(got, []string{"http://localhost:5175", "http://localhost:5176"}) {
		t.Errorf("got %v", got)
	}
}
