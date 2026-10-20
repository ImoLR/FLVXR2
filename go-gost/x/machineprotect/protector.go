package machineprotect

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-gost/x/logsafe"
)

// Options wires the protector to the agent. Zero values use the real system.
type Options struct {
	ProcRoot     string                     // root holding proc/net and proc/sys ("/")
	RunDir       string                     // runtime dir for the contract files ("/run")
	StateDir     string                     // agent config dir for the sysctl state file
	HelperActive func(context.Context) bool // nil: systemctl is-active mmwxc-helper.service
	Ports        func() PortSet             // live business ports of this agent
	CloseWait    func(seconds int64)        // native landing Core hook; 0 = Core default
	Now          func() time.Time
	Logf         func(string) // nil: stdout
	Interval     time.Duration

	guard guard // tests
}

// Protector is the single long-lived protection loop of an agent process. It is
// created once (not per WebSocket connection) and runs until the agent stops.
type Protector struct {
	opts      Options
	counter   *Counter
	owner     *ownerDetector
	tw        *twBuckets
	ports     portMemory
	portsFile *portsFileWriter
	guard     guard
	supported bool
	logs      *logsafe.RoutineLogger

	mu         sync.Mutex
	settings   Settings
	configured bool

	blocking  bool
	wasMmwx   bool
	closeWait int64
	seq       uint64
	snap      atomic.Pointer[Snapshot]
	wake      chan struct{}
	started   atomic.Bool
	runs      atomic.Int32
}

