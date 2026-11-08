// Package controller は制御プロセスの中核。GUIを持たず、設定、MediaMTXとffmpegの監督、
// 統計の取得と累計、プレイヤーとの通信を束ねる。GUIはこのパッケージを呼ぶだけの薄い層。
package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"srtrx/internal/config"
	"srtrx/internal/ffmpeg"
	"srtrx/internal/ipc"
	"srtrx/internal/logx"
	"srtrx/internal/mediamtx"
	"srtrx/internal/metrics"
	"srtrx/internal/proc"
	"srtrx/internal/supervisor"
)

// ErrLocked は本番ロック中に設定を変えようとした。
var ErrLocked = errors.New("本番ロック中は設定を変更できない")

const (
	heartbeatLost  = 3 * time.Second  // ハートビートがこれだけ来なければ「プレイヤー応答なし」
	diagInterval   = 10 * time.Second // 自己診断の再試行
	maxFaults      = 100
	statePushEvery = time.Second
)

// Options は起動時の指定。
type Options struct {
	Paths  config.Paths
	Logs   *logx.Logs
	FFmpeg string // ffmpeg の実行ファイル(空なら $SRTRX_FFMPEG、それも空なら PATH の ffmpeg)
	Clock  func() time.Time
}

// Player はプレイヤーからの最新の報告。
type Player struct {
	Connected   bool // ハートビートが途絶えていない
	PID         int
	FPS         float64
	FrameAgeMS  int64
	FramesTotal uint64
	LastBeat    time.Time
}

// Status はGUIへ見せる状態の写し。
type Status struct {
	FFmpeg          supervisor.Snapshot
	MediaMTX        supervisor.Snapshot
	MediaMTXEnabled bool
	HWFallback      bool
	DiagError       string // 空なら自己診断に合格
	FFmpegVersion   string
	PortInUse       bool
	Player          Player
	Stats           metrics.Stats
	LoadNotes       []string
	Locked          bool
	Config          config.Config
}

// Controller は制御の本体。
type Controller struct {
	opt   Options
	paths config.Paths
	now   func() time.Time

	mu       sync.Mutex
	cfg      config.Config
	loadInfo *config.LoadInfo

	caps      ffmpeg.Caps
	diagErr   error
	diagOK    bool
	mtxBin    string
	portInUse bool

	hw supervisor.HWFallback

	mtxSV *supervisor.Supervisor
	ffSV  *supervisor.Supervisor
	poll  *metrics.Poller

	// プレイヤー
	player Player
	ffBase uint64 // ffmpeg を起動した時点の frames_total(これを超えたら「フレームが来た」)
	// ffBasePending は、ffmpeg の起動後のハートビートをまだ受けていない間 true。
	// 起動時の player.FramesTotal は最大1秒古く、前の ffmpeg がその後に送ったフレームを含まない。
	// それを基準にすると、次のハートビートで「新しい ffmpeg のフレームが来た」と誤り、
	// キーフレームを待っている間に、ウォッチドッグ(3秒)で止めてしまう。
	ffBasePending bool
	lastTotal     uint64    // 直近のハートビートの frames_total
	lastFrame     time.Time // frames_total が最後に増えた時刻(制御の時計)

	// MediaMTX
	mtxLastOK  time.Time
	mtxReadyAt time.Time

	faults []supervisor.Fault

	ipcMu   sync.Mutex
	ipcConn *ipc.Conn
	stats   metrics.Stats

	ffCancel, mtxCancel context.CancelFunc
	ffDone, mtxDone     chan struct{}
	bg                  sync.WaitGroup
}

// New は設定を読み、前回の子プロセスを片付け、監督を組み立てる(まだ動かさない)。
func New(opt Options) (*Controller, error) {
	if opt.Clock == nil {
		opt.Clock = time.Now
	}
	c := &Controller{opt: opt, paths: opt.Paths, now: opt.Clock}
	if err := c.paths.EnsureDirs(); err != nil {
		return nil, fmt.Errorf("ディレクトリを作れない: %w", err)
	}
	c.cfg, c.loadInfo = config.Load(c.paths.ConfigFile)
	for _, n := range c.loadInfo.Notes {
		slog.Warn("設定の読み込み", "note", n)
	}
	// 前回の制御が残した子プロセスを止める(これをしないと、残った MediaMTX がポートを握ったままになる)
	for _, s := range []struct{ pid, name string }{{c.paths.FFmpegPID, "ffmpeg"}, {c.paths.MediaMTXPID, "mediamtx"}} {
		if _, err := proc.CleanupStale(s.pid, s.name); err != nil {
			slog.Warn("前回の子プロセスの片付けに失敗", "name", s.name, "err", err)
		}
	}
	c.lastFrame = c.now()
	c.buildSupervisors()
	return c, nil
}

