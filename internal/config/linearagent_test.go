package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func saveReload(t *testing.T, c *Config) (*Config, string) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out.toml")
	if err := c.Save(out); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	c2, err := Load(out)
	if err != nil {
		t.Fatal(err)
	}
	return c2, string(data)
}

func TestLinearAgentAbsentIsDisabledAndNotWritten(t *testing.T) {
	c := loadToml(t, remoteBaseToml)
	if c.LinearAgent != (LinearAgentConfig{}) {
		t.Fatalf("absent table = %+v", c.LinearAgent)
	}
	if _, data := saveReload(t, c); strings.Contains(data, "linear_agent") {
		t.Errorf("Save grew a [linear_agent] table:\n%s", data)
	}
}

func TestLinearAgentResolvesDefaultsAndRoundTrips(t *testing.T) {
	c := loadToml(t, remoteBaseToml+"\n[linear_agent]\nenabled = true\nclient_id = \"cid\"\npoll_interval = \"1s\"\n")
	a := c.LinearAgent
	if !a.Enabled || a.ClientID != "cid" || a.TokenKeychain != DefaultLinearAgentTokenKeychain ||
		a.ClientSecretKeychain != DefaultLinearAgentSecretKeychain || a.RedirectPort != DefaultLinearAgentRedirectPort {
		t.Fatalf("resolved = %+v", a)
	}
	if a.PollInterval != MinLinearAgentPollInterval {
		t.Fatalf("poll interval must clamp to the minimum, got %v", a.PollInterval)
	}
	if a.RedirectURI() != "http://localhost:8790/callback" {
		t.Fatalf("redirect = %s", a.RedirectURI())
	}
	c2, _ := saveReload(t, c)
	if c2.LinearAgent != a {
		t.Fatalf("round trip:\n got %+v\nwant %+v", c2.LinearAgent, a)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("valid table rejected: %v", err)
	}
}

func TestLinearAgentValidation(t *testing.T) {
	cases := map[string]string{
		"client_id":      "[linear_agent]\nenabled = true\n",
		"webhook_listen": "[linear_agent]\nenabled = true\nclient_id = \"c\"\nwebhook_listen = \"localhost:80\"\n",
		"unsigned":       "[linear_agent]\nenabled = true\nclient_id = \"c\"\nwebhook_listen = \"127.0.0.1:8789\"\nwebhook_secret_keychain = \"\"\n",
	}
	for want, body := range cases {
		c := loadToml(t, remoteBaseToml+"\n"+body)
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), "linear_agent") {
			t.Errorf("%s: want a linear_agent error, got %v", want, err)
		}
	}
	// Disabled: half-filled settings are kept without complaint.
	c := loadToml(t, remoteBaseToml+"\n[linear_agent]\nenabled = false\nwebhook_listen = \"nonsense\"\n")
	if err := c.Validate(); err != nil {
		t.Fatalf("a disabled table must not be validated: %v", err)
	}
}

// require_plan inherits from [defaults] like every other inheritable key, and
// an inherited value is never frozen into the file.
func TestRequirePlanInheritance(t *testing.T) {
	c := loadToml(t, `
[defaults]
require_plan = true

[[project]]
name = "inherits"
path = "/tmp/a"

[[project]]
name = "off"
path = "/tmp/b"
require_plan = false
`)
	in, off := c.ProjectByName("inherits"), c.ProjectByName("off")
	if !in.RequirePlan || !in.Inherits.RequirePlan {
		t.Fatalf("inherits = %v / bit %v", in.RequirePlan, in.Inherits.RequirePlan)
	}
	if off.RequirePlan || off.Inherits.RequirePlan {
		t.Fatalf("explicit false must override: %v / bit %v", off.RequirePlan, off.Inherits.RequirePlan)
	}
	c2, data := saveReload(t, c)
	if strings.Count(data, "require_plan") != 2 { // [defaults] + the override
		t.Fatalf("inherited require_plan must not be written:\n%s", data)
	}
	if !c2.ProjectByName("inherits").RequirePlan || c2.ProjectByName("off").RequirePlan {
		t.Fatal("round trip changed the effective values")
	}
	// A zero-value literal (Inherits all false) is explicit, as for every key.
	lit := Project{Name: "lit", Path: "/tmp/c"}
	c2.Projects = append(c2.Projects, lit)
	c2.ResolveInheritance()
	if c2.ProjectByName("lit").RequirePlan {
		t.Fatal("a literal project must not silently inherit")
	}
}
