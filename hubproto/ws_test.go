package hubproto

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var _ net.Conn = (*WSConn)(nil)

func tcpPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ch := make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept()
		ch <- c
	}()
	a, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	b := <-ch
	t.Cleanup(func() { a.Close(); b.Close() })
	return a, b
}

// rawFrame encodes one frame by hand so tests can break the rules.
func rawFrame(b0 byte, payload []byte, mask bool, lenEnc int) []byte {
	var out []byte
	out = append(out, b0)
	mb := byte(0)
	if mask {
		mb = 0x80
	}
	n := len(payload)
	switch {
	case lenEnc == 16 || (lenEnc == 0 && n >= 126 && n < 65536):
		out = append(out, mb|126, byte(n>>8), byte(n))
	case lenEnc == 64 || (lenEnc == 0 && n >= 65536):
		out = append(out, mb|127, 0, 0, 0, 0, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	default:
		out = append(out, mb|byte(n))
	}
	if mask {
		key := [4]byte{1, 2, 3, 4}
		out = append(out, key[:]...)
		p := append([]byte(nil), payload...)
		xorMask(key, 0, p)
		return append(out, p...)
	}
	return append(out, payload...)
}

// readRawFrame reads one unmasked frame a WSServer wrote.
func readRawFrame(t *testing.T, c net.Conn) (byte, []byte) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	var h [2]byte
	if _, err := io.ReadFull(c, h[:]); err != nil {
		t.Fatalf("read frame header: %v", err)
	}
	n := int(h[1] & 0x7f)
	switch n {
	case 126:
		var e [2]byte
		io.ReadFull(c, e[:])
		n = int(binary.BigEndian.Uint16(e[:]))
	case 127:
		var e [8]byte
		io.ReadFull(c, e[:])
		n = int(binary.BigEndian.Uint64(e[:]))
	}
	p := make([]byte, n)
	if _, err := io.ReadFull(c, p); err != nil {
		t.Fatal(err)
	}
	return h[0], p
}

func expectClose(t *testing.T, c net.Conn, code uint16) {
	t.Helper()
	for {
		b0, p := readRawFrame(t, c)
		if b0&0x0f == opClose {
			if len(p) < 2 || binary.BigEndian.Uint16(p) != code {
				t.Fatalf("close payload %v, want code %d", p, code)
			}
			return
		}
	}
}

// serverOver returns a WSServer conn plus the raw client socket feeding it.
func serverOver(t *testing.T, cfg WSConfig) (*WSConn, net.Conn) {
	a, b := tcpPair(t)
	return NewWSConn(b, nil, WSServer, cfg), a
}

func TestWSHandshakeAcceptKey(t *testing.T) {
	if got := acceptKey("dGhlIHNhbXBsZSBub25jZQ=="); got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("accept = %s", got)
	}
}

func upgradeReq() *http.Request {
	r := httptest.NewRequest("GET", "/v1/relay/daemon", nil)
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Connection", "keep-alive, Upgrade")
	r.Header.Set("Sec-WebSocket-Version", "13")
	r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	r.Header.Set("Sec-WebSocket-Protocol", "x, "+WSSubprotocol)
	return r
}

