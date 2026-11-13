package hub

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
)

func boardRow(id string) hubproto.ItemView {
	it := advisoryItem("acme/web")
	it.ID = id
	t0 := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	return hubproto.ItemView{Item: it, State: hubproto.StateOpen, FirstSeen: t0, LastSeen: t0.Add(time.Hour)}
}

func TestBoardValidatesTheWholeResponse(t *testing.T) {
	c, h := testClient(t)
	good := boardRow("item1")
	h.routes["GET "+hubproto.PathBoard] = func([]byte) (int, any) {
		return 200, hubproto.BoardResponse{Items: []hubproto.ItemView{good}}
	}
	resp, err := c.Board(context.Background(), hubproto.StateOpen, 50)
	if err != nil || len(resp.Items) != 1 || resp.Items[0].Item.ID != "item1" {
		t.Fatalf("Board = %+v, %v", resp, err)
	}
	if got := h.last().Path; got != "/v1/board?limit=50&state=open" {
		t.Errorf("path = %q", got)
	}

	// One bad row refuses the whole board: no partial junk.
	bad := boardRow("item2")
	bad.Escalation = &hubproto.EscalationView{URL: "javascript:alert(1)"}
	h.routes["GET "+hubproto.PathBoard] = func([]byte) (int, any) {
		return 200, hubproto.BoardResponse{Items: []hubproto.ItemView{good, bad}}
	}
	if _, err := c.Board(context.Background(), "", 0); !errors.Is(err, ErrInvalidResponse) {
		t.Errorf("bad row: %v", err)
	}
	// An unknown field fails the strict decode.
	h.routes["GET "+hubproto.PathBoard] = func([]byte) (int, any) {
		return 200, []byte(`{"items":[],"prompt":"x"}`)
	}
	if _, err := c.Board(context.Background(), "", 0); !errors.Is(err, ErrInvalidResponse) {
		t.Errorf("unknown field: %v", err)
	}
	if _, err := c.Board(context.Background(), "bogus", 0); err == nil {
		t.Error("an unknown state must not be sent")
	}
}

func TestBoardReadsPastMaxBody(t *testing.T) {
	c, h := testClient(t)
	var rows []hubproto.ItemView
	for i := range 400 {
		rows = append(rows, boardRow("item"+strconv.Itoa(i)))
	}
	h.routes["GET "+hubproto.PathBoard] = func([]byte) (int, any) {
		return 200, hubproto.BoardResponse{Items: rows}
	}
	resp, err := c.Board(context.Background(), "", 0)
	if err != nil || len(resp.Items) != 400 {
		t.Fatalf("a board over MaxBody must still decode: %d, %v", len(resp.Items), err)
	}
}

func TestOrgValidates(t *testing.T) {
	c, h := testClient(t)
	h.routes["GET "+hubproto.PathOrg] = func([]byte) (int, any) {
		return 200, hubproto.OrgView{Org: "acme", PolicyVersion: 2, Devices: []hubproto.DeviceView{{Name: "mbp", Me: true, Agents: []string{"claude"}}}}
	}
	o, err := c.Org(context.Background())
	if err != nil || o.Org != "acme" || len(o.Devices) != 1 {
		t.Fatalf("Org = %+v, %v", o, err)
	}
	h.routes["GET "+hubproto.PathOrg] = func([]byte) (int, any) {
		return 200, hubproto.OrgView{Org: "acme", Devices: []hubproto.DeviceView{{Name: "a", Me: true}, {Name: "b", Me: true}}}
	}
	if _, err := c.Org(context.Background()); !errors.Is(err, ErrInvalidResponse) {
		t.Errorf("two me devices: %v", err)
	}
}

func TestAcceptIntent(t *testing.T) {
	it := advisoryItem("acme/web")
	h := config.HubConfig{Enabled: true}
	p := &config.Project{Name: "web", Repo: "Acme/Web", Sandbox: "docker"}
	if ok, why := AcceptIntent(it, p, h); !ok {
		t.Fatalf("an un-opted-in sandboxed project must accept a human claim: %s", why)
	}
	host := &config.Project{Name: "web", Repo: "acme/web"}
	if ok, why := AcceptIntent(it, host, h); ok || why != ReasonNeedsSandbox {
		t.Errorf("require_sandbox must still hold: %v %q", ok, why)
	}
	h.AcceptKinds = []string{string(hubproto.KindRedBumpPR)}
	if ok, why := AcceptIntent(it, p, h); ok || why != ReasonKindRejected {
		t.Errorf("kind: %v %q", ok, why)
	}
	cfg := &config.Config{Projects: []config.Project{*p, {Name: "other", Repo: "acme/web"}, {Name: "x", Repo: "acme/x"}}}
	if got := ProjectsFor(cfg, it); len(got) != 2 {
		t.Errorf("ProjectsFor = %d", len(got))
	}
}
