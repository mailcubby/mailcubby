package smtpd

import (
	"crypto/tls"
	"errors"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/emersion/go-smtp"
)

const (
	tooManyConnsReply   = "421 4.7.0 Too many connections\r\n"
	rejectWriteDeadline = 2 * time.Second
	minAcceptRetryDelay = 5 * time.Millisecond
	maxAcceptRetryDelay = time.Second
)

type connLimits struct {
	mu       sync.Mutex
	maxTotal int
	maxPerIP int
	total    int
	perIP    map[netip.Addr]int
}

func newConnLimits(maxTotal, maxPerIP int) *connLimits {
	return &connLimits{
		maxTotal: maxTotal,
		maxPerIP: maxPerIP,
		perIP:    make(map[netip.Addr]int),
	}
}

func (c *connLimits) tryAcquire(ip netip.Addr, hasIP bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.maxTotal > 0 && c.total >= c.maxTotal {
		return false
	}
	if hasIP && c.maxPerIP > 0 && c.perIP[ip] >= c.maxPerIP {
		return false
	}
	c.total++
	if hasIP {
		c.perIP[ip]++
	}
	return true
}

func (c *connLimits) release(ip netip.Addr, hasIP bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.total > 0 {
		c.total--
	}
	if hasIP {
		if n := c.perIP[ip]; n <= 1 {
			delete(c.perIP, ip)
		} else {
			c.perIP[ip] = n - 1
		}
	}
}

func (c *connLimits) active() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total
}

// LimitListener wraps a net.Listener to enforce total and per-IP concurrent
// connection limits (SPEC FR-005, ARCHITECTURE 3.3). Excess connections receive
// "421 4.7.0 Too many connections" (when writeRejectReply is true) and are
// closed asynchronously without stalling the accept loop. It also tracks
// admitted connections so shutdown can close idle ones immediately and
// in-flight DATA ones once their reply is written (ARCHITECTURE 3.1).
type LimitListener struct {
	inner            net.Listener
	limits           *connLimits
	writeRejectReply bool

	admitCh chan net.Conn
	errCh   chan error
	done    chan struct{}

	mu       sync.Mutex
	closed   bool
	draining bool
	conns    map[*trackedConn]struct{}
	admitWG  sync.WaitGroup
	loopWG   sync.WaitGroup
}

// NewLimitListener starts accepting connections from inner and admits up to
// maxTotal concurrent connections and maxPerIP per client IP.
func NewLimitListener(inner net.Listener, maxTotal, maxPerIP int) *LimitListener {
	return newSharedLimitListener(inner, newConnLimits(maxTotal, maxPerIP), true)
}

func newSharedLimitListener(inner net.Listener, limits *connLimits, writeRejectReply bool) *LimitListener {
	bufSize := 16
	if limits != nil && limits.maxTotal > bufSize {
		bufSize = limits.maxTotal
	}
	l := &LimitListener{
		inner:            inner,
		limits:           limits,
		writeRejectReply: writeRejectReply,
		admitCh:          make(chan net.Conn, bufSize),
		errCh:            make(chan error, 1),
		done:             make(chan struct{}),
		conns:            make(map[*trackedConn]struct{}),
	}
	l.loopWG.Add(1)
	go l.acceptLoop()
	return l
}

// Accept returns the next admitted connection.
func (l *LimitListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.admitCh:
		return c, nil
	case err := <-l.errCh:
		return nil, err
	case <-l.done:
		return nil, net.ErrClosed
	}
}

// Close closes the underlying listener and waits for the accept and admission
// goroutines to finish.
func (l *LimitListener) Close() error {
	l.mu.Lock()
	already := l.closed
	l.closed = true
	l.mu.Unlock()

	if already {
		return nil
	}

	close(l.done)
	err := l.inner.Close()
	l.loopWG.Wait()
	l.admitWG.Wait()

	// Close any admitted connections still buffered in admitCh.
	for {
		select {
		case c := <-l.admitCh:
			_ = c.Close()
		default:
			return err
		}
	}
}

// Addr returns the underlying listener's network address.
func (l *LimitListener) Addr() net.Addr {
	return l.inner.Addr()
}

// ActiveConnections returns the current number of active admitted connections.
func (l *LimitListener) ActiveConnections() int {
	return l.limits.active()
}