func TestCheckUpgradeRefuses(t *testing.T) {
	if err := CheckUpgrade(upgradeReq()); err != nil {
		t.Fatalf("good request refused: %v", err)
	}
	cases := map[string]func(r *http.Request){
		"POST":          func(r *http.Request) { r.Method = "POST" },
		"HTTP/1.0":      func(r *http.Request) { r.ProtoMinor = 0 },
		"HTTP/2":        func(r *http.Request) { r.ProtoMajor, r.ProtoMinor = 2, 0 },
		"no upgrade":    func(r *http.Request) { r.Header.Del("Upgrade") },
		"foreign":       func(r *http.Request) { r.Header.Set("Upgrade", "h2c") },
		"no connection": func(r *http.Request) { r.Header.Set("Connection", "keep-alive") },
		"version 8":     func(r *http.Request) { r.Header.Set("Sec-WebSocket-Version", "8") },
		"key 15":        func(r *http.Request) { r.Header.Set("Sec-WebSocket-Key", "AAAAAAAAAAAAAAAAAAAA") },
		"key 17":        func(r *http.Request) { r.Header.Set("Sec-WebSocket-Key", "AAAAAAAAAAAAAAAAAAAAAAA=") },
		"two keys": func(r *http.Request) {
			r.Header.Add("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
		},
		"no subprotocol": func(r *http.Request) { r.Header.Del("Sec-WebSocket-Protocol") },
		"origin":         func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") },
	}
	for name, mut := range cases {
		r := upgradeReq()
		mut(r)
		err := CheckUpgrade(r)
		if !errors.Is(err, ErrWSHandshake) {
			t.Errorf("%s: err = %v", name, err)
		} else if strings.Contains(err.Error(), "evil.example") {
			t.Errorf("%s: error leaks a value", name)
		}
	}
}

// wsEchoServer accepts upgrades and runs fn on the server conn.
func wsServer(t *testing.T, cfg WSConfig, fn func(*WSConn)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := AcceptWebSocket(w, r, cfg)
		if err != nil {
			http.Error(w, "no", 400)
			return
		}
		fn(c)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func dial(t *testing.T, srv *httptest.Server, d WSDialer) *WSConn {
	t.Helper()
	req, _ := http.NewRequest("GET", srv.URL+"/x?a=b", nil)
	c, err := d.Dial(context.Background(), req)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.CloseNow(WSCloseNormal) })
	return c
}

func TestWSClientServerRoundTrip(t *testing.T) {
	srv := wsServer(t, WSConfig{}, func(c *WSConn) {
		defer c.Close()
		io.Copy(c, c)
	})
	c := dial(t, srv, WSDialer{})
	data := make([]byte, 1<<20)
	rand.Read(data)
	go func() { c.Write(data) }()
	got := make([]byte, len(data))
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("stream differs")
	}
}

func TestWSMaskingRules(t *testing.T) {
	s, raw := serverOver(t, WSConfig{})
	raw.Write(rawFrame(0x82, []byte("hi"), false, 0))
	if _, err := s.Read(make([]byte, 8)); !errors.Is(err, ErrWSProtocol) {
		t.Fatalf("server read unmasked: %v", err)
	}
	expectClose(t, raw, WSCloseProtocol)

	a, b := tcpPair(t)
	cl := NewWSConn(a, nil, WSClient, WSConfig{})
	b.Write(rawFrame(0x82, []byte("hi"), true, 0))
	if _, err := cl.Read(make([]byte, 8)); !errors.Is(err, ErrWSProtocol) {
		t.Fatalf("client read masked: %v", err)
	}
}

func TestWSFrameRules(t *testing.T) {
	big := make([]byte, WSMaxFramePayload+1)
	cases := []struct {
		name  string
		frame []byte
		code  uint16
		err   error
	}{
		{"rsv1", rawFrame(0xC2, []byte("x"), true, 0), WSCloseProtocol, ErrWSProtocol},
		{"unknown opcode", rawFrame(0x83, []byte("x"), true, 0), WSCloseProtocol, ErrWSProtocol},
		{"non-minimal 16", rawFrame(0x82, []byte("x"), true, 16), WSCloseProtocol, ErrWSProtocol},
		{"non-minimal 64", rawFrame(0x82, []byte("x"), true, 64), WSCloseProtocol, ErrWSProtocol},
		{"64-bit msb", func() []byte {
			f := rawFrame(0x82, []byte("x"), true, 64)
			f[2] = 0x80
			return f
		}(), WSCloseProtocol, ErrWSProtocol},
		{"control over 125", rawFrame(0x89, make([]byte, 126), true, 0), WSCloseProtocol, ErrWSProtocol},
		{"fragmented control", rawFrame(0x09, nil, true, 0), WSCloseProtocol, ErrWSProtocol},
		{"text", rawFrame(0x81, []byte("x"), true, 0), WSCloseUnsupported, ErrWSProtocol},
		{"too big", rawFrame(0x82, big, true, 0)[:14], WSCloseTooBig, ErrWSTooBig},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, raw := serverOver(t, WSConfig{})
			raw.Write(tc.frame)
			_, err := s.Read(make([]byte, 8))
			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
			expectClose(t, raw, tc.code)
		})
	}
}