// Config は現在の設定(コピー)。
func (c *Controller) Config() config.Config {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg.Clone()
}

// Paths は実行時のパス。
func (c *Controller) Paths() config.Paths { return c.paths }

// Faults は障害履歴(新しい順、最大100件)。
func (c *Controller) Faults() []supervisor.Fault {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]supervisor.Fault, len(c.faults))
	for i, f := range c.faults {
		out[len(c.faults)-1-i] = f
	}
	return out
}

func (c *Controller) addFault(f supervisor.Fault) {
	c.mu.Lock()
	c.faults = append(c.faults, f)
	if len(c.faults) > maxFaults {
		c.faults = c.faults[len(c.faults)-maxFaults:]
	}
	c.mu.Unlock()
}

// ffmpegBin は ffmpeg の指定。
func (c *Controller) ffmpegBin() string {
	if c.opt.FFmpeg != "" {
		return c.opt.FFmpeg
	}
	return os.Getenv("SRTRX_FFMPEG")
}

// mediamtxEnabled は同梱の MediaMTX を使うか。
func (c *Controller) mediamtxEnabled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg.MediaMTX.Enabled
}

func (c *Controller) buildSupervisors() {
	c.mtxSV = supervisor.New(supervisor.Target{
		Name:          "mediamtx",
		Start:         c.startMediaMTX,
		Ready:         func() bool { c.mu.Lock(); defer c.mu.Unlock(); return !c.mtxReadyAt.IsZero() },
		LastAlive:     func() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.mtxLastOK },
		StartTimeout:  func() time.Duration { return c.ms(func(g config.Config) int { return g.MediaMTX.ReadyTimeoutMS }) },
		HangThreshold: func() time.Duration { return c.ms(func(g config.Config) int { return g.MediaMTX.HangMS }) },
		HangReason:    func() string { return "/metrics無応答" },
		Backoff:       c.backoffParams,
		OnFault:       c.addFault,
		OnChange:      func(supervisor.Snapshot) { c.pushState() },
	}, c.nowClock(), false)

	c.ffSV = supervisor.New(supervisor.Target{
		Name:      "ffmpeg",
		Start:     c.startFFmpeg,
		Ready:     c.ffmpegReady,
		LastAlive: func() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.lastFrame },
		// 起動してから最初のフレームまでは、キーフレームを待つので、ウォッチドッグより長く取る
		StartTimeout: func() time.Duration {
			c.mu.Lock()
			defer c.mu.Unlock()
			return time.Duration(c.cfg.StartTimeoutMSEffective()) * time.Millisecond
		},
		HangThreshold: func() time.Duration { return time.Duration(c.watchdogMS()) * time.Millisecond },
		HangReason: func() string {
			return fmt.Sprintf("ウォッチドッグ(フレームが%.1f秒来ない)", float64(c.watchdogMS())/1000)
		},
		// ハートビートが途絶えている間は、フレーム途絶の判定を止める(プレイヤーの障害で ffmpeg を道連れにしない)
		Paused: func() bool { return !c.heartbeatFresh() },
		// 同梱の MediaMTX が running でない間、診断が通らない間は、起動しない
		Gate:    c.ffmpegGate,
		Backoff: c.backoffParams,
		OnFault: func(f supervisor.Fault) {
			c.addFault(f)
			cfg := c.Config()
			s := cfg.Supervisor
			c.hw.Record(f, cfg.Video.HWAccel != "none", time.Duration(s.HWFallbackFailMS)*time.Millisecond, s.HWFallbackFailCount)
		},
		OnChange: func(supervisor.Snapshot) { c.pushState() },
	}, c.nowClock(), false)
}

// ffmpegReady は、いまの ffmpeg が最初のフレームを出したか。
// 起動後のハートビートで取り直した基準を、frames_total が超えたら真。
func (c *Controller) ffmpegReady() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.ffBasePending && c.player.FramesTotal > c.ffBase
}

type clockFn func() time.Time

func (f clockFn) Now() time.Time { return f() }

func (c *Controller) nowClock() supervisor.Clock { return clockFn(c.now) }

func (c *Controller) ms(f func(config.Config) int) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Duration(f(c.cfg)) * time.Millisecond
}

func (c *Controller) watchdogMS() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg.WatchdogThresholdMS()
}

func (c *Controller) backoffParams() supervisor.BackoffParams {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.cfg.Supervisor
	return supervisor.BackoffParams{
		Initial:    time.Duration(s.BackoffInitialMS) * time.Millisecond,
		Max:        time.Duration(s.BackoffMaxMS) * time.Millisecond,
		ResetAfter: time.Duration(s.BackoffResetAfterMS) * time.Millisecond,
	}
}

