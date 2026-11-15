package devices

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/protocol"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newReg(t *testing.T) (*Registry, string, *clock) {
	t.Helper()
	home := t.TempDir()
	c := &clock{t: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)}
	return Open(home, c.now, t.Logf), home, c
}

func key(b byte) [32]byte {
	var k [32]byte
	for i := range k {
		k[i] = b
	}
	return k
}

func relay(rv string) *DeviceRelay {
	return &DeviceRelay{RV: strings.Repeat(rv, 32), BearerHash: strings.Repeat("a", 64), RotatedAt: time.Unix(1, 0).UTC()}
}

var allTiers = protocol.Tiers

func mustAdd(t *testing.T, r *Registry, name string, k byte) Device {
	t.Helper()
	d, err := r.Add(name, "ios", key(k), allTiers, nil)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func writeFile(t *testing.T, home, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func spkiB64(k byte) string { s := key(k); return base64.StdEncoding.EncodeToString(s[:]) }

func TestOpenMissingIsEmpty(t *testing.T) {
	r, home, _ := newReg(t)
	if r.Err() != nil || len(r.List()) != 0 {
		t.Fatalf("err=%v list=%v", r.Err(), r.List())
	}
	if _, err := os.Stat(filepath.Join(home, FileName)); !os.IsNotExist(err) {
		t.Fatal("Open must not create the file")
	}
}

func TestSaveIs0600AndAtomic(t *testing.T) {
	r, home, _ := newReg(t)
	mustAdd(t, r, "A", 1)
	fi, err := os.Stat(filepath.Join(home, FileName))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("stat %v %v", fi, err)
	}
	ents, _ := os.ReadDir(home)
	if len(ents) != 1 {
		t.Fatalf("temp files left: %v", ents)
	}
}

func TestUnparseableFailsClosedAndIsNotRewritten(t *testing.T) {
	home := t.TempDir()
	junk := "{not json"
	writeFile(t, home, FileName, junk)
	r := Open(home, nil, t.Logf)
	if !errors.Is(r.Err(), ErrBroken) {
		t.Fatalf("err=%v", r.Err())
	}
	if !strings.Contains(r.Err().Error(), filepath.Join(home, FileName)) {
		t.Fatalf("error must name the file: %v", r.Err())
	}
	if _, ok := r.Lookup(key(1)); ok {
		t.Fatal("broken registry authenticated")
	}
	for name, fn := range map[string]func() error{
		"add":    func() error { _, e := r.Add("A", "ios", key(1), allTiers, nil); return e },
		"revoke": func() error { _, e := r.Revoke("x"); return e },
		"tiers":  func() error { _, e := r.SetTiers("x", allTiers); return e },
		"relay":  func() error { _, e := r.SetRelay("x", nil); return e },
	} {
		if err := fn(); !errors.Is(err, ErrBroken) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	r.Touch("x")
	b, _ := os.ReadFile(filepath.Join(home, FileName))
	if string(b) != junk {
		t.Fatalf("file rewritten: %q", b)
	}
}

func TestUnknownVersionFailsClosed(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, FileName, `{"version":2,"epoch":0,"devices":[]}`)
	if r := Open(home, nil, t.Logf); !errors.Is(r.Err(), ErrBroken) {
		t.Fatalf("err=%v", r.Err())
	}
}

func devJSON(id string, k byte, rv string) string {
	rl := ""
	if rv != "" {
		rl = fmt.Sprintf(`,"relay":{"rv":%q,"bearer_sha256":%q,"rotated_at":"2026-01-01T00:00:00Z"}`, strings.Repeat(rv, 32), strings.Repeat("b", 64))
	}
	return fmt.Sprintf(`{"id":%q,"name":"n","spki":%q,"tiers":["read"],"epoch":1,"pairedAt":"2026-01-01T00:00:00Z"%s}`, id, spkiB64(k), rl)
}

func TestDuplicateSPKIFailsClosed(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, FileName, `{"version":1,"epoch":1,"devices":[`+devJSON("a", 1, "")+","+devJSON("b", 1, "")+`]}`)
	if r := Open(home, nil, t.Logf); r.Err() == nil {
		t.Fatal("duplicate spki accepted")
	}
	home = t.TempDir()
	writeFile(t, home, FileName, `{"version":1,"epoch":1,"devices":[`+devJSON("a", 1, "")+","+devJSON("a", 2, "")+`]}`)
	if r := Open(home, nil, t.Logf); r.Err() == nil {
		t.Fatal("duplicate id accepted")
	}
	home = t.TempDir()
	writeFile(t, home, FileName, `{"version":1,"epoch":1,"devices":[{"id":"a","name":"n","spki":"AAAA","tiers":["read"]}]}`)
	if r := Open(home, nil, t.Logf); r.Err() == nil {
		t.Fatal("short spki accepted")
	}
}

