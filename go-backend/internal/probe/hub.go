package probe

import (
	"bytes"
	"compress/gzip"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Frame is one built snapshot: marshalled once, gzipped once, and the same
// byte slices go to every viewer (GET cache and every WebSocket).
type Frame struct {
	Seq  uint64
	At   time.Time
	Raw  []byte
	Gzip []byte
	ETag string
}

type HubConfig struct {
	Interval time.Duration // one build per interval at most (2 s)
	MaxPerIP int           // live streams per client IP key
	MaxTotal int           // live streams in total
	Queue    int           // frames queued per stream before it counts as slow
	// Build returns the JSON bytes of the current snapshot.
	Build func(now time.Time) ([]byte, error)
	// Enabled is the public switch; off → every stream is closed.
	Enabled func() bool
	Now     func() time.Time
}

var (
	ErrTotal = errors.New("probe: too many live viewers")
	ErrPerIP = errors.New("probe: too many live viewers from this address")
)

// Kick reasons (Client.Reason).
const (
	KickOff  = "off"
	KickSlow = "slow"
	KickLeft = "left"
)

// Client is one live stream. The hub only enqueues frames (never blocks); the
// caller's writer goroutine drains Send and stops when Done is closed.
type Client struct {
	IP   string
	Gzip bool
	Send chan *Frame
	Done chan struct{}

	once   sync.Once
	reason atomic.Value
}

func (c *Client) kick(reason string) {
	c.once.Do(func() {
		c.reason.Store(reason)
		close(c.Done)
	})
}

// Reason tells why Done was closed ("" while open).
func (c *Client) Reason() string {
	if v, ok := c.reason.Load().(string); ok {
		return v
	}
	return ""
}

type HubStats struct {
	Builds  int64
	Sent    int64
	Slow    int64
	Clients int
}

// Hub builds snapshots only while somebody looks: the stream loop runs while
// at least one stream is open, a GET builds on demand when the cached frame is
// older than Interval. Nothing runs on an idle panel.
type Hub struct {
	cfg   HubConfig
	epoch string

	buildMu sync.Mutex

	mu      sync.Mutex
	cur     *Frame
	seq     uint64
	clients map[*Client]struct{}
	byIP    map[string]int
	loopOn  bool

	builds atomic.Int64
	sent   atomic.Int64
	slow   atomic.Int64
}

func NewHub(cfg HubConfig) *Hub {
	if cfg.Interval <= 0 {
		cfg.Interval = 2 * time.Second
	}
	if cfg.MaxPerIP <= 0 {
		cfg.MaxPerIP = 5
	}
	if cfg.MaxTotal <= 0 {
		cfg.MaxTotal = 200
	}
	if cfg.Queue <= 0 {
		cfg.Queue = 4
	}
	return &Hub{
		cfg:     cfg,
		epoch:   strconv.FormatInt(time.Now().UnixNano()%1_000_000_007, 36),
		clients: make(map[*Client]struct{}),
		byIP:    make(map[string]int),
	}
}

func (h *Hub) now() time.Time {
	if h.cfg.Now != nil {
		return h.cfg.Now()
	}
	return time.Now()
}

func (h *Hub) fresh(f *Frame, now time.Time) bool {
	return f != nil && now.Sub(f.At) < h.cfg.Interval
}

// Frame returns the current frame, building a new one (single flight) when the
// cached one is older than Interval.
func (h *Hub) Frame() (*Frame, error) {
	h.mu.Lock()
	cur := h.cur
	h.mu.Unlock()
	if h.fresh(cur, h.now()) {
		return cur, nil
	}
	h.buildMu.Lock()
	defer h.buildMu.Unlock()
	now := h.now()
	h.mu.Lock()
	cur = h.cur
	h.mu.Unlock()
	if h.fresh(cur, now) {
		return cur, nil
	}
	raw, err := h.cfg.Build(now)
	if err != nil {
		return nil, err
	}
	h.builds.Add(1)
	gz := gzipBytes(raw)
	h.mu.Lock()
	h.seq++
	f := &Frame{Seq: h.seq, At: now, Raw: raw, Gzip: gz, ETag: `"p` + h.epoch + "-" + strconv.FormatUint(h.seq, 36) + `"`}
	h.cur = f
	h.mu.Unlock()
	return f, nil
}

// Invalidate drops the cached frame (settings changed).
func (h *Hub) Invalidate() {
	h.mu.Lock()
	h.cur = nil
	h.mu.Unlock()
}

func gzipBytes(raw []byte) []byte {
	var buf bytes.Buffer
	buf.Grow(len(raw)/4 + 64)
	zw, _ := gzip.NewWriterLevel(&buf, gzip.DefaultCompression)
	_, _ = zw.Write(raw)
	_ = zw.Close()
	return buf.Bytes()
}

// Join reserves a live stream slot.
func (h *Hub) Join(ip string, gz bool) (*Client, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.clients) >= h.cfg.MaxTotal {
		return nil, ErrTotal
	}
	if h.byIP[ip] >= h.cfg.MaxPerIP {
		return nil, ErrPerIP
	}
	c := &Client{IP: ip, Gzip: gz, Send: make(chan *Frame, h.cfg.Queue), Done: make(chan struct{})}
	h.clients[c] = struct{}{}
	h.byIP[ip]++
	if !h.loopOn {
		h.loopOn = true
		go h.loop()
	}
	return c, nil
}

// Leave frees the slot (idempotent).
func (h *Hub) Leave(c *Client) {
	h.mu.Lock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		if h.byIP[c.IP]--; h.byIP[c.IP] <= 0 {
			delete(h.byIP, c.IP)
		}
	}
	h.mu.Unlock()
	c.kick(KickLeft)
}

// Offer queues f for c without blocking; a full queue drops the client.
func (h *Hub) Offer(c *Client, f *Frame) {
	select {
	case <-c.Done:
		return
	default:
	}
	select {
	case c.Send <- f:
		h.sent.Add(1)
	default:
		h.slow.Add(1)
		c.kick(KickSlow)
	}
}

func (h *Hub) loop() {
	t := time.NewTicker(h.cfg.Interval)
	defer t.Stop()
	for {
		<-t.C
		h.mu.Lock()
		if len(h.clients) == 0 {
			h.loopOn = false
			h.mu.Unlock()
			return
		}
		list := make([]*Client, 0, len(h.clients))
		for c := range h.clients {
			list = append(list, c)
		}
		h.mu.Unlock()
		if h.cfg.Enabled != nil && !h.cfg.Enabled() {
			for _, c := range list {
				c.kick(KickOff)
			}
			continue
		}
		f, err := h.Frame()
		if err != nil {
			continue
		}
		for _, c := range list {
			h.Offer(c, f)
		}
	}
}

func (h *Hub) Stats() HubStats {
	h.mu.Lock()
	n := len(h.clients)
	h.mu.Unlock()
	return HubStats{Builds: h.builds.Load(), Sent: h.sent.Load(), Slow: h.slow.Load(), Clients: n}
}

// Running reports whether the stream loop is alive (tests: idle = no loop).
func (h *Hub) Running() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.loopOn
}
