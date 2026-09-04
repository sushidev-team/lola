package devforward

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// echoServer is a loopback TCP server that echoes what it is sent, standing in
// for a dev server. Real ones are HTTP, but a forward is a byte pipe and the
// point is that it does not know or care.
func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	return ln.Addr().String()
}

func TestForwardCarriesBytesBothWays(t *testing.T) {
	f, err := Open("127.0.0.1", echoServer(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })

	c, err := net.Dial("tcp", f.Addr)
	if err != nil {
		t.Fatalf("dial the forward: %v", err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("hello\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	got, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != "hello\n" {
		t.Fatalf("got %q, want %q", got, "hello\n")
	}
}

// A page opens several connections at once; a serial accept loop would turn
// that into a queue.
func TestForwardServesConcurrentConnections(t *testing.T) {
	f, err := Open("127.0.0.1", echoServer(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })

	const n = 8
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			c, err := net.Dial("tcp", f.Addr)
			if err != nil {
				errs <- err
				return
			}
			defer c.Close()
			msg := fmt.Sprintf("n=%d\n", i)
			if _, err := c.Write([]byte(msg)); err != nil {
				errs <- err
				return
			}
			_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
			got, err := bufio.NewReader(c).ReadString('\n')
			if err != nil {
				errs <- err
				return
			}
			if got != msg {
				errs <- fmt.Errorf("got %q, want %q", got, msg)
				return
			}
			errs <- nil
		}(i)
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("connection %d: %v", i, err)
		}
	}
}

// A real dev server behind it: the forward must be transparent to HTTP,
// including the response body, because that is the whole feature.
func TestForwardServesHTTP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "host=%s path=%s", r.Host, r.URL.Path)
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	f, err := Open("127.0.0.1", ln.Addr().String())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })

	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + f.Addr + "/app")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	// The Host the server sees is the FORWARD's address, which is what makes a
	// dev server's own host check (vite's allowedHosts) the thing to watch.
	if !strings.Contains(string(body), "path=/app") {
		t.Fatalf("body = %q", body)
	}
	if !strings.Contains(string(body), "host="+f.Addr) {
		t.Errorf("the server should see the forward's Host, got %q", body)
	}
}

// THE RAIL THIS PACKAGE ENFORCES. A target off the loopback is refused: a
// forward reaches exactly one server on this host, and anything else would make
// it a proxy for the network it is bound to.
func TestOpenRefusesANonLoopbackTarget(t *testing.T) {
	for _, target := range []string{
		"192.168.1.10:8000",
		"10.0.0.1:3000",
		"example.com:80", // a NAME is refused rather than resolved
		"[2001:db8::1]:8080",
	} {
		if _, err := Open("127.0.0.1", target); err == nil {
			t.Errorf("%q was accepted", target)
		}
	}
	if _, err := Open("127.0.0.1", "127.0.0.1:0"); err == nil {
		t.Error("port 0 was accepted as a target")
	}
	if _, err := Open("127.0.0.1", "not-host-port"); err == nil {
		t.Error("a malformed target was accepted")
	}
	// ::1 is loopback too, and a dev server that binds it is the common case
	// for vite.
	f, err := Open("127.0.0.1", "[::1]:8080")
	if err != nil {
		t.Fatalf("[::1] must be accepted: %v", err)
	}
	_ = f.Close()
}

func TestOpenNeedsAnAddressToPublishOn(t *testing.T) {
	if _, err := Open("", "127.0.0.1:8000"); err == nil {
		t.Error("an empty bind host was accepted")
	}
}

// Close is idempotent because the daemon closes forwards on session change, on
// dev-tab change and on shutdown, and those overlap.
func TestCloseIsIdempotentAndStopsListening(t *testing.T) {
	f, err := Open("127.0.0.1", echoServer(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	addr := f.Addr
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_ = f.Close()

	if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		_ = c.Close()
		t.Fatal("the forward still accepts connections after Close")
	}
}

// A dev server that has gone away must not hang a browser: the accepted
// connection is closed, which is the same answer a direct connection would get.
func TestAConnectionToADeadTargetIsClosed(t *testing.T) {
	// A port nothing listens on: bind one, then release it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	dead := ln.Addr().String()
	_ = ln.Close()

	f, err := Open("127.0.0.1", dead)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })

	c, err := net.Dial("tcp", f.Addr)
	if err != nil {
		t.Fatalf("dial the forward: %v", err)
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadAll(c); err != nil && !errors.Is(err, io.EOF) {
		// A reset is fine too; what must not happen is a read that blocks.
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			t.Fatal("a connection to a dead dev server hung instead of closing")
		}
	}
}

// A dev server on IPv6 loopback: vite binds [::1] and php binds 127.0.0.1, so a
// forward that collapsed "localhost" to one family answered "connection reset"
// for half of them — which is exactly what a phone saw.
func TestForwardReachesAnIPv6OnlyServer(t *testing.T) {
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skip("no IPv6 loopback on this machine")
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()

	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	// Dialed by NAME, which is what makes it dual-stack.
	f, err := Open("127.0.0.1", net.JoinHostPort("localhost", port))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })

	c, err := net.Dial("tcp", f.Addr)
	if err != nil {
		t.Fatalf("dial the forward: %v", err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("v6\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	got, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != "v6\n" {
		t.Fatalf("got %q", got)
	}
}

// The name is accepted at Open; the guarantee is enforced on the CONNECTION, so
// a "localhost" that resolved off-box carries no bytes.
func TestARemoteAnswerIsDroppedEvenForAnAcceptedName(t *testing.T) {
	if _, err := Open("127.0.0.1", "localhost:8000"); err != nil {
		t.Fatalf("localhost must be accepted at Open: %v", err)
	}
	// The guard itself, against a peer that is not loopback.
	srv, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	host, err := nonLoopbackHost()
	if err != nil {
		t.Skip("no non-loopback address on this machine")
	}
	_, port, _ := net.SplitHostPort(srv.Addr().String())
	c, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 2*time.Second)
	if err != nil {
		t.Skipf("cannot reach this machine's own LAN address: %v", err)
	}
	defer c.Close()
	if remoteIsLoopback(c) {
		t.Fatal("a non-loopback peer was reported as loopback")
	}
}

func nonLoopbackHost() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLoopback() || ipn.IP.To4() == nil {
			continue
		}
		return ipn.IP.String(), nil
	}
	return "", errors.New("none")
}