func TestWSFragmentedStream(t *testing.T) {
	s, raw := serverOver(t, WSConfig{})
	var w bytes.Buffer
	w.Write(rawFrame(0x02, []byte("abc"), true, 0))
	w.Write(rawFrame(0x89, []byte("p"), true, 0))
	w.Write(rawFrame(0x00, []byte("def"), true, 0))
	w.Write(rawFrame(0x80, []byte("ghi"), true, 0))
	raw.Write(w.Bytes())
	got := make([]byte, 9)
	if _, err := io.ReadFull(s, got); err != nil || string(got) != "abcdefghi" {
		t.Fatalf("got %q, %v", got, err)
	}
	if b0, p := readRawFrame(t, raw); b0 != 0x8A || string(p) != "p" {
		t.Fatalf("pong = %x %q", b0, p)
	}

	s2, raw2 := serverOver(t, WSConfig{})
	raw2.Write(rawFrame(0x80, []byte("x"), true, 0))
	if _, err := s2.Read(make([]byte, 4)); !errors.Is(err, ErrWSProtocol) {
		t.Fatalf("orphan continuation: %v", err)
	}
	expectClose(t, raw2, WSCloseProtocol)
}

func TestWSReadMessageRefusesFragments(t *testing.T) {
	s, raw := serverOver(t, WSConfig{})
	raw.Write(rawFrame(0x02, []byte("abc"), true, 0))
	if _, err := s.ReadMessage(100); !errors.Is(err, ErrWSTooBig) {
		t.Fatalf("err = %v", err)
	}
	expectClose(t, raw, WSCloseTooBig)

	s2, raw2 := serverOver(t, WSConfig{})
	raw2.Write(rawFrame(0x82, []byte("abcd"), true, 0))
	if _, err := s2.ReadMessage(3); !errors.Is(err, ErrWSTooBig) {
		t.Fatalf("over limit: %v", err)
	}
	s3, raw3 := serverOver(t, WSConfig{})
	raw3.Write(rawFrame(0x82, []byte("abcd"), true, 0))
	if m, err := s3.ReadMessage(4); err != nil || string(m) != "abcd" {
		t.Fatalf("message %q %v", m, err)
	}
}

func TestWSWriteChunks(t *testing.T) {
	a, b := tcpPair(t)
	s := NewWSConn(a, nil, WSServer, WSConfig{})
	go s.Write(make([]byte, 100<<10))
	total := 0
	for total < 100<<10 {
		b0, p := readRawFrame(t, b)
		if b0 != 0x82 || len(p) > WSWriteChunk {
			t.Fatalf("frame %x len %d", b0, len(p))
		}
		total += len(p)
	}
}

func TestWSPingPong(t *testing.T) {
	s, raw := serverOver(t, WSConfig{})
	go s.Read(make([]byte, 4))
	raw.Write(rawFrame(0x89, []byte("hello"), true, 0))
	if b0, p := readRawFrame(t, raw); b0 != 0x8A || string(p) != "hello" {
		t.Fatalf("pong %x %q", b0, p)
	}

	// DeadAfter closes a silent peer.
	d, _ := serverOver(t, WSConfig{DeadAfter: 100 * time.Millisecond})
	errc := make(chan error, 1)
	go func() { _, err := d.Read(make([]byte, 4)); errc <- err }()
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("nil error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watchdog did not fire")
	}

	// A pinging peer keeps it alive.
	k, kraw := serverOver(t, WSConfig{DeadAfter: 300 * time.Millisecond})
	go io.Copy(io.Discard, k)
	go io.Copy(io.Discard, kraw)
	for i := 0; i < 6; i++ {
		kraw.Write(rawFrame(0x82, []byte{1}, true, 0)) // data keeps lastRx fresh
		time.Sleep(100 * time.Millisecond)
	}
	select {
	case <-k.done:
		t.Fatal("conn closed despite traffic")
	default:
	}
}