// acceptLoop runs until l.done is closed or the inner listener returns a fatal
// error. Temporary accept errors (for example EMFILE) are retried with capped
// exponential backoff, like net/http and go-smtp, so a short spike never stops
// the SMTP listener (ARCHITECTURE 3.3).
func (l *LimitListener) acceptLoop() {
	defer l.loopWG.Done()

	var tempDelay time.Duration
	for {
		conn, err := l.inner.Accept()
		if err != nil {
			select {
			case <-l.done:
				return
			default:
			}
			var ne net.Error
			//nolint:staticcheck // SA1019: same retry rule as net/http and go-smtp Serve.
			if errors.As(err, &ne) && ne.Temporary() {
				if tempDelay == 0 {
					tempDelay = minAcceptRetryDelay
				} else {
					tempDelay = min(2*tempDelay, maxAcceptRetryDelay)
				}
				t := time.NewTimer(tempDelay)
				select {
				case <-t.C:
				case <-l.done:
					t.Stop()
					return
				}
				continue
			}
			// Forward fatal accept errors to callers blocked in Accept.
			if !errors.Is(err, net.ErrClosed) {
				select {
				case l.errCh <- err:
				default:
				}
			}
			return
		}
		tempDelay = 0

		// Admit each raw connection in a short goroutine so a slow peer during
		// 421 rejection or channel delivery never stalls accepting new connections.
		l.admitWG.Add(1)
		go l.admitConn(conn)
	}
}

func (l *LimitListener) admitConn(conn net.Conn) {
	defer l.admitWG.Done()

	ip, hasIP := extractRemoteIP(conn.RemoteAddr())

	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		_ = conn.Close()
		return
	}
	if !l.limits.tryAcquire(ip, hasIP) {
		l.mu.Unlock()
		if l.writeRejectReply {
			_ = conn.SetWriteDeadline(time.Now().Add(rejectWriteDeadline))
			_, _ = conn.Write([]byte(tooManyConnsReply))
		}
		_ = conn.Close()
		return
	}
	tc := &trackedConn{Conn: conn, l: l}
	tc.onClose = func() {
		l.release(tc, ip, hasIP)
	}
	l.conns[tc] = struct{}{}
	l.mu.Unlock()

	select {
	case l.admitCh <- tc:
	case <-l.done:
		_ = tc.Close()
	}
}

func (l *LimitListener) release(tc *trackedConn, ip netip.Addr, hasIP bool) {
	l.mu.Lock()
	delete(l.conns, tc)
	l.mu.Unlock()
	l.limits.release(ip, hasIP)
}

// closeIdle switches the listener to draining and closes every admitted
// connection that is not inside a DATA transaction. Busy connections are
// closed by endTransaction once their reply has been written.
func (l *LimitListener) closeIdle() {
	l.closeConns(false)
}

// closeAll switches the listener to draining and closes every admitted
// connection, aborting in-flight DATA transactions.
func (l *LimitListener) closeAll() {
	l.closeConns(true)
}

func (l *LimitListener) closeConns(all bool) {
	l.mu.Lock()
	l.draining = true
	toClose := make([]*trackedConn, 0, len(l.conns))
	for c := range l.conns {
		if all || !c.busy {
			toClose = append(toClose, c)
		}
	}
	l.mu.Unlock()

	for _, c := range toClose {
		_ = c.Close()
	}
}

type trackedConn struct {
	net.Conn
	l       *LimitListener
	busy    bool // guarded by l.mu
	once    sync.Once
	onClose func()
}

// trackedFromSMTP returns the trackedConn under an SMTP connection, unwrapping
// STARTTLS, or nil when the connection was not admitted by a LimitListener.
func trackedFromSMTP(c *smtp.Conn) *trackedConn {
	if c == nil {
		return nil
	}
	nc := c.Conn()
	if t, ok := nc.(*tls.Conn); ok {
		nc = t.NetConn()
	}
	tc, _ := nc.(*trackedConn)
	return tc
}

// beginTransaction marks c as inside a DATA transaction so closeIdle leaves it
// open until its reply has been written.
func (c *trackedConn) beginTransaction() {
	c.l.mu.Lock()
	c.busy = true
	c.l.mu.Unlock()
}

// endTransaction clears the DATA mark and closes c if the listener is
// draining. go-smtp calls Session.Reset after writing the DATA reply, so the
// client always receives its reply before the connection is closed.
func (c *trackedConn) endTransaction() {
	c.l.mu.Lock()
	c.busy = false
	drain := c.l.draining
	c.l.mu.Unlock()
	if drain {
		_ = c.Close()
	}
}

func (c *trackedConn) Close() error {
	c.once.Do(c.onClose)
	return c.Conn.Close()
}

func extractRemoteIP(addr net.Addr) (netip.Addr, bool) {
	if addr == nil {
		return netip.Addr{}, false
	}
	if tcp, ok := addr.(*net.TCPAddr); ok && tcp.IP != nil {
		if ip, ok := netip.AddrFromSlice(tcp.IP); ok {
			return ip.Unmap(), true
		}
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return netip.Addr{}, false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return ip.Unmap(), true
}
