package goal

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/sushidev-team/lola/internal/goalpolicy"
)

// TaskScopes resolves ownership once, against the immutable accepted policy.
// Omitted task boundaries inherit that policy; explicit entries can only narrow
// it, including making an always-included stack repository read-only.
func TaskScopes(policy goalpolicy.Policy, t Task) (map[string]goalpolicy.Scope, error) {
	members, err := taskMembers(policy, t)
	if err != nil {
		return nil, err
	}
	if len(t.Paths) > 0 && len(t.Paths) != len(members) {
		return nil, errors.New("task ownership must cover exactly its routed members")
	}
	out := map[string]goalpolicy.Scope{}
	writable := false
	for _, id := range members {
		var parent goalpolicy.Scope
		for _, m := range policy.Members {
			if m.ID == id {
				parent = goalpolicy.Scope{All: len(m.Paths) == 0, Paths: slices.Clone(m.Paths)}
			}
		}
		scope := parent
		if len(t.Paths) > 0 {
			paths, ok := t.Paths[id]
			if !ok {
				return nil, fmt.Errorf("task ownership omits member %s", id)
			}
			scope = goalpolicy.Scope{Paths: slices.Clone(paths)}
		}
		if err := scope.Validate(); err != nil {
			return nil, err
		}
		if !scope.Within(parent) {
			return nil, fmt.Errorf("task %s ownership exceeds accepted paths for %s", t.Title, id)
		}
		out[id] = scope
		writable = writable || !scope.ReadOnly()
	}
	if !writable {
		return nil, errors.New("worker task needs at least one writable deliverable; use read-only scope for context members only")
	}
	return out, nil
}

func (g Goal) AttemptScopes(a Attempt) (map[string]goalpolicy.Scope, error) {
	if g.Execution == nil {
		return nil, errors.New("attempt has no execution policy")
	}
	var task Task
	for _, t := range g.Tasks {
		if t.ID == a.TaskID {
			task = t
			break
		}
	}
	if task.ID == "" {
		return nil, errors.New("attempt task missing")
	}
	want, err := TaskScopes(g.Execution.Policy, task)
	if err != nil {
		return nil, err
	}
	if len(a.Scopes) == 0 {
		return want, nil
	} // pre-ownership snapshots inherit the accepted task/policy
	if !maps.EqualFunc(want, a.Scopes, func(x, y goalpolicy.Scope) bool { return x.All == y.All && slices.Equal(x.Paths, y.Paths) }) {
		return nil, errors.New("attempt ownership differs from its accepted task")
	}
	return want, nil
}

func scopesOverlap(a, b map[string]goalpolicy.Scope) bool {
	for id, x := range a {
		if y, ok := b[id]; ok && x.Overlaps(y) {
			return true
		}
	}
	return false
}