func TestWSPingerSendsPings(t *testing.T) {
	_, raw := serverOver(t, WSConfig{PingInterval: 20 * time.Millisecond})
	if b0, _ := readRawFrame(t, raw); b0 != 0x89 {
		t.Fatalf("frame %x", b0)
	}
}

func TestWSPongAfterExpiredDataDeadline(t *testing.T) {
	s, raw := serverOver(t, WSConfig{})
	go s.Read(make([]byte, 4))
	s.SetWriteDeadline(time.Now().Add(10 * time.Millisecond))
	if _, err := s.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	readRawFrame(t, raw)
	time.Sleep(50 * time.Millisecond)
	for i := 0; i < 3; i++ {
		raw.Write(rawFrame(0x89, nil, true, 0))
		if b0, _ := readRawFrame(t, raw); b0 != 0x8A {
			t.Fatalf("frame %x", b0)
		}
	}
	if _, err := s.Write([]byte("y")); !isTimeout(err) {
		t.Fatalf("expired deadline write: %v", err)
	}
	s.SetWriteDeadline(time.Now().Add(time.Second))
	if _, err := s.Write([]byte("z")); err != nil {
		t.Fatalf("later write: %v", err)
	}
}

func TestWSDataWriteDeadline(t *testing.T) {
	a, b := tcpPair(t)
	_ = b // never read: the window fills
	s := NewWSConn(a, nil, WSServer, WSConfig{})
	s.SetWriteDeadline(time.Now().Add(300 * time.Millisecond))
	chunk := make([]byte, WSWriteChunk)
	var err error
	deadline := time.Now().Add(10 * time.Second)
	for err == nil && time.Now().Before(deadline) {
		_, err = s.Write(chunk)
	}
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Fatalf("err = %v, want timeout", err)
	}
	if _, err := s.Write([]byte("x")); !errors.Is(err, ErrWSBroken) {
		t.Fatalf("after timeout: %v", err)
	}

	// An already-expired deadline fails before any byte, leaving the stream usable.
	c, d := tcpPair(t)
	s2 := NewWSConn(c, nil, WSServer, WSConfig{})
	s2.SetWriteDeadline(time.Now().Add(-time.Second))
	if _, err := s2.Write([]byte("x")); !isTimeout(err) {
		t.Fatalf("expired: %v", err)
	}
	s2.SetWriteDeadline(time.Time{})
	if _, err := s2.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if b0, _ := readRawFrame(t, d); b0 != 0x82 {
		t.Fatalf("frame %x", b0)
	}
}

