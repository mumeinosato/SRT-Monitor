package supervisor

import (
	"sync"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

type fakeRunner struct {
	done    chan struct{}
	once    sync.Once
	stopped bool
	reason  string
	tail    []string
}

func newRunner() *fakeRunner {
	return &fakeRunner{done: make(chan struct{}), reason: "終了コード 1", tail: []string{"a", "b", "c"}}
}
func (r *fakeRunner) Done() <-chan struct{} { return r.done }
func (r *fakeRunner) Stop()                 { r.stopped = true; r.exit() }
func (r *fakeRunner) exit()                 { r.once.Do(func() { close(r.done) }) }
func (r *fakeRunner) Reason() string        { return r.reason }
func (r *fakeRunner) Tail(n int) []string {
	if len(r.tail) > n {
		return r.tail[len(r.tail)-n:]
	}
	return r.tail
}

type rig struct {
	t       *testing.T
	clk     *fakeClock
	sv      *Supervisor
	runners []*fakeRunner
	faults  []Fault
	changes []Snapshot

	ready      bool
	alive      time.Time
	paused     bool
	gateOpen   bool
	startErr   error
	startCount int
	hang       time.Duration
	startTO    time.Duration
}

func newRig(t *testing.T) *rig {
	r := &rig{t: t, clk: &fakeClock{t: time.Unix(1000, 0)}, gateOpen: true, hang: 3 * time.Second, startTO: 3 * time.Second}
	r.sv = New(Target{
		Name: "ffmpeg",
		Start: func() (Runner, error) {
			r.startCount++
			if r.startErr != nil {
				return nil, r.startErr
			}
			ru := newRunner()
			r.runners = append(r.runners, ru)
			r.ready = false
			r.alive = r.clk.Now()
			return ru, nil
		},
		Ready:         func() bool { return r.ready },
		LastAlive:     func() time.Time { return r.alive },
		StartTimeout:  func() time.Duration { return r.startTO },
		HangThreshold: func() time.Duration { return r.hang },
		HangReason:    func() string { return "ウォッチドッグ" },
		Paused:        func() bool { return r.paused },
		Gate:          func() bool { return r.gateOpen },
		Backoff: func() BackoffParams {
			return BackoffParams{Initial: 200 * time.Millisecond, Max: 2 * time.Second, ResetAfter: 10 * time.Second}
		},
		OnFault:  func(f Fault) { r.faults = append(r.faults, f) },
		OnChange: func(s Snapshot) { r.changes = append(r.changes, s) },
	}, r.clk, true)
	return r
}

func (r *rig) tick(d time.Duration) {
	r.clk.Advance(d)
	r.sv.Tick()
}
func (r *rig) state() string { return r.sv.Snapshot().State }
func (r *rig) want(s string) {
	r.t.Helper()
	if got := r.state(); got != s {
		r.t.Fatalf("状態: want %s got %s", s, got)
	}
}
func (r *rig) last() *fakeRunner { return r.runners[len(r.runners)-1] }

// 起動して running まで進める。
func (r *rig) up() {
	r.t.Helper()
	r.tick(0)
	r.want(Starting)
	r.ready = true
	r.tick(10 * time.Millisecond)
	r.want(Running)
}

func TestStartingToRunningOnReady(t *testing.T) {
	r := newRig(t)
	r.want(Stopped)
	r.tick(0)
	r.want(Starting)
	r.tick(100 * time.Millisecond)
	r.want(Starting)
	r.ready = true
	r.tick(100 * time.Millisecond)
	r.want(Running)
	if len(r.faults) != 0 {
		t.Error("障害なし")
	}
}

func TestStartingProcessExitsToBackoff(t *testing.T) {
	r := newRig(t)
	r.tick(0)
	r.last().exit()
	r.tick(50 * time.Millisecond)
	r.want(Backoff)
	if len(r.faults) != 1 || r.faults[0].Kind != FaultExit || r.faults[0].Reason != "終了コード 1" || len(r.faults[0].Stderr) != 3 {
		t.Errorf("障害履歴: %+v", r.faults)
	}
}

func TestStartingTimeoutStopsAndBackoff(t *testing.T) {
	r := newRig(t)
	r.tick(0)
	r.tick(2900 * time.Millisecond)
	r.want(Starting)
	r.tick(200 * time.Millisecond) // しきい値(3秒)を超えた
	r.want(Backoff)
	if !r.last().stopped {
		t.Error("プロセスグループを止める")
	}
	if len(r.faults) != 1 || r.faults[0].Kind != FaultStartTimeout {
		t.Errorf("%+v", r.faults)
	}
}

func TestRunningProcessExitToBackoff(t *testing.T) {
	r := newRig(t)
	r.up()
	r.last().exit() // kill -9
	r.tick(50 * time.Millisecond)
	r.want(Backoff)
	if r.faults[0].Kind != FaultExit {
		t.Errorf("%+v", r.faults)
	}
}

func TestRunningWatchdog(t *testing.T) {
	r := newRig(t)
	r.up()
	r.alive = r.clk.Now() // 最後のフレーム
	r.tick(2900 * time.Millisecond)
	r.want(Running)
	r.alive = r.clk.Now().Add(-2900 * time.Millisecond)
	r.tick(0)
	r.want(Running)
	r.tick(200 * time.Millisecond) // 3.1秒フレーム無し
	r.want(Backoff)
	if !r.last().stopped {
		t.Error("固まったらプロセスグループを止める(T5)")
	}
	if r.faults[0].Kind != FaultHang || r.faults[0].Reason != "ウォッチドッグ" {
		t.Errorf("%+v", r.faults)
	}
}

func TestFrameResumeResetsWatchdog(t *testing.T) {
	r := newRig(t)
	r.up()
	for i := 0; i < 10; i++ {
		r.tick(2 * time.Second)
		r.alive = r.clk.Now() // フレームが来続ける
		r.want(Running)
	}
}

func TestBackoffDoublesToCap(t *testing.T) {
	r := newRig(t)
	r.tick(0)
	var waits []time.Duration
	for i := 0; i < 7; i++ {
		r.last().exit()
		r.tick(10 * time.Millisecond)
		r.want(Backoff)
		// 待ち時間を測る: 1msずつ進めて starting に戻るまで
		t0 := r.clk.Now()
		for r.state() == Backoff {
			r.tick(10 * time.Millisecond)
			if r.clk.Now().Sub(t0) > 5*time.Second {
				t.Fatal("backoff が終わらない")
			}
		}
		waits = append(waits, r.clk.Now().Sub(t0))
	}
	want := []time.Duration{200, 400, 800, 1600, 2000, 2000, 2000}
	for i, w := range want {
		got := waits[i]
		if got < w*time.Millisecond || got > w*time.Millisecond+20*time.Millisecond {
			t.Errorf("%d 回目の待ち: want %dms got %v", i+1, w, got)
		}
	}
}

func TestBackoffResetAfterStableRun(t *testing.T) {
	r := newRig(t)
	r.tick(0)
	// 3回失敗させて待ち時間を伸ばす
	for i := 0; i < 3; i++ {
		r.last().exit()
		r.tick(10 * time.Millisecond)
		r.tick(2 * time.Second)
		r.want(Starting)
	}
	r.ready = true
	r.tick(10 * time.Millisecond)
	r.want(Running)
	// 10秒安定して動く(フレームは来続ける)
	for i := 0; i < 11; i++ {
		r.alive = r.clk.Now()
		r.tick(time.Second)
	}
	r.want(Running)
	r.last().exit()
	r.tick(10 * time.Millisecond)
	r.want(Backoff)
	t0 := r.clk.Now()
	for r.state() == Backoff {
		r.tick(10 * time.Millisecond)
	}
	if d := r.clk.Now().Sub(t0); d > 250*time.Millisecond {
		t.Errorf("安定して動いたら待ち時間は初期値(0.2秒)へ: %v", d)
	}
}

func TestRestartCommandNoWaitAndResetsDelay(t *testing.T) {
	r := newRig(t)
	r.tick(0)
	for i := 0; i < 3; i++ { // 待ち時間を伸ばす
		r.last().exit()
		r.tick(10 * time.Millisecond)
		r.tick(2 * time.Second)
	}
	r.last().exit()
	r.tick(10 * time.Millisecond)
	r.want(Backoff)
	n := r.startCount
	r.sv.Restart()
	r.tick(0)
	r.want(Starting)
	if r.startCount != n+1 {
		t.Error("待ち時間なしで起動し直す")
	}
	// 待ち時間が初期値に戻っている
	r.last().exit()
	r.tick(10 * time.Millisecond)
	t0 := r.clk.Now()
	for r.state() == Backoff {
		r.tick(10 * time.Millisecond)
	}
	if d := r.clk.Now().Sub(t0); d > 250*time.Millisecond {
		t.Errorf("再起動で待ち時間を初期値へ: %v", d)
	}
}

func TestRestartWhileRunningStopsOld(t *testing.T) {
	r := newRig(t)
	r.up()
	old := r.last()
	r.sv.Restart()
	r.tick(0)
	r.want(Starting)
	if !old.stopped || r.last() == old {
		t.Error("古いプロセスを止めて新しく起動する")
	}
}

func TestStopAndStart(t *testing.T) {
	r := newRig(t)
	r.up()
	r.sv.Stop()
	r.tick(0)
	r.want(Stopped)
	if !r.last().stopped {
		t.Error("停止でプロセスグループを止める")
	}
	n := r.startCount
	r.tick(10 * time.Second)
	r.want(Stopped)
	if r.startCount != n {
		t.Error("stopped では自動で再起動しない")
	}
	r.sv.Start()
	r.tick(0)
	r.want(Starting)
}

func TestStopFromBackoff(t *testing.T) {
	r := newRig(t)
	r.tick(0)
	r.last().exit()
	r.tick(10 * time.Millisecond)
	r.want(Backoff)
	r.sv.Stop()
	r.tick(0)
	r.want(Stopped)
	r.tick(10 * time.Second)
	r.want(Stopped)
}

func TestRestartCountAndSince(t *testing.T) {
	r := newRig(t)
	r.up()
	if c := r.sv.Snapshot().RestartCount; c != 0 {
		t.Errorf("最初の起動は再起動ではない: %d", c)
	}
	r.last().exit()
	r.tick(10 * time.Millisecond)
	r.tick(300 * time.Millisecond)
	r.want(Starting)
	if c := r.sv.Snapshot().RestartCount; c != 1 {
		t.Errorf("restart_count: %d", c)
	}
	snap := r.sv.Snapshot()
	r.tick(500 * time.Millisecond)
	if !r.sv.Snapshot().Since.Equal(snap.Since) {
		t.Error("同じ状態の間は since が変わらない")
	}
	if r.sv.Snapshot().LastError != "終了コード 1" {
		t.Errorf("最後のエラー: %q", r.sv.Snapshot().LastError)
	}
}

func TestStartErrorGoesToBackoff(t *testing.T) {
	r := newRig(t)
	r.startErr = errBoom
	r.tick(0)
	r.want(Backoff)
	if len(r.faults) != 1 || r.faults[0].Kind != FaultStartError {
		t.Errorf("%+v", r.faults)
	}
	r.startErr = nil
	r.tick(250 * time.Millisecond)
	r.want(Starting)
}

type boomErr struct{}

func (boomErr) Error() string { return "boom" }

var errBoom = boomErr{}

func TestGateClosedStopsAndWaitsWithoutBackoffCount(t *testing.T) {
	r := newRig(t)
	r.up()
	old := r.last()
	r.gateOpen = false // MediaMTX が落ちた
	r.tick(50 * time.Millisecond)
	r.want(Backoff)
	if !old.stopped {
		t.Error("MediaMTX が running でないなら ffmpeg も先に止める")
	}
	if !r.sv.Snapshot().GateWait {
		t.Error("GateWait")
	}
	if len(r.faults) != 0 {
		t.Error("gate による停止は障害ではない")
	}
	n := r.startCount
	r.tick(30 * time.Second) // いくら待っても起動しない
	r.want(Backoff)
	if r.startCount != n {
		t.Error("gate が閉じている間は起動しない")
	}
	r.gateOpen = true // MediaMTX が running になった
	r.tick(10 * time.Millisecond)
	r.want(Starting)
	if r.startCount != n+1 {
		t.Error("待ち時間なしで starting へ")
	}
}

func TestGateClosedAtBootWaitsThenStartsImmediately(t *testing.T) {
	r := newRig(t)
	r.gateOpen = false
	r.tick(0)
	r.want(Backoff)
	if r.startCount != 0 {
		t.Error("gate が閉じている間は起動しない")
	}
	r.tick(10 * time.Second)
	r.want(Backoff)
	r.gateOpen = true
	r.tick(0)
	r.want(Starting)
}

func TestGateDoesNotAdvanceBackoffDelay(t *testing.T) {
	r := newRig(t)
	r.up()
	r.gateOpen = false
	r.tick(10 * time.Millisecond)
	r.tick(10 * time.Second)
	r.gateOpen = true
	r.tick(0)
	r.want(Starting)
	// gate 由来の停止で待ち時間が伸びていない: 次の本物の失敗は初期値(0.2秒)で待つ
	r.last().exit()
	r.tick(10 * time.Millisecond)
	t0 := r.clk.Now()
	for r.state() == Backoff {
		r.tick(10 * time.Millisecond)
	}
	if d := r.clk.Now().Sub(t0); d > 250*time.Millisecond {
		t.Errorf("待ち時間 %v", d)
	}
}

func TestPausedSuspendsWatchdogAndStartTimeout(t *testing.T) {
	r := newRig(t)
	r.up()
	r.alive = r.clk.Now()
	r.paused = true // ハートビート途絶(プレイヤー障害)
	r.tick(60 * time.Second)
	r.want(Running)
	// 解除直後に誤判定しない: 解除から hang の間は猶予
	r.paused = false
	r.tick(2900 * time.Millisecond)
	r.want(Running)
	r.tick(200 * time.Millisecond)
	r.want(Backoff)

	// starting でも同様
	r = newRig(t)
	r.tick(0)
	r.paused = true
	r.tick(60 * time.Second)
	r.want(Starting)
}

func TestPausedDoesNotBlockProcessExit(t *testing.T) {
	r := newRig(t)
	r.up()
	r.paused = true
	r.last().exit()
	r.tick(50 * time.Millisecond)
	r.want(Backoff)
}

func TestOnChangeSequence(t *testing.T) {
	r := newRig(t)
	r.up()
	r.last().exit()
	r.tick(10 * time.Millisecond)
	var seq []string
	for _, c := range r.changes {
		seq = append(seq, c.State)
	}
	want := []string{Starting, Running, Backoff}
	if len(seq) != len(want) {
		t.Fatalf("%v", seq)
	}
	for i := range want {
		if seq[i] != want[i] {
			t.Errorf("%v", seq)
		}
	}
}

func TestDynamicBackoffParams(t *testing.T) {
	r := newRig(t)
	var p BackoffParams = BackoffParams{Initial: 500 * time.Millisecond, Max: 500 * time.Millisecond, ResetAfter: time.Hour}
	r.sv.t.Backoff = func() BackoffParams { return p }
	r.tick(0)
	r.last().exit()
	r.tick(10 * time.Millisecond)
	r.tick(400 * time.Millisecond)
	r.want(Backoff)
	r.tick(150 * time.Millisecond)
	r.want(Starting)
}

func TestShutdownStopsRunner(t *testing.T) {
	r := newRig(t)
	r.up()
	r.sv.Shutdown()
	if !r.last().stopped {
		t.Error("終了時にプロセスを止める")
	}
}

func TestHWFallback(t *testing.T) {
	var h HWFallback
	fast := Fault{Kind: FaultExit, Uptime: time.Second}
	slow := Fault{Kind: FaultExit, Uptime: 10 * time.Second}
	h.Record(fast, true, 2*time.Second, 3)
	h.Record(fast, true, 2*time.Second, 3)
	if h.Active() {
		t.Fatal("2回では外さない")
	}
	h.Record(slow, true, 2*time.Second, 3) // 長く動いたら連続が途切れる
	h.Record(fast, true, 2*time.Second, 3)
	h.Record(fast, true, 2*time.Second, 3)
	if h.Active() {
		t.Fatal("連続でなければ外さない")
	}
	h.Record(fast, true, 2*time.Second, 3)
	if !h.Active() {
		t.Fatal("2秒以内の失敗が3回続いたら外す")
	}
	h.Reset()
	if h.Active() {
		t.Error("設定の適用で戻る")
	}
	// hwaccel=none では数えない
	for i := 0; i < 10; i++ {
		h.Record(fast, false, 2*time.Second, 3)
	}
	if h.Active() {
		t.Error("none のときは対象外")
	}
	// 固まり(ウォッチドッグ)は起動失敗に数えない
	for i := 0; i < 10; i++ {
		h.Record(Fault{Kind: FaultHang, Uptime: time.Second}, true, 2*time.Second, 3)
	}
	if h.Active() {
		t.Error("ウォッチドッグは対象外")
	}
}
