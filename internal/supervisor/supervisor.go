// Package supervisor は、ffmpeg と MediaMTX の両方に使う、1つの監督の仕組み。
// 状態は starting / running / backoff / stopped の4つ。
package supervisor

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"srtrx/internal/logx"
)

// 状態名(ipc の State と同じ文字列)。
const (
	Starting = "starting"
	Running  = "running"
	Backoff  = "backoff"
	Stopped  = "stopped"
)

// Clock は時計。テストで差し替える。
type Clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// Runner は起動した子プロセス1つ。
type Runner interface {
	Done() <-chan struct{}
	// Stop は停止(SIGTERM、猶予、SIGKILL)し、終了を確認して戻る。
	Stop()
	// Reason は終了の理由(Done が閉じた後)。
	Reason() string
	// Tail は直近の標準エラー n 行。
	Tail(n int) []string
}

// FaultKind は障害の種類。
type FaultKind int

const (
	FaultExit         FaultKind = iota // プロセスが終了した
	FaultHang                          // 固まり(ウォッチドッグ、/metrics無応答)
	FaultStartTimeout                  // starting のまま準備完了にならなかった
	FaultStartError                    // 起動できなかった
)

// Fault は障害履歴の1件。
type Fault struct {
	Time   time.Time
	Target string
	Kind   FaultKind
	Reason string   // 終了コード、シグナル、ウォッチドッグ、/metrics無応答 など
	Stderr []string // 直近3行
	Uptime time.Duration
}

// Snapshot は監督の現在の状態。
type Snapshot struct {
	Name         string
	State        string
	RestartCount int
	Since        time.Time // いまの状態に入った時刻
	LastError    string
	GateWait     bool // gate が閉じているため待っている(backoff)
}

// BackoffParams は再起動の待ち時間の規則。
type BackoffParams struct {
	Initial, Max, ResetAfter time.Duration
}

// Target は監督される対象の定義。ffmpeg と MediaMTX で違う部分をここに集める。
type Target struct {
	Name string

	// Start はプロセスを起動する。starting へ入るたびに呼ばれる。
	Start func() (Runner, error)
	// Ready は starting 中に呼ばれ、true で running へ進む。
	Ready func() bool
	// LastAlive は生存の証拠の最終時刻(ffmpeg: 最後にフレームが増えた時刻、MediaMTX: /metrics が最後に取れた時刻)。
	LastAlive func() time.Time
	// StartTimeout は starting が準備完了にならずに許される時間。
	StartTimeout func() time.Duration
	// HangThreshold は running で LastAlive から許される時間。
	HangThreshold func() time.Duration
	// HangReason はその超過の理由の文言。
	HangReason func() string
	// Paused が true の間は、時間切れの判定を止める(プレイヤー障害でffmpegを道連れにしない)。
	Paused func() bool
	// Gate が false の間は起動しない。動いていれば止める。待ち時間には数えない。
	Gate func() bool
	// Backoff は待ち時間の規則(設定の変更を即時に反映するため、毎回呼ぶ)。
	Backoff func() BackoffParams

	OnFault  func(Fault)
	OnChange func(Snapshot)
}

// Supervisor は Target を監督する。
type Supervisor struct {
	t     Target
	clock Clock

	stepMu sync.Mutex // Tick を直列化する

	mu         sync.Mutex // 以下の状態と、コマンドを守る
	state      string
	since      time.Time
	restarts   int
	starts     int
	lastErr    string
	gateWait   bool
	runner     Runner
	startedAt  time.Time
	runningAt  time.Time
	lastPause  time.Time
	delay      time.Duration
	backoffEnd time.Time

	cmdStop, cmdStart, cmdRestart bool
	wake                          chan struct{}
}

// New は停止状態から始める(Start() または Run 開始で starting へ)。
// autostart が true なら、最初の Tick で starting へ進む。
func New(t Target, clock Clock, autostart bool) *Supervisor {
	if clock == nil {
		clock = realClock{}
	}
	s := &Supervisor{t: t, clock: clock, state: Stopped, since: clock.Now(), wake: make(chan struct{}, 1)}
	s.cmdStart = autostart
	return s
}

// Snapshot は現在の状態。
func (s *Supervisor) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *Supervisor) snapshotLocked() Snapshot {
	return Snapshot{Name: s.t.Name, State: s.state, RestartCount: s.restarts, Since: s.since,
		LastError: s.lastErr, GateWait: s.gateWait}
}

// Start は「開始」ボタン(stopped → starting)。
func (s *Supervisor) Start() { s.command(func() { s.cmdStart = true; s.cmdStop = false }) }