func TestWSCloseWithBlockedWriter(t *testing.T) {
	a, _ := tcpPair(t) // peer never reads
	s := NewWSConn(a, nil, WSServer, WSConfig{})
	werr := make(chan error, 1)
	go func() {
		chunk := make([]byte, WSWriteChunk)
		for {
			if _, err := s.Write(chunk); err != nil {
				werr <- err
				return
			}
		}
	}()
	time.Sleep(500 * time.Millisecond) // writer is parked on a full window
	start := time.Now()
	s.CloseWithCode(WSCloseGoingAway)
	if d := time.Since(start); d > WSControlWriteTimeout+WSCloseTimeout+100*time.Millisecond {
		t.Fatalf("close took %v", d)
	}
	select {
	case err := <-werr:
		if err == nil {
			t.Fatal("writer returned nil")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("writer still parked")
	}
}

func TestWSCloseHandshake(t *testing.T) {
	// Peer's Close is echoed with the same code; Read then reports EOF.
	s, raw := serverOver(t, WSConfig{})
	go func() { raw.Write(rawFrame(0x88, []byte{0x0f, 0xa0}, true, 0)) }() // 4000
	if _, err := s.Read(make([]byte, 4)); err != io.EOF {
		t.Fatalf("read = %v", err)
	}
	expectClose(t, raw, 4000)
	if code, ok := s.PeerCloseCode(); !ok || code != 4000 {
		t.Fatalf("peer code %d %v", code, ok)
	}
	if _, err := s.Write([]byte("x")); err == nil {
		t.Fatal("write after close succeeded")
	}

	// CloseWrite keeps reading.
	w, wraw := serverOver(t, WSConfig{})
	w.CloseWrite()
	expectClose(t, wraw, WSCloseNormal)
	wraw.Write(rawFrame(0x82, []byte("late"), true, 0))
	got := make([]byte, 4)
	if _, err := io.ReadFull(w, got); err != nil || string(got) != "late" {
		t.Fatalf("read after CloseWrite: %q %v", got, err)
	}

	// Close is bounded against a peer that never answers; CloseNow does not wait.
	n, _ := serverOver(t, WSConfig{})
	start := time.Now()
	n.CloseNow(WSCloseNormal)
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("CloseNow waited")
	}
	m, _ := serverOver(t, WSConfig{})
	start = time.Now()
	m.Close()
	if d := time.Since(start); d > WSCloseTimeout+500*time.Millisecond || d < WSCloseTimeout-200*time.Millisecond {
		t.Fatalf("Close took %v, want about %v", d, WSCloseTimeout)
	}
}

func TestWSPingFlood(t *testing.T) {
	for _, tc := range []struct {
		pings int
		bad   bool
	}{{4, false}, {5, true}} {
		s, raw := serverOver(t, WSConfig{})
		errc := make(chan error, 1)
		go func() { _, err := s.Read(make([]byte, 4)); errc <- err }()
		go io.Copy(io.Discard, raw)
		for i := 0; i < tc.pings; i++ {
			raw.Write(rawFrame(0x89, nil, true, 0))
		}
		select {
		case err := <-errc:
			if !tc.bad {
				t.Fatalf("%d pings: %v", tc.pings, err)
			}
			if !errors.Is(err, ErrWSProtocol) {
				t.Fatalf("err %v", err)
			}
		case <-time.After(300 * time.Millisecond):
			if tc.bad {
				t.Fatalf("%d pings did not close", tc.pings)
			}
		}
	}
}

func TestWSEmptyFrameFlood(t *testing.T) {
	s, raw := serverOver(t, WSConfig{})
	for i := 0; i < WSMaxEmptyFramesInARow+1; i++ {
		raw.Write(rawFrame(0x82, nil, true, 0))
	}
	if _, err := s.Read(make([]byte, 4)); !errors.Is(err, ErrWSProtocol) {
		t.Fatalf("17 empty: %v", err)
	}
	expectClose(t, raw, WSCloseProtocol)

	s2, raw2 := serverOver(t, WSConfig{})
	for i := 0; i < WSMaxEmptyFramesInARow; i++ {
		raw2.Write(rawFrame(0x82, nil, true, 0))
	}
	raw2.Write(rawFrame(0x82, []byte("ok"), true, 0))
	got := make([]byte, 2)
	if _, err := io.ReadFull(s2, got); err != nil || string(got) != "ok" {
		t.Fatalf("16 empty then data: %q %v", got, err)
	}
}

func TestWSOnFrameCountsWireBytes(t *testing.T) {
	var mu sync.Mutex
	var seen []int
	s, raw := serverOver(t, WSConfig{OnFrame: func(n int) { mu.Lock(); seen = append(seen, n); mu.Unlock() }})
	go io.Copy(io.Discard, raw)
	data := make([]byte, 200)
	raw.Write(append(append(append(
		rawFrame(0x82, []byte("abc"), true, 0),   // 2+4+3 = 9
		rawFrame(0x89, []byte("p"), true, 0)...), // 2+4+1 = 7
		rawFrame(0x8A, nil, true, 0)...), // 2+4   = 6
		rawFrame(0x82, data, true, 0)...)) // 4+4+200 = 208
	got := make([]byte, 203)
	if _, err := io.ReadFull(s, got); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []int{9, 7, 6, 208}
	if len(seen) != len(want) {
		t.Fatalf("seen %v", seen)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("seen %v, want %v", seen, want)
		}
	}
}

