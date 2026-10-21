package main

import "testing"

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