func (c *Controller) heartbeatFresh() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.player.LastBeat.IsZero() && c.now().Sub(c.player.LastBeat) <= heartbeatLost
}

func (c *Controller) ffmpegGate() bool {
	c.mu.Lock()
	diagOK := c.diagOK
	enabled := c.cfg.MediaMTX.Enabled
	c.mu.Unlock()
	if !diagOK {
		return false
	}
	return !enabled || c.mtxSV.Snapshot().State == supervisor.Running
}

// ---- 子プロセスの Runner ----

type procRunner struct {
	p      *proc.Proc
	grace  time.Duration
	reason func(proc.ExitInfo) string
}

func (r *procRunner) Done() <-chan struct{} { return r.p.Done() }
func (r *procRunner) Stop()                 { r.p.Stop(r.grace) }
func (r *procRunner) Tail(n int) []string   { return r.p.Tail(n) }
func (r *procRunner) Reason() string {
	if r.reason != nil {
		return r.reason(r.p.Exit())
	}
	return r.p.Exit().String()
}

// ---- ffmpeg ----

func (c *Controller) startFFmpeg() (supervisor.Runner, error) {
	c.mu.Lock()
	cfg := c.cfg.Effective()
	caps := c.caps
	hw := cfg.Video.HWAccel
	c.mu.Unlock()
	if c.hw.Active() {
		hw = "none"
	}
	output := c.paths.FrameSock
	if !caps.HasUnix {
		output = c.paths.FrameFIFO
	}
	o := ffmpeg.Options{Config: cfg, Caps: caps, HWAccel: hw, Output: output}
	slog.Info("ffmpeg を起動する", "args", ffmpeg.BuildArgs(o))

	// フレーム判定の基準を取り直す
	c.mu.Lock()
	c.ffBase = c.player.FramesTotal
	c.ffBasePending = true // 起動後の最初のハートビートで取り直す
	c.lastFrame = c.now()
	c.mu.Unlock()

	p, err := ffmpeg.Start(o, c.paths.FFmpegPID, nil)
	if err != nil {
		return nil, err
	}
	return &procRunner{p: p, grace: ffmpeg.StopGrace}, nil
}

// ---- MediaMTX ----

func (c *Controller) startMediaMTX() (supervisor.Runner, error) {
	c.mu.Lock()
	cfg := c.cfg.Effective()
	bin := c.mtxBin
	c.mtxLastOK, c.mtxReadyAt = time.Time{}, time.Time{}
	c.mu.Unlock()

	if bin == "" {
		b, err := mediamtx.Extract(c.paths.BinDir)
		if err != nil {
			return nil, fmt.Errorf("MediaMTX を書き出せない: %w", err)
		}
		bin = b
		c.mu.Lock()
		c.mtxBin = b
		c.mu.Unlock()
	}
	if err := mediamtx.WriteConfig(c.paths.MediaMTXYML, cfg.MediaMTX); err != nil {
		return nil, fmt.Errorf("mediamtx.yml を書けない: %w", err)
	}
	p, err := mediamtx.Start(bin, c.paths.MediaMTXYML, c.paths.MediaMTXPID, nil)
	if err != nil {
		return nil, err
	}
	url := cfg.Metrics.URL
	go c.pingLoop(p, url)
	go func() {
		select {
		case <-p.PortConflict():
			c.mu.Lock()
			c.portInUse = true
			c.mu.Unlock()
			slog.Error(mediamtx.PortInUse)
		case <-p.Done():
		}
	}()
	return &procRunner{p: p.Proc, grace: mediamtx.StopGrace, reason: func(e proc.ExitInfo) string {
		select {
		case <-p.PortConflict():
			return mediamtx.PortInUse
		default:
		}
		return e.String()
	}}, nil
}

// pingLoop は、この MediaMTX のプロセスが生きている間、/metrics が取れるかを見る。
// 準備完了(最初の200)と、固まり(取れない時間)の判定に使う。統計の取得(Poller)とは別。
func (c *Controller) pingLoop(p *mediamtx.Process, url string) {
	defer logx.Recover("controller.pingLoop")
	client := &http.Client{Timeout: 800 * time.Millisecond}
	tk := time.NewTicker(250 * time.Millisecond)
	defer tk.Stop()
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := mediamtx.Ping(ctx, client, url)
		cancel()
		if err == nil {
			now := c.now()
			c.mu.Lock()
			c.mtxLastOK = now
			if c.mtxReadyAt.IsZero() {
				c.mtxReadyAt = now
			}
			c.mu.Unlock()
		}
		select {
		case <-p.Done():
			return
		case <-tk.C:
		}
	}
}

// ---- 自己診断 ----

