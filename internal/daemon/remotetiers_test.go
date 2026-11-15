package daemon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/remote"
)

// protocolStringConsts reads every `Name = "literal"` const of internal/protocol
// from source, so a case label written as protocol.CmdX can be resolved without
// importing a name table the daemon itself does not have.
func protocolStringConsts(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("../protocol/*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no protocol sources found: %v", err)
	}
	out := map[string]string{}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, name := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if v, err := strconv.Unquote(lit.Value); err == nil {
							out[name.Name] = v
						}
					}
				}
			}
		}
	}
	return out
}

// handledCommands returns every command named by a case label of
// (*Daemon).handle's top-level switch in server.go.
func handledCommands(t *testing.T) []string {
	t.Helper()
	consts := protocolStringConsts(t)
	file, err := parser.ParseFile(token.NewFileSet(), "server.go", nil, 0)
	if err != nil {
		t.Fatalf("parse server.go: %v", err)
	}
	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		if f, ok := decl.(*ast.FuncDecl); ok && f.Name.Name == "handle" && f.Recv != nil {
			fn = f
		}
	}
	if fn == nil {
		t.Fatal("(*Daemon).handle not found in server.go")
	}
	var sw *ast.SwitchStmt
	for _, st := range fn.Body.List {
		if s, ok := st.(*ast.SwitchStmt); ok {
			sw = s
		}
	}
	if sw == nil {
		t.Fatal("handle has no top-level switch")
	}
	var cmds []string
	for _, c := range sw.Body.List {
		cc := c.(*ast.CaseClause)
		for _, e := range cc.List {
			switch x := e.(type) {
			case *ast.BasicLit:
				v, err := strconv.Unquote(x.Value)
				if err != nil {
					t.Fatalf("case label %s: %v", x.Value, err)
				}
				cmds = append(cmds, v)
			case *ast.SelectorExpr:
				id, ok := x.X.(*ast.Ident)
				if !ok || id.Name != "protocol" {
					t.Fatalf("case label %v is a selector on something other than protocol", x.Sel.Name)
				}
				v, ok := consts[x.Sel.Name]
				if !ok {
					t.Fatalf("protocol.%s is not a string const the test can resolve", x.Sel.Name)
				}
				cmds = append(cmds, v)
			default:
				t.Fatalf("case label of unexpected form %T", e)
			}
		}
	}
	if len(cmds) < 50 {
		t.Fatalf("found only %d handled commands; the parse is wrong", len(cmds))
	}
	return cmds
}

// TestEveryHandledCommandHasATier is the totality half of the capability
// table. A command the daemon routes must be CLASSIFIED in internal/remote —
// including the ones that are tier never, which are written out — so a case
// added to handle is a decision somebody made and not a command a phone reaches
// by default (an unlisted command is never, which is safe, but a forgotten one
// is indistinguishable from a deliberate one).
func TestEveryHandledCommandHasATier(t *testing.T) {
	tiers := remote.CommandTiers()
	var missing []string
	for _, cmd := range handledCommands(t) {
		if _, ok := tiers[cmd]; !ok {
			missing = append(missing, cmd)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("commands routed by (*Daemon).handle with no entry in remote.CommandTiers: %v", missing)
	}
}

// pushRouted are the commands handle hands to handlePush (server_push.go)
// before its switch, so no case label names them.
var pushRouted = []string{protocol.CmdAttachImage, protocol.CmdAttachmentImage}

// TestTierTableNamesOnlyRealCommands is the other direction: every key of the
// table is a command the daemon routes, a local-only command, a transport
// command internal/remote answers itself, a command handlePush routes, or
// remote.pair. A typo in the table
// would otherwise classify nothing and look like a rule.
func TestTierTableNamesOnlyRealCommands(t *testing.T) {
	handled := map[string]bool{}
	for _, c := range handledCommands(t) {
		handled[c] = true
	}
	var stray []string
	for cmd := range remote.CommandTiers() {
		switch {
		case handled[cmd],
			slices.Contains(protocol.LocalOnlyCommands, cmd),
			slices.Contains(remote.TransportCommands, cmd),
			slices.Contains(pushRouted, cmd),
			cmd == protocol.CmdRemotePair:
		default:
			stray = append(stray, cmd)
		}
	}
	sort.Strings(stray)
	if len(stray) > 0 {
		t.Errorf("commands in remote.CommandTiers that nothing routes: %v", stray)
	}
}
