package relay

import (
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sushidev-team/lola/hubproto"
)

// shapeSlice bounds one shaping sleep so a close interrupts it promptly.
const shapeSlice = 100 * time.Millisecond

var bufPool = sync.Pool{New: func() any { b := make([]byte, 32<<10); return &b }}

// splice joins a phone leg and a daemon leg. It moves bytes and counts them;
// what they mean (inner TLS) is none of its business.
type splice struct {
	h      *Hub
	p      *Pending
	phone  *hubproto.WSConn
	daemon *hubproto.WSConn
	start  time.Time

	lastData atomic.Int64 // unix nanos (Hub.Now) of the last payload either way
	stop     chan struct{}
	closed   chan struct{} // closed once both legs are closed
	once     sync.Once
	code     atomic.Uint32
}

// Splice runs the splice in the calling goroutine until both legs ended. p
// must have been Taken; phone is the already upgraded phone leg. Every
// outcome releases the reservation exactly once.
func (h *Hub) Splice(p *Pending, phone *hubproto.WSConn) {
	h.mu.Lock()
	daemon := p.daemon
	h.mu.Unlock()
	if daemon == nil {
		_ = phone.CloseNow(hubproto.RelayClosePeerGone)
		h.mu.Lock()
		delete(h.pending, p.Conn)
		h.mu.Unlock()
		p.doRelease()
		return
	}
	now := h.Now()
	s := &splice{h: h, p: p, phone: phone, daemon: daemon, start: now,
		stop: make(chan struct{}), closed: make(chan struct{})}
	s.lastData.Store(now.UnixNano())

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		s.shutdown(hubproto.WSCloseGoingAway, true)
		<-s.closed
		s.finish()
		return
	}
	h.splices[s] = struct{}{}
	h.mu.Unlock()

	done := make(chan struct{}, 2)
	up := &shapedReader{s: s, src: phone, dir: dirUp}
	down := &shapedReader{s: s, src: daemon, dir: dirDown}
	go func() { s.copyHalf(daemon, phone, up); done <- struct{}{} }()
	go func() { s.copyHalf(phone, daemon, down); done <- struct{}{} }()
	go s.idleWatch()

	var grace <-chan time.Time
	quota := p.quotaCh // nil once fired: a closed channel would spin the select
	for ended := 0; ended < 2; {
		select {
		case <-done:
			ended++
			if ended == 1 {
				t := time.NewTimer(h.lim.halfClose)
				defer t.Stop()
				grace = t.C
			}
		case <-grace:
			grace = nil
			s.shutdown(hubproto.RelayClosePeerGone, false)
		case <-quota:
			quota = nil
			s.shutdown(hubproto.RelayCloseQuota, false)
		}
	}
	s.shutdown(hubproto.WSCloseNormal, false)
	<-s.closed
	s.finish()
}

func (s *splice) finish() {
	h := s.h
	h.mu.Lock()
	delete(h.splices, s)
	delete(h.pending, s.p.Conn)
	h.mu.Unlock()
	h.acct.Lock()
	up, down := s.p.bytes[dirUp], s.p.bytes[dirDown]
	h.acct.Unlock()
	h.logf("relay: device=%s org=%s conn=%s dur=%s up=%d down=%d end=%d",
		short(s.p.Device), s.p.Org, short(s.p.Conn),
		h.Now().Sub(s.start).Round(time.Second), up, down, s.code.Load())
	s.p.doRelease()
}

// shutdown ends the splice once, with the first code given: both legs get the
// Close (bounded) and every blocked read and shaping sleep unblocks. now skips
// the wait for the peers' Close (process shutdown).
func (s *splice) shutdown(code uint16, now bool) {
	s.once.Do(func() {
		s.code.Store(uint32(code))
		close(s.stop)
		go func() {
			defer close(s.closed)
			var wg sync.WaitGroup
			for _, ws := range []*hubproto.WSConn{s.phone, s.daemon} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if now {
						_ = ws.CloseNow(code)
					} else {
						_ = ws.CloseWithCode(code)
					}
				}()
			}
			wg.Wait()
		}()
	})
}

// idleWatch closes the splice once no payload moved either way for the idle
// timeout. Pings, pongs and empty frames are not payload.
func (s *splice) idleWatch() {
	for {
		select {
		case <-s.stop:
			return
		case <-s.h.idleTick():
			last := time.Unix(0, s.lastData.Load())
			if s.h.Now().Sub(last) >= s.h.lim.idle {
				s.shutdown(hubproto.RelayCloseIdle, false)
				return
			}
		}
	}
}

// shapedReader delays reads while the direction's rate debt exceeds its burst.
// Shaping slows a splice, it never closes it.
type shapedReader struct {
	s   *splice
	src io.Reader
	dir int
}

func (r *shapedReader) Read(p []byte) (int, error) {
	for {
		w := r.s.h.shapeWait(r.s.p, r.dir)
		if w <= 0 {
			break
		}
		if !r.s.h.sleep(min(w, shapeSlice), r.s.stop) {
			return 0, hubproto.ErrWSClosed
		}
	}
	return r.src.Read(p)
}

// copyHalf moves payload src -> dstWS. A peer that keeps sending but stops
// reading stalls the write for at most the write-stall bound, then the splice
// ends 1008.
func (s *splice) copyHalf(dstWS, srcWS *hubproto.WSConn, src io.Reader) {
	bp := bufPool.Get().(*[]byte)
	defer bufPool.Put(bp)
	buf := *bp
	for {
		n, err := src.Read(buf)
		if n > 0 {
			s.lastData.Store(s.h.Now().UnixNano())
			// A real-time deadline: it is the socket's, not the fake clock's.
			_ = dstWS.SetWriteDeadline(time.Now().Add(s.h.lim.writeStall))
			if _, werr := dstWS.Write(buf[:n]); werr != nil {
				if errors.Is(werr, os.ErrDeadlineExceeded) || isTimeout(werr) {
					s.shutdown(hubproto.WSClosePolicy, false)
				} else {
					s.shutdown(hubproto.RelayClosePeerGone, false)
				}
				return
			}
		}
		if err != nil {
			s.sourceEnded(dstWS, srcWS, err)
			return
		}
	}
}

// sourceEnded forwards the end of one direction. A peer's normal Close becomes
// a Close toward the other side (which stays readable for the grace period); a
// 1001 is forwarded as such; anything else reads as the peer being gone.
func (s *splice) sourceEnded(dstWS, srcWS *hubproto.WSConn, err error) {
	select {
	case <-s.stop:
		return
	default:
	}
	if errors.Is(err, io.EOF) {
		code, _ := srcWS.PeerCloseCode()
		switch code {
		case 0, hubproto.WSCloseNormal, 1005:
			_ = dstWS.CloseWrite()
			return
		case hubproto.WSCloseGoingAway:
			_ = dstWS.CloseWithCode(hubproto.WSCloseGoingAway)
			return
		}
	}
	_ = dstWS.CloseWithCode(hubproto.RelayClosePeerGone)
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