func (c *Controller) diagnose(ctx context.Context) {
	caps, err := ffmpeg.Diagnose(ctx, c.ffmpegBin())
	c.mu.Lock()
	was := c.diagOK
	c.diagOK = err == nil
	c.diagErr = err
	if err == nil {
		c.caps = caps
	}
	c.mu.Unlock()
	switch {
	case err != nil:
		slog.Error("自己診断に失敗", "reason", err.Error())
	case !was:
		slog.Info("自己診断に合格", "ffmpeg", caps.VersionText, "path", caps.Path, "srt", caps.HasSRT, "unix", caps.HasUnix)
		if !caps.HasUnix {
			slog.Warn("この ffmpeg には unix プロトコルが無いので、FIFO で受け渡す")
		}
	}
	c.pushState()
}

func (c *Controller) diagLoop(ctx context.Context) {
	defer logx.Recover("controller.diagLoop")
	for {
		c.diagnose(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(diagInterval):
		}
	}
}

// Run は監督を始め、ctx が終わるか Close が呼ばれるまで動く。
func (c *Controller) Run(ctx context.Context) {
	cfg := c.Config()
	eff := cfg.Effective()
	c.poll = metrics.NewPoller(eff.Metrics.URL,
		time.Duration(cfg.Metrics.IntervalMS)*time.Millisecond, time.Duration(cfg.Metrics.TimeoutMS)*time.Millisecond,
		c.onStats)

	c.bg.Add(4)
	go func() { defer c.bg.Done(); c.diagLoop(ctx) }()
	go func() { defer c.bg.Done(); c.poll.Run(ctx) }()
	go func() { defer c.bg.Done(); c.ipcLoop(ctx) }()
	go func() { defer c.bg.Done(); c.stateLoop(ctx) }()

	// MediaMTX を先に起動し、ffmpeg はその準備完了を待つ。止めるときは ffmpeg を先に止める。
	var mctx, fctx context.Context
	mctx, c.mtxCancel = context.WithCancel(context.Background())
	fctx, c.ffCancel = context.WithCancel(context.Background())
	c.mtxDone, c.ffDone = make(chan struct{}), make(chan struct{})
	if cfg.MediaMTX.Enabled {
		c.mtxSV.Start()
	}
	c.ffSV.Start()
	go func() { defer close(c.mtxDone); c.mtxSV.Run(mctx) }()
	go func() { defer close(c.ffDone); c.ffSV.Run(fctx) }()

	<-ctx.Done()
	c.Close()
}

// Close は、ffmpeg、MediaMTX の順に止める。
func (c *Controller) Close() {
	if c.ffCancel != nil {
		c.ffCancel()
		<-c.ffDone
		c.mtxCancel()
		<-c.mtxDone
		c.ffCancel, c.mtxCancel = nil, nil
	}
}

// ---- 操作(GUIから) ----

// RestartAll は「再起動」ボタン。
func (c *Controller) RestartAll() {
	if c.mediamtxEnabled() {
		c.mtxSV.Restart()
	}
	c.ffSV.Restart()
}

// StopAll は「停止」ボタン。自動では再起動しない。
func (c *Controller) StopAll() {
	c.ffSV.Stop()
	c.mtxSV.Stop()
}

// StartAll は「開始」ボタン。
func (c *Controller) StartAll() {
	if c.mediamtxEnabled() {
		c.mtxSV.Start()
	}
	c.ffSV.Start()
}

// ResetCounters は累計のリセット(GUIのボタンとホットキー)。
func (c *Controller) ResetCounters() {
	if c.poll == nil {
		return
	}
	st := c.poll.Tracker.Reset()
	c.ipcMu.Lock()
	c.stats = st
	c.ipcMu.Unlock()
	c.sendIPC(ipc.NewCountersReset())
	c.sendStats(c.poll.Tracker.Last())
}

// Status はGUI用の状態の写し。
func (c *Controller) Status() Status {
	c.mu.Lock()
	st := Status{
		MediaMTXEnabled: c.cfg.MediaMTX.Enabled,
		PortInUse:       c.portInUse,
		Player:          c.player,
		LoadNotes:       append([]string(nil), c.loadInfo.Notes...),
		Locked:          c.cfg.Lock.Enabled,
		Config:          c.cfg.Clone(),
		FFmpegVersion:   c.caps.VersionText,
	}
	if c.diagErr != nil {
		st.DiagError = c.diagErr.Error()
	}
	st.Player.Connected = !c.player.LastBeat.IsZero() && c.now().Sub(c.player.LastBeat) <= heartbeatLost
	c.mu.Unlock()
	st.FFmpeg = c.ffSV.Snapshot()
	st.MediaMTX = c.mtxSV.Snapshot()
	st.HWFallback = c.hw.Active()
	c.ipcMu.Lock()
	st.Stats = c.stats
	c.ipcMu.Unlock()
	return st
}
