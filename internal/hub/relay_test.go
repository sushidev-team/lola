package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hubproto"
)

func relayEntry(t *testing.T) hubproto.RelayRendezvous {
	t.Helper()
	rv, err := hubproto.NewRelayID()
	if err != nil {
		t.Fatal(err)
	}
	_, hash, err := hubproto.NewRelayBearer()
	if err != nil {
		t.Fatal(err)
	}
	return hubproto.RelayRendezvous{RV: rv, BearerSHA256: hash}
}

func TestSyncRendezvousDropsForeignConflicts(t *testing.T) {
	c, h := testClient(t)
	e1, e2 := relayEntry(t), relayEntry(t)
	foreign, _ := hubproto.NewRelayID()
	h.routes["POST "+hubproto.PathRelayRendezvous] = func(body []byte) (int, any) {
		var req hubproto.RendezvousRequest
		if err := json.Unmarshal(body, &req); err != nil || len(req.Entries) != 2 {
			t.Errorf("body = %s", body)
		}
		return 200, hubproto.RendezvousResponse{Registered: 1, Conflicts: []string{foreign, e2.RV, e2.RV}}
	}
	resp, err := c.SyncRendezvous(context.Background(), []hubproto.RelayRendezvous{e1, e2})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Registered != 1 || len(resp.Conflicts) != 1 || resp.Conflicts[0] != e2.RV {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestSyncRendezvousEmptySetSendsEmptyList(t *testing.T) {
	c, h := testClient(t)
	h.routes["POST "+hubproto.PathRelayRendezvous] = func([]byte) (int, any) {
		return 200, hubproto.RendezvousResponse{}
	}
	if _, err := c.SyncRendezvous(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if got := string(h.last().Body); got != `{"entries":[]}` {
		t.Fatalf("body = %s", got)
	}
}

func TestSyncRendezvousRefusesBadRequestAndAnswer(t *testing.T) {
	c, h := testClient(t)
	if _, err := c.SyncRendezvous(context.Background(), []hubproto.RelayRendezvous{{RV: "nope"}}); err == nil {
		t.Fatal("invalid entry sent")
	}
	h.routes["POST "+hubproto.PathRelayRendezvous] = func([]byte) (int, any) {
		return 200, hubproto.RendezvousResponse{Registered: 5}
	}
	if _, err := c.SyncRendezvous(context.Background(), []hubproto.RelayRendezvous{relayEntry(t)}); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("err = %v", err)
	}
}

// relayServer answers every upgrade attempt with a refusal.
func relayServer(t *testing.T, h http.HandlerFunc) (*RelayClient, *Key) {
	t.Helper()
	key, err := LoadOrCreateKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &RelayClient{BaseURL: srv.URL, Key: key}, key
}

func TestRelayHandshakeErrorIsCleaned(t *testing.T) {
	huge := strings.Repeat("x", 4<<10)
	rc, _ := relayServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(hubproto.ErrorResponse{Error: huge, Code: "Not-A-Known_Code!"})
	})
	for name, dial := range map[string]func() error{
		"control": func() error { _, err := rc.DialControl(context.Background()); return err },
		"accept": func() error {
			id, _ := hubproto.NewRelayID()
			_, err := rc.DialAccept(context.Background(), id)
			return err
		},
	} {
		err := dial()
		var ae *APIError
		if !errors.As(err, &ae) {
			t.Fatalf("%s: err = %v", name, err)
		}
		if ae.Status != 429 || ae.Code != "" || ae.Message != "" || ae.RetryAfter != 30*time.Second {
			t.Errorf("%s: %+v", name, ae)
		}
		if strings.Contains(err.Error(), "xxx") {
			t.Errorf("%s: hub text leaked: %v", name, err)
		}
	}

	rc2, _ := relayServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(hubproto.ErrorResponse{Error: "no", Code: hubproto.CodeUnauthorized})
	})
	if _, err := rc2.DialControl(context.Background()); !IsCode(err, hubproto.CodeUnauthorized) {
		t.Fatalf("401 not surfaced as unauthorized: %v", err)
	}
}

func TestRelayDialsAreSignedAndShaped(t *testing.T) {
	var gotURI string
	var gotHdr http.Header
	var kp *Key
	rc, key := relayServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotURI, gotHdr = r.URL.RequestURI(), r.Header.Clone()
		if _, err := hubproto.Verify(r, nil, kp.Public, time.Now()); err != nil {
			t.Errorf("signature does not verify: %v", err)
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	kp = key
	_, _ = rc.DialControl(context.Background())
	if gotURI != hubproto.PathRelayDaemon+"?v=1" || gotHdr.Get(hubproto.HeaderDevice) != key.DeviceID() {
		t.Fatalf("control: %s %v", gotURI, gotHdr)
	}
	id, _ := hubproto.NewRelayID()
	_, _ = rc.DialAccept(context.Background(), id)
	if gotURI != hubproto.PathRelayAccept+"?conn="+id {
		t.Fatalf("accept uri = %s", gotURI)
	}
	if _, err := rc.DialAccept(context.Background(), "../x"); err == nil {
		t.Fatal("bad conn id accepted")
	}
	if _, err := (&RelayClient{BaseURL: rc.BaseURL}).DialControl(context.Background()); err == nil {
		t.Fatal("keyless dial accepted")
	}
}

func TestRelayStatusRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if _, ok := LoadRelayStatus(dir); ok {
		t.Fatal("status from nothing")
	}
	in := RelayStatus{State: "connected", Since: time.Unix(1700000000, 0).UTC(), Registered: 2, RegisteredURL: "https://h.example", RegisteredDevice: "d"}
	if err := SaveRelayStatus(dir, in); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, RelayStatusFile))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("stat = %v %v", fi, err)
	}
	out, ok := LoadRelayStatus(dir)
	if !ok || !out.Since.Equal(in.Since) || out.State != in.State || out.Registered != 2 || out.RegisteredURL != in.RegisteredURL {
		t.Fatalf("out = %+v ok=%v", out, ok)
	}
	if err := os.WriteFile(filepath.Join(dir, RelayStatusFile), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadRelayStatus(dir); ok {
		t.Fatal("garbage loaded")
	}
}
