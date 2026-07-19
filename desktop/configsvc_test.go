package main

import (
	"reflect"
	"testing"
)

func TestEnvRoundTrip(t *testing.T) {
	lines := []string{"A=1", "B=two=with=eq", "C="}
	m, err := linesToEnv(lines)
	if err != nil {
		t.Fatalf("linesToEnv: %v", err)
	}
	if m["A"] != "1" || m["B"] != "two=with=eq" || m["C"] != "" {
		t.Fatalf("env map = %+v", m)
	}
	// envToLines is sorted and stable.
	got := envToLines(m)
	want := []string{"A=1", "B=two=with=eq", "C="}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("envToLines = %v, want %v", got, want)
	}
}

func TestLinesToEnvRejectsBadLine(t *testing.T) {
	if _, err := linesToEnv([]string{"NOEQUALS"}); err == nil {
		t.Fatal("want error for a line without '='")
	}
}

func TestLinesToEnvSkipsBlank(t *testing.T) {
	m, err := linesToEnv([]string{"  ", "", "K=v"})
	if err != nil {
		t.Fatalf("linesToEnv: %v", err)
	}
	if len(m) != 1 || m["K"] != "v" {
		t.Fatalf("map = %+v", m)
	}
}

func TestEnvToLinesEmpty(t *testing.T) {
	if got := envToLines(nil); got != nil {
		t.Fatalf("want nil for empty map, got %v", got)
	}
}

func TestNonEmpty(t *testing.T) {
	got := nonEmpty([]string{"a", "", "  ", "b"})
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("nonEmpty = %v", got)
	}
}

func TestGetProjectNewIsBlank(t *testing.T) {
	s := &ConfigService{}
	dto, err := s.GetProject("")
	if err != nil {
		t.Fatalf("GetProject(\"\"): %v", err)
	}
	if !dto.IsNew {
		t.Fatal("expected IsNew for empty name")
	}
}
