package main

import (
	"github.com/sushidev-team/lola/internal/knowledge"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLearningCodeTargets(t *testing.T) {
	targets, err := learningCodeTargets([]string{"api:internal/storage", "web:."})
	if err != nil || len(targets) != 2 || targets[0].Member != "api" || targets[1].Path != "." {
		t.Fatalf("targets: %+v %v", targets, err)
	}
	for _, values := range [][]string{{"api"}, {"api:"}, {":src"}, {"api:../escape"}, {"api:/absolute"}, {"api:src", "api:src"}} {
		if _, err := learningCodeTargets(values); err == nil {
			t.Fatalf("accepted %v", values)
		}
	}
}

func TestImprovementCommandValidation(t *testing.T) {
	for _, args := range [][]string{
		{"improvements"},
		{"lesson", "id", "--project", "p"},
		{"decide-improvement", "id", "--project", "p"},
		{"export-improvement", "id", "--project", "p", "--revision", "rev"},
	} {
		c := learnCmd()
		c.SilenceErrors = true
		c.SilenceUsage = true
		c.SetArgs(args)
		if err := c.Execute(); err == nil {
			t.Fatalf("missing required input accepted: %v", args)
		}
	}
	t.Setenv("LOLA_SESSION", "worker")
	c := learnCmd()
	c.SilenceErrors = true
	c.SilenceUsage = true
	c.SetArgs([]string{"decide-improvement", "id", "--project", "p", "--revision", "rev", "--decision", "tracked", "--evidence", "ENG-42 records the implementation"})
	if err := c.Execute(); err == nil || !strings.Contains(err.Error(), "operator") {
		t.Fatalf("worker curates proposals: %v", err)
	}
}

func TestImprovementExportIsReviewableAndDoesNotOverwrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "proposal")
	files := knowledge.ExportImprovement(knowledge.Improvement{ID: "proposal", Topic: "Recovery", Problem: "Persist before dispatch.", State: "proposed", Scope: knowledge.Scope{Project: "p"}})
	if err := writeLearningExport(dir, files); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "IMPROVEMENT.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "proposal.json")); err != nil {
		t.Fatal(err)
	}
	if err := writeLearningExport(dir, files); err == nil {
		t.Fatal("overwrote a reviewed proposal")
	}
}
