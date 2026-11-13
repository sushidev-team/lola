package config

import "testing"

func TestAutoPRCommentsInheritanceRoundTrip(t *testing.T) {
	c, path := writeCfg(t, `[defaults]
auto_pr_comments = true
[[project]]
name = "inherit"
[[project]]
name = "off"
auto_pr_comments = false
[[project]]
name = "on"
auto_pr_comments = true
`)
	for i := 0; i < 2; i++ {
		if !c.Projects[0].AutoPRComments || !c.Projects[0].Inherits.AutoPRComments || c.Projects[1].AutoPRComments || c.Projects[1].Inherits.AutoPRComments || !c.Projects[2].AutoPRComments || c.Projects[2].Inherits.AutoPRComments {
			t.Fatalf("lost inheritance or explicit false: %+v", c.Projects)
		}
		if err := c.Save(path); err != nil {
			t.Fatal(err)
		}
		var err error
		c, err = Load(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	c.Defaults.AutoPRComments = false
	c.ResolveInheritance()
	if c.Projects[0].AutoPRComments || c.Projects[1].AutoPRComments || !c.Projects[2].AutoPRComments {
		t.Fatal("changing the default did not preserve overrides")
	}
	c, _ = writeCfg(t, `[[project]]
name = "old"
`)
	if c.Defaults.AutoPRComments || c.Projects[0].AutoPRComments {
		t.Fatal("old configs must stay off")
	}
}
