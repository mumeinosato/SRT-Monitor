package controller

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"srtrx/internal/config"
	"srtrx/internal/ipc"
	"srtrx/internal/supervisor"
)

func newTestController(t *testing.T) *Controller {
	t.Helper()
	dir := t.TempDir()
	paths := config.PathsFrom(filepath.Join(dir, "run"), filepath.Join(dir, "cfg"), filepath.Join(dir, "state"))
	c, err := New(Options{Paths: paths})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNewFirstStartUsesDefaultsAndNotes(t *testing.T) {
	c := newTestController(t)
	if got := c.Config(); got.Input.Port != 8890 {
		t.Errorf("既定値: %+v", got.Input)
	}
	if n := c.Status().LoadNotes; len(n) == 0 {
		t.Error("初回起動の理由が LoadNotes に出る")
	}
}

func TestSetLiveSavesAndAppliesWithoutRestartKeyChange(t *testing.T) {
	c := newTestController(t)
	if err := c.SetLive(func(g *config.Config) { g.Overlay.Opacity = 0.5; g.Overlay.Position = "bottom" }); err != nil {
		t.Fatal(err)
	}
	got := c.Config()
	if got.Overlay.Opacity != 0.5 || got.Overlay.Position != "bottom" {
		t.Errorf("即時反映: %+v", got.Overlay)
	}
	// 保存されている
	saved, info := config.Load(c.paths.ConfigFile)
	if info.Source == "" || saved.Overlay.Opacity != 0.5 {
		t.Errorf("保存されていない: %+v %+v", saved.Overlay, info)
	}
	// 範囲外は拒否され、現在の設定は変わらない
	if err := c.SetLive(func(g *config.Config) { g.Overlay.Opacity = 5 }); err == nil {
		t.Error("範囲外は拒否")
	}
	if c.Config().Overlay.Opacity != 0.5 {
		t.Error("拒否した変更が残った")
	}
}

func TestLockBlocksChanges(t *testing.T) {
	c := newTestController(t)
	if err := c.SetLock(true); err != nil {
		t.Fatal(err)
	}
	if !c.Status().Locked {
		t.Error("Locked")
	}
	if err := c.SetLive(func(g *config.Config) { g.Overlay.Opacity = 0.5 }); !errors.Is(err, ErrLocked) {
		t.Errorf("ロック中は SetLive 不可: %v", err)
	}
	d := c.Config()
	d.Input.LatencyMS = 500
	if err := c.ApplyRestart(d); !errors.Is(err, ErrLocked) {
		t.Errorf("ロック中は ApplyRestart 不可: %v", err)
	}
	if err := c.SetLock(false); err != nil {
		t.Fatal(err)
	}
	if err := c.SetLive(func(g *config.Config) { g.Overlay.Opacity = 0.5 }); err != nil {
		t.Errorf("解除後は変更できる: %v", err)
	}
}

func TestPendingRestartOnlyForRestartKeys(t *testing.T) {
	c := newTestController(t)
	d := c.Config()
	if c.PendingRestart(d) {
		t.Error("変更なしなら保留なし")
	}
	d.Overlay.Opacity = 0.3 // 即時の項目
	d.Supervisor.BackoffMaxMS = 3000
	d.Display.Fullscreen = false
	if c.PendingRestart(d) {
		t.Error("即時の項目だけなら再起動は要らない")
	}
	for name, mut := range map[string]func(*config.Config){
		"latency":  func(g *config.Config) { g.Input.LatencyMS = 500 },
		"width":    func(g *config.Config) { g.Video.Width = 1280 },
		"hwaccel":  func(g *config.Config) { g.Video.HWAccel = "vaapi" },
		"extra":    func(g *config.Config) { g.Video.ExtraArgs = []string{"-threads", "2"} },
		"mtx port": func(g *config.Config) { g.MediaMTX.SRTListen = ":9000" },
		"mode":     func(g *config.Config) { g.Input.Mode = "listener" },
	} {
		e := c.Config()
		mut(&e)
		if !c.PendingRestart(e) {
			t.Errorf("%s: 再起動が必要な設定", name)
		}
	}
}

func TestApplyRestartPersistsAndClearsPending(t *testing.T) {
	c := newTestController(t)
	d := c.Config()
	d.Input.LatencyMS = 500
	if err := c.ApplyRestart(d); err != nil {
		t.Fatal(err)
	}
	if c.Config().Input.LatencyMS != 500 || c.PendingRestart(d) {
		t.Error("適用後は保留が消える")
	}
	d.Input.Port = 0
	if err := c.ApplyRestart(d); err == nil {
		t.Error("範囲外は適用できない")
	}
}

func TestHeartbeatTracksFramesAndPlayerRestart(t *testing.T) {
	c := newTestController(t)
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }

	c.onHeartbeat(ipc.NewHeartbeat(30, 10, 100))
	if !c.heartbeatFresh() {
		t.Error("ハートビート直後は fresh")
	}
	last1 := c.lastFrame
	now = now.Add(time.Second)
	c.onHeartbeat(ipc.NewHeartbeat(0, 1500, 100)) // frames_total が増えない
	if !c.lastFrame.Equal(last1) {
		t.Error("frames_total が増えない間は lastFrame を進めない")
	}
	now = now.Add(time.Second)
	c.onHeartbeat(ipc.NewHeartbeat(30, 10, 130))
	if !c.lastFrame.Equal(now) {
		t.Error("frames_total が増えたら lastFrame を更新(制御の時計)")
	}

	// ffmpeg 起動時の基準
	c.ffBase = c.player.FramesTotal
	now = now.Add(time.Second)
	c.onHeartbeat(ipc.NewHeartbeat(30, 10, 130))
	ready := c.ffmpegReady
	if ready() {
		t.Error("基準から増えていなければ、まだ準備完了ではない")
	}
	c.onHeartbeat(ipc.NewHeartbeat(30, 10, 140))
	if !ready() {
		t.Error("基準を超えたら準備完了")
	}

	// プレイヤーが再起動して frames_total が 0 から始まり直した
	c.ffBase = 5000
	c.onHeartbeat(ipc.NewHeartbeat(0, -1, 0))
	c.onHeartbeat(ipc.NewHeartbeat(30, 10, 1))
	if !ready() {
		t.Error("プレイヤーの再起動で基準を取り直し、新しいフレームで準備完了になる")
	}

	// 途絶
	now = now.Add(4 * time.Second)
	if c.heartbeatFresh() {
		t.Error("3秒を超えたら fresh でない(フレーム途絶の判定を止める)")
	}
	if c.Status().Player.Connected {
		t.Error("プレイヤー応答なし")
	}
}

