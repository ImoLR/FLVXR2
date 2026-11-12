package tls

import (
	"context"
	"crypto/tls"
	"net"
	"sync"
	"time"

	"github.com/go-gost/core/limiter"
	"github.com/go-gost/core/listener"
	"github.com/go-gost/core/logger"
	md "github.com/go-gost/core/metadata"
	admission "github.com/go-gost/x/admission/wrapper"
	xnet "github.com/go-gost/x/internal/net"
	"github.com/go-gost/x/internal/net/proxyproto"
	climiter "github.com/go-gost/x/limiter/conn/wrapper"
	limiter_wrapper "github.com/go-gost/x/limiter/traffic/wrapper"
	metrics "github.com/go-gost/x/metrics/wrapper"
	stats "github.com/go-gost/x/observer/stats/wrapper"
	"github.com/go-gost/x/registry"
)

func init() {
	registry.ListenerRegistry().Register("tls", NewListener)
}

type tlsListener struct {
	ln      net.Listener
	logger  logger.Logger
	md      metadata
	options listener.Options
}

func NewListener(opts ...listener.Option) listener.Listener {
	options := listener.Options{}
	for _, opt := range opts {
		opt(&options)
	}
	return &tlsListener{
		logger:  options.Logger,
		options: options,
	}
}

func (l *tlsListener) Init(md md.Metadata) (err error) {
	if err = l.parseMetadata(md); err != nil {
		return
	}

	network := "tcp"
	if xnet.IsIPv4(l.options.Addr) {
		network = "tcp4"
	}

	lc := net.ListenConfig{}
	if l.md.mptcp {
		lc.SetMultipathTCP(true)
		l.logger.Debugf("mptcp enabled: %v", lc.MultipathTCP())
	}
	ln, err := lc.Listen(context.Background(), network, l.options.Addr)
	if err != nil {
		return
	}
	ln = proxyproto.WrapListener(l.options.ProxyProtocol, ln, 10*time.Second)
	ln = metrics.WrapListener(l.options.Service, ln)
	ln = stats.WrapListener(ln, l.options.Stats)
	ln = admission.WrapListener(l.options.Admission, ln)
	ln = limiter_wrapper.WrapListener(l.options.Service, ln, l.options.TrafficLimiter)
	ln = climiter.WrapListener(l.options.ConnLimiter, ln)

	l.ln = newSilentTLSListener(ln, l.options.TLSConfig, l.logger)

	return
}

func (l *tlsListener) Accept() (conn net.Conn, err error) {
	conn, err = l.ln.Accept()
	if err != nil {
		return
	}

	conn = limiter_wrapper.WrapConn(
		conn,
		l.options.TrafficLimiter,
		conn.RemoteAddr().String(),
		limiter.ScopeOption(limiter.ScopeConn),
		limiter.ServiceOption(l.options.Service),
		limiter.NetworkOption(conn.LocalAddr().Network()),
		limiter.SrcOption(conn.RemoteAddr().String()),
	)

	return
}

func (l *tlsListener) Addr() net.Addr {
	return l.ln.Addr()
}

func (l *tlsListener) Close() error {
	return l.ln.Close()
}

const (
	silentHandshakeTimeout = 10 * time.Second
	// Plan 245: handshakes in flight per listener. At the cap the accept loop
	// waits (kernel backlog), so a listener never holds more than this many
	// unfinished or undelivered conns.
	silentMaxHandshakes = 64
)

type acceptResult struct {
	conn net.Conn
	err  error
}

// silentTLSListener 是一个自定义的TLS监听器，对非TLS连接静默处理
//
// Plan 245: the handshake runs in its own goroutine per connection, not
// inside Accept — one client that never sends a ClientHello (a port scan)
// used to block every new tunnel connection of this port for up to 10 s,
// and concurrent connections queued behind each other's handshake RTT.
type silentTLSListener struct {
	net.Listener
	config *tls.Config
	logger logger.Logger

	results   chan acceptResult // unbuffered: finished conns are handed over one by one
	sem       chan struct{}     // handshakes in flight (+ waiting for hand-over)
	done      chan struct{}     // closed by Close
	dead      chan struct{}     // closed when the inner listener failed for good
	deadErr   error
	closeOnce sync.Once
}

func newSilentTLSListener(inner net.Listener, config *tls.Config, logger logger.Logger) net.Listener {
	l := &silentTLSListener{
		Listener: inner,
		config:   config,
		logger:   logger,
		results:  make(chan acceptResult),
		sem:      make(chan struct{}, silentMaxHandshakes),
		done:     make(chan struct{}),
		dead:     make(chan struct{}),
	}
	go l.acceptLoop()
	return l
}

func (l *silentTLSListener) acceptLoop() {
	for {
		select {
		case l.sem <- struct{}{}:
		case <-l.done:
			return
		}
		conn, err := l.Listener.Accept()
		if err != nil {
			<-l.sem
			select {
			case <-l.done:
				return
			default:
			}
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				// The service loop backs off on a temporary error and
				// calls Accept again: hand it over, then keep going.
				select {
				case l.results <- acceptResult{err: err}:
					continue
				case <-l.done:
					return
				}
			}
			l.deadErr = err
			close(l.dead)
			return
		}
		go l.handshake(conn)
	}
}

func (l *silentTLSListener) handshake(conn net.Conn) {
	defer func() { <-l.sem }()

	// 创建TLS连接
	tlsConn := tls.Server(conn, l.config)

	// 设置握手超时
	tlsConn.SetDeadline(time.Now().Add(silentHandshakeTimeout))

	// 尝试TLS握手（失败静默关闭）
	if err := tlsConn.Handshake(); err != nil {
		conn.Close()
		return
	}

	// 清除超时设置
	tlsConn.SetDeadline(time.Time{})

	select {
	case l.results <- acceptResult{conn: tlsConn}:
	case <-l.done:
		tlsConn.Close()
	}
}

func (l *silentTLSListener) Accept() (net.Conn, error) {
	select {
	case r := <-l.results:
		return r.conn, r.err
	case <-l.done:
		return nil, net.ErrClosed
	case <-l.dead:
		// A conn finished just before the inner listener died still goes out.
		select {
		case r := <-l.results:
			return r.conn, r.err
		default:
		}
		return nil, l.deadErr
	}
}

func (l *silentTLSListener) Close() error {
	l.closeOnce.Do(func() { close(l.done) })
	return l.Listener.Close()
}