// New builds a protector; call Run once.
func New(opts Options) *Protector {
	if opts.RunDir == "" {
		opts.RunDir = "/run"
	}
	if opts.StateDir == "" {
		opts.StateDir = "/etc/flvxx"
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Interval <= 0 {
		opts.Interval = SampleInterval
	}
	if opts.Ports == nil {
		opts.Ports = func() PortSet { return PortSet{} }
	}
	g := opts.guard
	if g == nil {
		g = newGuard()
	}
	p := &Protector{
		opts:      opts,
		counter:   NewCounter(opts.ProcRoot),
		owner:     newOwnerDetector(opts.RunDir, opts.HelperActive),
		tw:        newTWBuckets(opts.ProcRoot, opts.StateDir),
		portsFile: newPortsFileWriter(opts.RunDir),
		guard:     g,
		logs:      logsafe.NewRoutineLogger(opts.Logf),
		closeWait: -1,
		wake:      make(chan struct{}, 1),
	}
	p.supported = g.Supported()
	return p
}

// Configure applies panel settings (and settings persisted by an earlier push).
// Protection never starts before the first Configure.
func (p *Protector) Configure(s Settings) {
	p.mu.Lock()
	p.settings, p.configured = s, true
	p.mu.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// Settings returns the current settings and whether any were configured.
func (p *Protector) Settings() (Settings, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.settings, p.configured
}

// Snapshot returns the latest sample (nil before the first one).
func (p *Protector) Snapshot() *Snapshot { return p.snap.Load() }

// Run samples every Interval until ctx ends. A second call returns at once, so a
// reconnect can never start another loop.
func (p *Protector) Run(ctx context.Context) {
	if !p.started.CompareAndSwap(false, true) {
		return
	}
	p.runs.Add(1)
	defer p.runs.Add(-1)
	ticker := time.NewTicker(p.opts.Interval)
	defer ticker.Stop()
	for {
		p.tick(ctx)
		select {
		case <-ctx.Done():
			p.shutdown()
			return
		case <-ticker.C:
		case <-p.wake:
		}
	}
}

// shutdown never leaves a drop chain behind a stopped agent. Sets, counter and the
// sysctl stay; the next process reconciles them.
func (p *Protector) shutdown() {
	if p.blocking && p.supported {
		_ = p.guard.Ensure(p.ports.merge(p.opts.Now(), p.opts.Ports()), false)
		p.blocking = false
	}
}

func (p *Protector) logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if p.opts.Logf != nil {
		p.opts.Logf(msg)
		return
	}
	fmt.Println(msg)
}

func (p *Protector) tick(ctx context.Context) {
	now := p.opts.Now()
	conns, connErr := p.counter.Read()
	p.logs.Record("machine protection", "count", errText(connErr))
	own := p.owner.detect(ctx, now)
	ports := p.ports.merge(now, p.opts.Ports())
	p.logs.Record("machine protection", "business-ports", errText(p.portsFile.sync(ports, now)))

	settings, configured := p.Settings()
	var prot Protect
	var closeWait int64
	var problems []string
	if own.mmwx {
		if !p.wasMmwx {
			p.logf("ℹ️ 本机由妙妙屋助手负责连接保护，FLVX 只上报连接数并提供业务端口")
		}
		p.wasMmwx = true
		// Hand over: never keep our own table or sysctl value next to the Helper's.
		p.releaseKernel(&problems)
		prot = own.mirror()
		closeWait = own.closeWait()
	} else {
		if p.wasMmwx {
			p.logf("ℹ️ 未再检测到妙妙屋助手，连接保护改由 FLVX 负责")
		}
		p.wasMmwx = false
		prot = Protect{Owner: OwnerFLVX}
		if configured && settings.Enabled {
			prot.Enabled = true
			prot.MaxTotal = positive(settings.MaxTotal)
			prot.TWBuckets = positive(settings.TWBuckets)
			prot.CloseWaitSeconds = positive(settings.CloseWaitSeconds)
			p.applyCap(settings, conns, connErr, ports, &prot, &problems)
			if settings.TWBuckets > 0 {
				if err := p.tw.apply(settings.TWBuckets); err != nil {
					problems = append(problems, "TIME_WAIT 上限设置失败: "+err.Error())
				}
			} else if err := p.tw.restore(); err != nil {
				problems = append(problems, "TIME_WAIT 原值恢复失败: "+err.Error())
			}
			closeWait = settings.CloseWaitSeconds
		} else {
			p.releaseKernel(&problems)
		}
	}
	if p.opts.CloseWait != nil && closeWait != p.closeWait {
		p.opts.CloseWait(closeWait)
		p.closeWait = closeWait
	}
	if connErr != nil {
		problems = append(problems, "整机连接数读取失败")
	}
	prot.Error = strings.Join(problems, "；")
	p.logs.Record("machine protection", "apply", prot.Error)

	p.seq++
	snap := &Snapshot{Seq: p.seq, At: now, Protect: prot}
	if connErr == nil {
		c := conns
		snap.Conns = &c
	}
	p.snap.Store(snap)
}

// applyCap runs the hysteresis and installs or updates the nft guard.
func (p *Protector) applyCap(s Settings, conns Conns, connErr error, ports PortSet, prot *Protect, problems *[]string) {
	if s.MaxTotal <= 0 || ports.Len() == 0 {
		// No cap, or nothing of ours to protect: no table at all.
		p.removeGuard(problems)
		return
	}
	if !p.supported {
		*problems = append(*problems, "nftables 不可用，整机连接上限未生效")
		p.blocking = false
		return
	}
	want := false
	if connErr == nil {
		want = NextBlocking(p.blocking, conns.Total, s.MaxTotal)
	}
	if err := p.guard.Ensure(ports, want); err != nil {
		*problems = append(*problems, "防护规则更新失败: "+err.Error())
	}
	// The kernel state wins: a freshly built table starts idle and blocks from
	// the next sample on (conntrack first learns the existing flows).
	want = p.guard.Blocking()
	if want != p.blocking {
		if want {
			p.logf("🛡️ 整机连接 %d ≥ 上限 %d：开始丢弃业务端口的新连接（低于 %d 自动解除）", conns.Total, s.MaxTotal, ReleaseLevel(s.MaxTotal))
		} else {
			p.logf("✅ 整机连接 %d 已低于 %d：解除新连接保护", conns.Total, ReleaseLevel(s.MaxTotal))
		}
	}
	p.blocking = want
	prot.Blocking = want
	prot.Ports = ports.Len()
	dropped, err := p.guard.Dropped()
	if err != nil {
		*problems = append(*problems, "丢弃计数读取失败: "+err.Error())
	}
	prot.DroppedTotal = dropped
}

func (p *Protector) removeGuard(problems *[]string) {
	if p.blocking {
		p.logf("✅ 已解除新连接保护")
	}
	p.blocking = false
	if !p.supported {
		return
	}
	if err := p.guard.Remove(); err != nil {
		*problems = append(*problems, "防护规则删除失败: "+err.Error())
	}
}

// releaseKernel removes everything FLVX changed in the kernel.
func (p *Protector) releaseKernel(problems *[]string) {
	p.removeGuard(problems)
	if err := p.tw.restore(); err != nil {
		*problems = append(*problems, "TIME_WAIT 原值恢复失败: "+err.Error())
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