func TestDuplicateRVFailsClosed(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, FileName, `{"version":1,"epoch":1,"devices":[`+devJSON("a", 1, "c")+","+devJSON("b", 2, "c")+`]}`)
	if r := Open(home, nil, t.Logf); r.Err() == nil {
		t.Fatal("shared rv accepted")
	}
}

func TestAddSameSPKIReplacesAndBumpsEpoch(t *testing.T) {
	r, _, c := newReg(t)
	a := mustAdd(t, r, "Old", 1)
	c.t = c.t.Add(time.Hour)
	b, err := r.Add("New", "ios", key(1), []string{"read"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != b.ID || b.Epoch != a.Epoch+1 || b.Name != "New" || !b.PairedAt.After(a.PairedAt) || len(r.List()) != 1 {
		t.Fatalf("%+v -> %+v", a, b)
	}
	if b.Has(protocol.TierControl) {
		t.Fatal("tiers not replaced")
	}
}

func TestAddValidatesName(t *testing.T) {
	r, _, _ := newReg(t)
	for _, n := range []string{"", "   ", strings.Repeat("x", MaxNameRunes+1), "a\x00b", "a​b", "a\nb"} {
		if _, err := r.Add(n, "ios", key(1), allTiers, nil); !errors.Is(err, ErrBadName) {
			t.Errorf("%q: %v", n, err)
		}
	}
	d, err := r.Add("  "+strings.Repeat("é", MaxNameRunes)+" ", "ios", key(1), allTiers, nil)
	if err != nil || d.Name != strings.Repeat("é", MaxNameRunes) {
		t.Fatalf("%q %v", d.Name, err)
	}
}

func TestMaxDevices(t *testing.T) {
	r, _, _ := newReg(t)
	for i := 0; i < MaxDevices; i++ {
		mustAdd(t, r, "d", byte(i+1))
	}
	if _, err := r.Add("x", "ios", key(200), allTiers, nil); !errors.Is(err, ErrFull) {
		t.Fatalf("err=%v", err)
	}
	if _, err := r.Add("again", "ios", key(1), allTiers, nil); err != nil {
		t.Fatalf("re-pair of a present key must work when full: %v", err)
	}
}

func TestIDIs17Chars(t *testing.T) {
	h := HashSPKI([]byte("spki"))
	id := IDFor(h)
	if len(id) != 17 || id[0] != 'd' {
		t.Fatal(id)
	}
	r, _, _ := newReg(t)
	if d := mustAdd(t, r, "A", 1); d.ID != IDFor(key(1)) {
		t.Fatal(d.ID)
	}
}

func TestSetTiersRequiresRead(t *testing.T) {
	r, _, _ := newReg(t)
	d := mustAdd(t, r, "A", 1)
	for _, ts := range [][]string{nil, {"control"}, {"read", "root"}} {
		if _, err := r.SetTiers(d.ID, ts); !errors.Is(err, ErrBadTiers) {
			t.Errorf("%v: %v", ts, err)
		}
	}
	got, err := r.SetTiers(d.ID, []string{"control", "read", "read"})
	if err != nil || fmt.Sprint(got.Tiers) != "[read control]" {
		t.Fatalf("%v %v", got.Tiers, err)
	}
	if _, err := r.SetTiers("nope", allTiers); !errors.Is(err, ErrUnknownDevice) {
		t.Fatal(err)
	}
}

func TestUnknownTierInFileDropped(t *testing.T) {
	home := t.TempDir()
	body := fmt.Sprintf(`{"version":1,"epoch":1,"devices":[{"id":"a","name":"n","spki":%q,"tiers":["read","root","control"],"epoch":1},{"id":"b","name":"n","spki":%q,"tiers":["root"],"epoch":1}]}`, spkiB64(1), spkiB64(2))
	writeFile(t, home, FileName, body)
	r := Open(home, nil, t.Logf)
	a, _ := r.Get("a")
	b, _ := r.Get("b")
	if fmt.Sprint(a.Tiers) != "[read control]" || fmt.Sprint(b.Tiers) != "[read]" {
		t.Fatalf("%v %v", a.Tiers, b.Tiers)
	}
}

func TestMalformedRelayRecordDropped(t *testing.T) {
	home := t.TempDir()
	d := fmt.Sprintf(`{"id":"a","name":"n","spki":%q,"tiers":["read"],"epoch":1,"relay":{"rv":"XYZ","bearer_sha256":"zz","rotated_at":"2026-01-01T00:00:00Z"}}`, spkiB64(1))
	writeFile(t, home, FileName, `{"version":1,"epoch":1,"devices":[`+d+`]}`)
	r := Open(home, nil, t.Logf)
	got, ok := r.Get("a")
	if r.Err() != nil || !ok || got.Relay != nil {
		t.Fatalf("err=%v ok=%v relay=%v", r.Err(), ok, got.Relay)
	}
	// Add / SetRelay reject malformed and foreign rv.
	d1 := mustAdd(t, r, "B", 2)
	bad := []*DeviceRelay{
		{RV: "x", BearerHash: strings.Repeat("a", 64)},
		{RV: strings.Repeat("a", 32), BearerHash: "short"},
		{RV: strings.Repeat("a", 32), BearerHash: strings.Repeat("a", 64), PrevHash: strings.Repeat("a", 64), PrevUntil: time.Now()},
		{RV: strings.Repeat("a", 32), BearerHash: strings.Repeat("a", 64), PrevHash: strings.Repeat("b", 64)},
		{RV: strings.Repeat("a", 32), BearerHash: strings.Repeat("a", 64), PrevUntil: time.Now()},
	}
	for i, rl := range bad {
		if _, err := r.SetRelay(d1.ID, rl); !errors.Is(err, ErrBadRelay) {
			t.Errorf("%d: %v", i, err)
		}
	}
	if _, err := r.SetRelay(d1.ID, relay("c")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Add("C", "ios", key(3), allTiers, relay("c")); !errors.Is(err, ErrBadRelay) {
		t.Fatalf("foreign rv: %v", err)
	}
	if got, err := r.SetRelay(d1.ID, nil); err != nil || got.Relay != nil {
		t.Fatalf("clear: %v %v", got.Relay, err)
	}
}

func TestRelayRegexesMatchHubproto(t *testing.T) {
	samples := []string{"", "a", strings.Repeat("a", 32), strings.Repeat("a", 31), strings.Repeat("a", 33), strings.Repeat("A", 32),
		strings.Repeat("g", 32), strings.Repeat("0", 64), strings.Repeat("f", 64), strings.Repeat("F", 64), strings.Repeat("a", 63),
		strings.Repeat("a", 65), strings.Repeat("a", 31) + "\n", strings.Repeat("a", 64) + "\n", " " + strings.Repeat("a", 32)}
	for i := 0; i < 20; i++ {
		b := make([]byte, 16)
		rand.Read(b)
		samples = append(samples, fmt.Sprintf("%x", b), fmt.Sprintf("%x", append(b, b...)))
	}
	for _, s := range samples {
		if relayIDRe.MatchString(s) != hubproto.ValidRelayID(s) {
			t.Errorf("id %q", s)
		}
		if relayHashRe.MatchString(s) != hubproto.ValidRelayHash(s) {
			t.Errorf("hash %q", s)
		}
	}
}

func TestRevokeUnknown(t *testing.T) {
	r, _, _ := newReg(t)
	if _, err := r.Revoke("dnope"); !errors.Is(err, ErrUnknownDevice) {
		t.Fatal(err)
	}
}

func failWrite(string, []byte) error { return errors.New("disk full") }

func TestRevokeJournalsWhenFileUnwritable(t *testing.T) {
	r, home, c := newReg(t)
	d := mustAdd(t, r, "A", 1)
	mustAdd(t, r, "B", 2)
	r.write = failWrite
	_, err := r.Revoke(d.ID)
	if err == nil || errors.Is(err, ErrRevokeUnsaved) || !strings.Contains(err.Error(), RevokedFileName) {
		t.Fatalf("err=%v", err)
	}
	if _, ok := r.Get(d.ID); ok || r.Unsaved() != nil {
		t.Fatal("memory must drop it; journal write succeeded so not unsaved")
	}
	b, _ := os.ReadFile(filepath.Join(home, RevokedFileName))
	if string(b) != fmt.Sprintf("%s %d\n", d.SPKI, d.Epoch) {
		t.Fatalf("journal %q", b)
	}
	r2 := Open(home, c.now, t.Logf)
	if _, ok := r2.Get(d.ID); ok || r2.Err() != nil || len(r2.List()) != 1 {
		t.Fatalf("revoked device visible after reopen: %v", r2.List())
	}
}

func TestRevokeJournalAppliesOnlyUpToItsEpoch(t *testing.T) {
	r, home, c := newReg(t)
	d := mustAdd(t, r, "A", 1)
	r.write = failWrite
	if _, err := r.Revoke(d.ID); err == nil {
		t.Fatal("want journal note")
	}
	// Re-pair with a higher epoch while the old record is still journalled.
	r.write = writeAtomic
	d2 := mustAdd(t, r, "A again", 1)
	if d2.Epoch <= d.Epoch {
		t.Fatal("epoch must grow")
	}
	// Simulate a crash before the save absorbed the journal: restore it.
	if err := appendJournalLine(filepath.Join(home, RevokedFileName), d.SPKI, d.Epoch); err != nil {
		t.Fatal(err)
	}
	r2 := Open(home, c.now, t.Logf)
	if _, ok := r2.Get(d2.ID); !ok {
		t.Fatal("re-paired device hidden by an older journal line")
	}
}

func TestRevokeSuccessTruncatesJournal(t *testing.T) {
	r, home, _ := newReg(t)
	a := mustAdd(t, r, "A", 1)
	mustAdd(t, r, "B", 2)
	r.write = failWrite
	r.Revoke(a.ID)
	r.write = writeAtomic
	b, _ := r.Lookup(key(2))
	if _, err := r.Revoke(b.ID); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(home, RevokedFileName))
	if err != nil || fi.Size() != 0 {
		t.Fatalf("journal not truncated: %v %v", fi, err)
	}
	if n := len(Open(home, nil, t.Logf).List()); n != 0 {
		t.Fatalf("n=%d", n)
	}
}

func TestRevokeUnsavedWhenBothUnwritable(t *testing.T) {
	r, _, _ := newReg(t)
	a := mustAdd(t, r, "A", 1)
	mustAdd(t, r, "B", 2)
	r.write = failWrite
	r.appendJournal = func(string, string, uint64) error { return errors.New("no") }
	var fired int32
	r.Subscribe(func() { atomic.AddInt32(&fired, 1) })
	if _, err := r.Revoke(a.ID); !errors.Is(err, ErrRevokeUnsaved) {
		t.Fatalf("err=%v", err)
	}
	if _, ok := r.Get(a.ID); ok || r.Unsaved() == nil || fired != 1 {
		t.Fatalf("memory/unsaved/subscribers wrong (fired=%d)", fired)
	}
	r.write = writeAtomic
	r.Touch(mustAdd2(t, r))
	if r.Unsaved() != nil {
		t.Fatal("a good save must clear Unsaved")
	}
}

func mustAdd2(t *testing.T, r *Registry) string {
	t.Helper()
	d, ok := r.Lookup(key(2))
	if !ok {
		t.Fatal("missing B")
	}
	return d.ID
}

func TestUnparseableJournalFailsClosed(t *testing.T) {
	for _, body := range []string{"garbage\n", "AAAA 3\n", spkiB64(1) + " x\n", spkiB64(1) + " 1 extra\n"} {
		home := t.TempDir()
		writeFile(t, home, RevokedFileName, body)
		if r := Open(home, nil, t.Logf); !errors.Is(r.Err(), ErrBroken) {
			t.Errorf("%q: %v", body, r.Err())
		}
	}
}

func TestTouchPersistsAtMostEvery(t *testing.T) {
	r, home, c := newReg(t)
	d := mustAdd(t, r, "A", 1)
	read := func() time.Time {
		b, _ := os.ReadFile(filepath.Join(home, FileName))
		var f file
		json.Unmarshal(b, &f)
		return f.Devices[0].LastSeenAt
	}
	c.t = c.t.Add(time.Minute)
	r.Touch(d.ID)
	if !read().IsZero() {
		t.Fatal("persisted too early")
	}
	if got, _ := r.Get(d.ID); got.LastSeenAt.IsZero() {
		t.Fatal("memory must update")
	}
	c.t = c.t.Add(lastSeenPersistEvery)
	r.Touch(d.ID)
	if read().IsZero() {
		t.Fatal("not persisted after the interval")
	}
}

func TestSubscribersRunOutsideLock(t *testing.T) {
	r, _, _ := newReg(t)
	var n int32
	r.Subscribe(func() { atomic.AddInt32(&n, 1); _ = r.List() })
	done := make(chan struct{})
	go func() {
		d := mustAdd(t, r, "A", 1)
		r.SetTiers(d.ID, []string{"read"})
		r.SetRelay(d.ID, relay("d"))
		r.Revoke(d.ID)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock: subscriber ran under the lock")
	}
	if n != 4 {
		t.Fatalf("n=%d", n)
	}
}

func TestLookupRV(t *testing.T) {
	r, _, _ := newReg(t)
	d := mustAdd(t, r, "A", 1)
	if _, ok := r.LookupRV(strings.Repeat("e", 32)); ok {
		t.Fatal("found before set")
	}
	r.SetRelay(d.ID, relay("e"))
	got, ok := r.LookupRV(strings.Repeat("e", 32))
	if !ok || got.ID != d.ID {
		t.Fatal("not found")
	}
	r.Revoke(d.ID)
	if _, ok := r.LookupRV(strings.Repeat("e", 32)); ok {
		t.Fatal("found after revoke")
	}
}

func TestFailedWriteLeavesMemoryUnchanged(t *testing.T) {
	r, _, _ := newReg(t)
	d := mustAdd(t, r, "A", 1)
	r.write = failWrite
	if _, err := r.SetTiers(d.ID, []string{"read"}); err == nil {
		t.Fatal("want error")
	}
	if got, _ := r.Get(d.ID); !got.Has(protocol.TierShell) {
		t.Fatal("memory changed despite failed write")
	}
	if !bytes.Equal([]byte(d.ID), []byte(IDFor(key(1)))) {
		t.Fatal("id")
	}
}