func TestWSDialNonUpgradeAnswer(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		header string
		retry  string
		code   string
		after  time.Duration
	}{
		{401, `{"error":"x","code":"unauthorized"}`, "", "", "unauthorized", 0},
		{404, `{"error":"x","code":"not_found"}`, "", "", "not_found", 0},
		{429, `{"error":"x","code":"rate_limited"}`, "", "7", "rate_limited", 7 * time.Second},
		{503, ``, "offline", "", "offline", 0},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if tc.header != "" {
				w.Header().Set("Lola-Relay-Code", tc.header)
			}
			if tc.retry != "" {
				w.Header().Set("Retry-After", tc.retry)
			}
			w.WriteHeader(tc.status)
			io.WriteString(w, tc.body)
		}))
		req, _ := http.NewRequest("GET", srv.URL+"/x", nil)
		_, err := WSDialer{}.Dial(context.Background(), req)
		srv.Close()
		var he *WSHandshakeError
		if !errors.As(err, &he) || he.Status != tc.status || he.Code != tc.code || he.RetryAfter != tc.after {
			t.Fatalf("%d: %#v", tc.status, err)
		}
		if strings.Contains(he.Error(), tc.code) && tc.code != "" {
			t.Fatalf("error text leaks the code: %s", he.Error())
		}
	}
}

// TestWSDialBoundsTheAnswer: a hub that never finishes its answer (an endless
// header after a 101, or an endless chunked body after a refusal) fails the
// dial after WSMaxHandshakeHead bytes instead of holding it, and growing its
// buffer, until the deadline.
func TestWSDialBoundsTheAnswer(t *testing.T) {
	for name, head := range map[string]string{
		"endless header":       "HTTP/1.1 101 Switching Protocols\r\nX-Pad: ",
		"endless header lines": "HTTP/1.1 101 Switching Protocols\r\n",
		"endless chunked body": "HTTP/1.1 503 Service Unavailable\r\nTransfer-Encoding: chunked\r\n\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			var sent atomic.Int64
			go func() {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				defer c.Close()
				br := bufio.NewReader(c)
				for {
					l, err := br.ReadString('\n')
					if err != nil || l == "\r\n" {
						break
					}
				}
				io.WriteString(c, head)
				pad := strings.Repeat("a", 1024)
				for sent.Load() < 64<<20 {
					var chunk string
					switch name {
					case "endless header":
						chunk = pad
					case "endless header lines":
						chunk = "X-Pad: " + pad + "\r\n"
					default:
						chunk = "400\r\n" + pad + "\r\n"
					}
					n, err := io.WriteString(c, chunk)
					if err != nil {
						return
					}
					sent.Add(int64(n))
				}
			}()
			req, _ := http.NewRequest("GET", "http://"+ln.Addr().String()+"/x", nil)
			start := time.Now()
			_, err = WSDialer{}.Dial(context.Background(), req)
			if err == nil {
				t.Fatal("an endless answer was accepted")
			}
			if time.Since(start) > 5*time.Second {
				t.Fatalf("the dial waited %v for an endless answer", time.Since(start))
			}
			if name != "endless chunked body" && !errors.Is(err, ErrWSHandshake) {
				t.Fatalf("err = %v, want a handshake error", err)
			}
		})
	}
}