func TestStatePassesFFmpegWhenMediaMTXRunning(t *testing.T) {
	c := newTestController(t)
	// 外部 MediaMTX を使う設定なら、ffmpeg の状態をそのまま送る
	if err := c.SetLive(func(g *config.Config) { g.MediaMTX.Enabled = false }); err != nil {
		t.Fatal(err)
	}
	c.diagErr = nil
	st := c.State()
	if st.Type != "state" || st.State != supervisor.Stopped {
		t.Errorf("%+v", st)
	}
}

func TestStateIsBackoffWhileMediaMTXNotRunning(t *testing.T) {
	c := newTestController(t) // 同梱MediaMTXが既定で有効、まだ起動していない(stopped)
	c.ffSV.Start()
	c.ffSV.Tick() // ffmpeg は gate が閉じて backoff(待機)になる
	st := c.State()
	if st.State == ipc.StateRunning || st.Detail == "" {
		t.Errorf("MediaMTX が running でないときは backoff 系 + detail: %+v", st)
	}
	if st.Detail != "mediamtx: stopped" {
		t.Errorf("detail: %q", st.Detail)
	}
}

func TestStateReportsDiagFailure(t *testing.T) {
	c := newTestController(t)
	c.mu.Lock()
	c.diagErr = errors.New("ffmpeg が見つからない")
	c.mu.Unlock()
	st := c.State()
	if st.State != ipc.StateBackoff || st.Detail != "ffmpeg: ffmpeg が見つからない" {
		t.Errorf("自己診断の失敗を state で送り続ける: %+v", st)
	}
	if c.Status().DiagError == "" {
		t.Error("GUIにも理由を出す")
	}
}

func TestFaultsNewestFirstAndCapped(t *testing.T) {
	c := newTestController(t)
	for i := 0; i < 130; i++ {
		c.addFault(supervisor.Fault{Target: "ffmpeg", Reason: string(rune('a' + i%26)), Time: time.Unix(int64(i), 0)})
	}
	f := c.Faults()
	if len(f) != maxFaults {
		t.Fatalf("直近%d件: %d", maxFaults, len(f))
	}
	if f[0].Time.Unix() != 129 || f[len(f)-1].Time.Unix() != 30 {
		t.Errorf("新しい順: %d ... %d", f[0].Time.Unix(), f[len(f)-1].Time.Unix())
	}
}

// 前の ffmpeg が最後のハートビートの後に送ったフレームを、新しい ffmpeg のフレームと取り違えない。
// 取り違えると、キーフレームを待っている間に running へ進み、ウォッチドッグ(3秒)で止めてしまう。
func TestFFmpegReadyIgnoresFramesOfPreviousFFmpeg(t *testing.T) {
	c := newTestController(t)
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }
	c.onHeartbeat(ipc.NewHeartbeat(30, 10, 100))

	// 前の ffmpeg は、このハートビートの後もフレームを送ってから止まった(プレイヤーの累計は 115)。
	// ffmpeg を起動(制御が知っている累計は 100 のまま)
	c.mu.Lock()
	c.ffBase = c.player.FramesTotal
	c.ffBasePending = true
	c.mu.Unlock()
	if c.ffmpegReady() {
		t.Fatal("起動直後は準備完了ではない")
	}
	now = now.Add(time.Second)
	c.onHeartbeat(ipc.NewHeartbeat(5, 900, 115)) // 前の ffmpeg の残り
	if c.ffmpegReady() {
		t.Error("前の ffmpeg のフレームで準備完了にしない")
	}
	now = now.Add(time.Second)
	c.onHeartbeat(ipc.NewHeartbeat(0, 1900, 115)) // キーフレーム待ち
	if c.ffmpegReady() {
		t.Error("フレームが増えない間は準備完了ではない")
	}
	now = now.Add(time.Second)
	c.onHeartbeat(ipc.NewHeartbeat(30, 10, 140))
	if !c.ffmpegReady() {
		t.Error("新しい ffmpeg のフレームで準備完了")
	}
}