// Stop は「停止」ボタン(どの状態でも → stopped。自動では再起動しない)。
func (s *Supervisor) Stop() {
	s.command(func() { s.cmdStop = true; s.cmdStart = false; s.cmdRestart = false })
}

// Restart は「再起動」と「再起動が必要な設定の適用」(待ち時間なし、待ち時間は初期値へ)。
func (s *Supervisor) Restart() { s.command(func() { s.cmdRestart = true; s.cmdStop = false }) }

func (s *Supervisor) command(f func()) {
	s.mu.Lock()
	f()
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Run は ctx が終わるまで Tick を回し、終わるときに子プロセスを止める。
func (s *Supervisor) Run(ctx context.Context) {
	defer logx.Recover("supervisor." + s.t.Name)
	tk := time.NewTicker(50 * time.Millisecond)
	defer tk.Stop()
	for {
		s.Tick()
		select {
		case <-ctx.Done():
			s.Shutdown()
			return
		case <-s.wake:
		case <-tk.C:
		}
	}
}

// Shutdown は動いている子プロセスを止める(状態は変えない)。
func (s *Supervisor) Shutdown() {
	s.stepMu.Lock()
	defer s.stepMu.Unlock()
	s.stopRunner()
}

// ---- 以下は Tick(単一のgoroutineから)で動く ----

func (s *Supervisor) stopRunner() {
	s.mu.Lock()
	r := s.runner
	s.runner = nil
	s.mu.Unlock()
	if r != nil {
		r.Stop() // 時間がかかるので、ロックを持たずに呼ぶ
	}
}

// transition は状態を変え、OnChange を呼ぶ(ロックの外で)。
func (s *Supervisor) transition(now time.Time, state string, mutate func()) {
	s.mu.Lock()
	if s.state != state {
		s.since = now
	}
	s.state = state
	if mutate != nil {
		mutate()
	}
	snap := s.snapshotLocked()
	s.mu.Unlock()
	if s.t.OnChange != nil {
		s.t.OnChange(snap)
	}
}

func (s *Supervisor) params() BackoffParams {
	p := s.t.Backoff()
	if p.Initial <= 0 {
		p.Initial = 200 * time.Millisecond
	}
	if p.Max < p.Initial {
		p.Max = p.Initial
	}
	return p
}

func (s *Supervisor) recordFault(now time.Time, kind FaultKind, reason string, r Runner) {
	f := Fault{Time: now, Target: s.t.Name, Kind: kind, Reason: reason, Uptime: now.Sub(s.startedAt)}
	if r != nil {
		f.Stderr = r.Tail(3)
	}
	s.mu.Lock()
	s.lastErr = reason
	s.mu.Unlock()
	slog.Warn("障害", "target", s.t.Name, "reason", reason, "uptime", f.Uptime.String(), "stderr", f.Stderr)
	if s.t.OnFault != nil {
		s.t.OnFault(f)
	}
}

// enterBackoff は待ち時間を決めて backoff へ。次の待ち時間は ×2(上限 Max)。
func (s *Supervisor) enterBackoff(now time.Time) {
	p := s.params()
	d := s.delay
	if d < p.Initial {
		d = p.Initial
	}
	if d > p.Max {
		d = p.Max
	}
	next := d * 2
	if next > p.Max {
		next = p.Max
	}
	s.transition(now, Backoff, func() {
		s.delay = next
		s.backoffEnd = now.Add(d)
		s.gateWait = false
	})
}

func (s *Supervisor) startNow(now time.Time) {
	r, err := s.t.Start()
	if err != nil {
		s.mu.Lock()
		s.startedAt = now
		s.mu.Unlock()
		s.recordFault(now, FaultStartError, err.Error(), nil)
		s.enterBackoff(now)
		return
	}
	s.transition(now, Starting, func() {
		s.since = now // starting → starting(再起動)でも「入ってからの経過」を数え直す
		s.runner = r
		s.startedAt = now
		s.gateWait = false
		if s.starts > 0 {
			s.restarts++
		}
		s.starts++
	})
}

func exited(r Runner) bool {
	select {
	case <-r.Done():
		return true
	default:
		return false
	}
}

// Tick は状態を1歩進める。Run が50msごとに呼ぶ。テストからは時計を進めて直接呼ぶ。
func (s *Supervisor) Tick() {
	s.stepMu.Lock()
	defer s.stepMu.Unlock()
	now := s.clock.Now()

	s.mu.Lock()
	stop, start, restart := s.cmdStop, s.cmdStart, s.cmdRestart
	s.cmdStop, s.cmdStart, s.cmdRestart = false, false, false
	state := s.state
	s.mu.Unlock()

	// --- コマンド ---
	if stop {
		s.stopRunner()
		if state != Stopped {
			s.transition(now, Stopped, func() { s.gateWait = false })
		}
		return
	}
	if state == Stopped {
		if start {
			s.mu.Lock()
			s.delay = s.params().Initial
			s.mu.Unlock()
			s.startIfGateOpen(now)
		}
		return
	}
	if restart {
		s.stopRunner()
		s.mu.Lock()
		s.delay = s.params().Initial
		s.mu.Unlock()
		s.startIfGateOpen(now)
		return
	}

	// --- gate(MediaMTX が running でない間、ffmpeg は待つ) ---
	if s.t.Gate != nil && !s.t.Gate() {
		switch state {
		case Starting, Running:
			s.stopRunner()
			s.transition(now, Backoff, func() { s.gateWait = true })
		case Backoff:
			s.mu.Lock()
			s.gateWait = true
			s.mu.Unlock()
		}
		return
	}
	s.mu.Lock()
	gw := s.gateWait
	s.mu.Unlock()
	if gw && state == Backoff {
		s.startNow(now) // 待ち時間なしで starting へ
		return
	}

	// --- 状態ごとの遷移 ---
	paused := s.t.Paused != nil && s.t.Paused()
	if paused {
		s.mu.Lock()
		s.lastPause = now
		s.mu.Unlock()
	}

	s.mu.Lock()
	r := s.runner
	startedAt, runningAt, lastPause, backoffEnd := s.startedAt, s.runningAt, s.lastPause, s.backoffEnd
	s.mu.Unlock()

	switch state {
	case Starting:
		if r == nil || exited(r) {
			s.onExit(now, r)
			return
		}
		if s.t.Ready != nil && s.t.Ready() {
			s.transition(now, Running, func() { s.runningAt = now })
			return
		}
		ref := later(startedAt, lastPause)
		if !paused && now.Sub(ref) > s.t.StartTimeout() {
			s.stopRunner()
			s.recordFault(now, FaultStartTimeout,
				fmt.Sprintf("起動しても %s の間、準備完了にならない", s.t.StartTimeout().Round(10*time.Millisecond)), r)
			s.enterBackoff(now)
		}
	case Running:
		if r == nil || exited(r) {
			s.onExit(now, r)
			return
		}
		ref := later(later(runningAt, lastPause), s.t.LastAlive())
		if !paused && now.Sub(ref) > s.t.HangThreshold() {
			s.stopRunner()
			s.recordFault(now, FaultHang, s.t.HangReason(), r)
			s.enterBackoff(now)
			return
		}
		if rst := s.params().ResetAfter; rst > 0 && now.Sub(runningAt) >= rst {
			s.mu.Lock()
			s.delay = s.params().Initial // 安定して動いた: 待ち時間を初期値へ
			s.mu.Unlock()
		}
	case Backoff:
		if !now.Before(backoffEnd) {
			s.startNow(now)
		}
	}
}

func (s *Supervisor) startIfGateOpen(now time.Time) {
	if s.t.Gate != nil && !s.t.Gate() {
		s.transition(now, Backoff, func() { s.gateWait = true })
		return
	}
	s.startNow(now)
}

func (s *Supervisor) onExit(now time.Time, r Runner) {
	s.mu.Lock()
	s.runner = nil
	s.mu.Unlock()
	reason := "プロセスが無い"
	if r != nil {
		reason = r.Reason()
	}
	s.recordFault(now, FaultExit, reason, r)
	s.enterBackoff(now)
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// HWFallback は、ハードウェアデコードの起動失敗を数え、続いたらソフトウェアデコードへ落とす。
// 設定ファイルは変えない。Reset(設定の適用)か、アプリの再起動まで維持する。
type HWFallback struct {
	mu          sync.Mutex
	consecutive int
	active      bool
}

// Record は ffmpeg の障害を記録する。configured は設定の hwaccel が none でないか。
// 起動後 failWithin 以内の終了が count 回連続したら有効になる。
func (h *HWFallback) Record(f Fault, configured bool, failWithin time.Duration, count int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !configured || h.active {
		return
	}
	if (f.Kind == FaultExit || f.Kind == FaultStartError) && f.Uptime <= failWithin {
		h.consecutive++
		if h.consecutive >= count {
			h.active = true
			slog.Warn("ハードウェアデコードの起動失敗が続いたので、ソフトウェアデコードで起動する", "count", h.consecutive)
		}
		return
	}
	h.consecutive = 0
}

// Active はソフトウェアデコードへ落としている間 true。
func (h *HWFallback) Active() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.active
}

// Reset は設定の適用時に呼ぶ。
func (h *HWFallback) Reset() {
	h.mu.Lock()
	h.consecutive, h.active = 0, false
	h.mu.Unlock()
}