func TestWSDialForcesHTTP11ALPN(t *testing.T) {
	var proto atomic.Value
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := AcceptWebSocket(w, r, WSConfig{})
		if err != nil {
			http.Error(w, "no", 400)
			return
		}
		proto.Store(r.TLS.NegotiatedProtocol)
		c.Close()
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	req, _ := http.NewRequest("GET", srv.URL+"/x", nil)
	c, err := WSDialer{TLSConfig: &tls.Config{RootCAs: pool, NextProtos: []string{"h2", "http/1.1"}}}.Dial(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow(WSCloseNormal)
	c.Read(make([]byte, 1)) // wait for the server's close
	if p, ok := proto.Load().(string); !ok || p == "h2" {
		t.Fatalf("negotiated %q", p)
	}
}

func TestWSDialProxyConnect(t *testing.T) {
	srv := wsServer(t, WSConfig{}, func(c *WSConn) { defer c.Close(); io.Copy(c, c) })
	target := strings.TrimPrefix(srv.URL, "http://")
	var connectLine atomic.Value
	pl, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pl.Close()
	go func() {
		for {
			cc, err := pl.Accept()
			if err != nil {
				return
			}
			go func() {
				defer cc.Close()
				br := bufio.NewReader(cc)
				line, _ := br.ReadString('\n')
				connectLine.Store(strings.TrimSpace(line))
				for {
					l, err := br.ReadString('\n')
					if err != nil || l == "\r\n" {
						break
					}
				}
				up, err := net.Dial("tcp", target)
				if err != nil {
					return
				}
				defer up.Close()
				io.WriteString(cc, "HTTP/1.1 200 Connection established\r\n\r\n")
				go io.Copy(up, br)
				io.Copy(cc, up)
			}()
		}
	}()
	pu, _ := url.Parse("http://" + pl.Addr().String())
	c := dial(t, srv, WSDialer{Proxy: func(*http.Request) (*url.URL, error) { return pu, nil }})
	c.Write([]byte("ping"))
	got := make([]byte, 4)
	if _, err := io.ReadFull(c, got); err != nil || string(got) != "ping" {
		t.Fatalf("echo %q %v", got, err)
	}
	if l, _ := connectLine.Load().(string); l != "CONNECT "+target+" HTTP/1.1" {
		t.Fatalf("connect line %q", l)
	}

	// A non-http proxy scheme is refused.
	req, _ := http.NewRequest("GET", srv.URL, nil)
	_, err = WSDialer{Proxy: func(*http.Request) (*url.URL, error) { return &url.URL{Scheme: "socks5", Host: "x:1"}, nil }}.Dial(context.Background(), req)
	if !errors.Is(err, ErrWSHandshake) {
		t.Fatalf("socks proxy: %v", err)
	}
}

func TestWSDialRefusesPlainHTTPToRemoteHost(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://hub.example.com/x", nil)
	if _, err := (WSDialer{}).Dial(context.Background(), req); !errors.Is(err, ErrWSHandshake) {
		t.Fatalf("err = %v", err)
	}
}

// TestWSConnIsNetConn runs the net.Conn contract checks that matter here
// (no x/net/nettest dependency).
func TestWSConnIsNetConn(t *testing.T) {
	srv := wsServer(t, WSConfig{}, func(c *WSConn) { defer c.Close(); io.Copy(c, c) })
	c := dial(t, srv, WSDialer{})
	var nc net.Conn = c
	if nc.LocalAddr() == nil || nc.RemoteAddr() == nil {
		t.Fatal("addresses")
	}
	// Read deadline: a timeout is a net.Error and the conn stays usable.
	c.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	_, err := c.Read(make([]byte, 1))
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Fatalf("read timeout: %v", err)
	}
	c.SetReadDeadline(time.Time{})
	if _, err := c.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 1)
	if _, err := io.ReadFull(c, b); err != nil || b[0] != 'a' {
		t.Fatalf("echo %q %v", b, err)
	}
	// Zero-length reads and writes are no-ops.
	if n, err := c.Read(nil); n != 0 || err != nil {
		t.Fatal("zero read")
	}
	if n, err := c.Write(nil); n != 0 || err != nil {
		t.Fatal("zero write")
	}
	// Close unblocks a pending Read and is idempotent.
	done := make(chan error, 1)
	go func() { _, err := c.Read(make([]byte, 1)); done <- err }()
	time.Sleep(50 * time.Millisecond)
	c.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("read after close returned nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("read not unblocked")
	}
	c.Close()
	if _, err := c.Write([]byte("x")); err == nil {
		t.Fatal("write after close")
	}
}
