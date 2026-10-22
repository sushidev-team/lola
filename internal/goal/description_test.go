package goal

import "testing"

// Linear's Markdown serializer inserts a blank line before lists, writes '*'
// bullets, and strips the final newline. Emit that form so a fresh creation is
// not mistaken for a human edit. Keep exact comparison of all user content.
func TestDescriptionsMatchLinearSerializedMarkdown(t *testing.T) {
	g := Goal{ID: "goal", Outcome: "Keep exact `content`.", Acceptance: []string{"Works", "Preserves focus"}, NonGoals: []string{"Backend changes"}}
	want := Marker(g.ID, "parent") + "\n\nKeep exact `content`.\n\n## Acceptance\n\n* Works\n* Preserves focus\n\n## Non-goals\n\n* Backend changes\n\nManaged by Lola. Publishing this plan does not authorize execution. Task completion is tracked by verified goal integration; final delivery requires human merge."
	if got := g.ParentDescription(); got != want {
		t.Fatalf("parent Markdown:\n%q\nwant:\n%q", got, want)
	}
	task := Task{ID: "task", TaskDraft: TaskDraft{Description: "Change the dialog.", Acceptance: []string{"Fits"}, Paths: map[string][]string{"app": {"src/dialog.tsx"}}}}
	want = Marker(g.ID, task.ID) + "\n\nChange the dialog.\n\n## Acceptance\n\n* Fits\n\n## Repository ownership\n\n* app: `src/dialog.tsx`"
	if got := g.TaskDescription(task); got != want {
		t.Fatalf("task Markdown:\n%q\nwant:\n%q", got, want)
	}
}

func TestMarkdownCodeProtectsPathPunctuation(t *testing.T) {
	for input, want := range map[string]string{"src/__tests__/a.ts": "`src/__tests__/a.ts`", "src/a`b.ts": "``src/a`b.ts``", "`edge`": "`` `edge` ``"} {
		if got := markdownCode(input); got != want {
			t.Errorf("%q: %q want %q", input, got, want)
		}
	}
}
