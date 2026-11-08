package controller

import (
	"context"
	"log/slog"
	"time"

	"srtrx/internal/config"
	"srtrx/internal/ipc"
	"srtrx/internal/logx"
	"srtrx/internal/metrics"
	"srtrx/internal/supervisor"
)

// ---- プレイヤーとの通信 ----

func (c *Controller) setConn(conn *ipc.Conn) {
	c.ipcMu.Lock()
	c.ipcConn = conn
	c.ipcMu.Unlock()
}

func (c *Controller) sendIPC(msg any) {
	c.ipcMu.Lock()
	conn := c.ipcConn
	c.ipcMu.Unlock()
	if conn == nil {
		return
	}
	if err := conn.Send(msg); err != nil {
		slog.Debug("プレイヤーへ送れない", "err", err)
		conn.Close() // 受信側のループが終端を検知して再接続する
	}
}

// ipcLoop は制御ソケットへ接続し、切れたら1秒ごとに再接続する。
func (c *Controller) ipcLoop(ctx context.Context) {
	defer logx.Recover("controller.ipcLoop")
	done := ctx.Done()
	for {
		conn := ipc.Dial(c.paths.ControlSock, time.Second, done)
		if conn == nil {
			return
		}
		slog.Info("プレイヤーに接続した")
		c.setConn(conn)
		// 接続直後に config と現在の状態を送る
		c.sendIPC(ipc.NewConfig(c.Config()))
		c.pushState()
		c.sendStats(c.currentStats())
		c.recvLoop(ctx, conn)
		c.setConn(nil)
		conn.Close()
		slog.Warn("プレイヤーとの接続が切れた")
		select {
		case <-done:
			return
		case <-time.After(time.Second):
		}
	}
}

func (c *Controller) recvLoop(ctx context.Context, conn *ipc.Conn) {
	go func() { // ctx が終わったら読み取りを解除する
		<-ctx.Done()
		conn.Close()
	}()
	for {
		msg, err := conn.Recv()
		if err != nil {
			if ipc.IsFatal(err) {
				return
			}
			slog.Warn("プレイヤーからの不正なメッセージを無視した", "err", err)
			continue
		}
		switch m := msg.(type) {
		case ipc.Hello:
			slog.Info("プレイヤーの hello", "pid", m.PID, "version", m.Version)
			c.mu.Lock()
			c.player.PID = m.PID
			c.mu.Unlock()
		case ipc.Heartbeat:
			c.onHeartbeat(m)
		case ipc.Unknown:
			slog.Warn("未知の type を無視した", "type", m.Type)
		default:
			slog.Warn("想定外のメッセージを無視した", "msg", m)
		}
	}
}

func (c *Controller) onHeartbeat(h ipc.Heartbeat) {
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if h.FramesTotal < c.lastTotal { // プレイヤーが再起動した(frames_total が0から始まり直す)
		c.ffBase = 0
		c.lastFrame = now
	}
	if h.FramesTotal != c.lastTotal {
		c.lastFrame = now // 「フレームが来た」は frames_total が変わったこと(制御の時計で数える)
	}
	c.lastTotal = h.FramesTotal
	c.player.FPS, c.player.FrameAgeMS, c.player.FramesTotal = h.FPS, h.FrameAgeMS, h.FramesTotal
	c.player.LastBeat = now
}

// ---- 状態と統計の送信 ----

func (c *Controller) currentStats() metrics.Stats {
	c.ipcMu.Lock()
	defer c.ipcMu.Unlock()
	return c.stats
}

func (c *Controller) onStats(st metrics.Stats) {
	c.ipcMu.Lock()
	c.stats = st
	c.ipcMu.Unlock()
	c.sendStats(st)
}

func (c *Controller) sendStats(st metrics.Stats) {
	m := ipc.NewStats()
	m.OK, m.Connected = st.OK, st.Connected
	m.Drop, m.Loss, m.Retrans, m.FrameErrors, m.RTTMS = st.Drop, st.Loss, st.Retrans, st.FrameErrors, st.RTTMS
	c.sendIPC(m)
}

// State はプレイヤーへ送る state を作る。
// ただし同梱の MediaMTX が running でないときは backoff にし、detail へ "mediamtx: <状態>" を入れる。
// 自己診断に失敗している間も、プレイヤーへ送り続ける。
func (c *Controller) State() ipc.State {
	ff := c.ffSV.Snapshot()
	state, detail, since := ff.State, ff.LastError, ff.Since
	c.mu.Lock()
	enabled, diagErr := c.cfg.MediaMTX.Enabled, c.diagErr
	c.mu.Unlock()
	if enabled {
		if m := c.mtxSV.Snapshot(); m.State != supervisor.Running {
			state, detail, since = ipc.StateBackoff, "mediamtx: "+m.State, m.Since
			if ff.State == supervisor.Stopped {
				state = ipc.StateStopped
			}
		}
	}
	if diagErr != nil {
		state, detail = ipc.StateBackoff, "ffmpeg: "+diagErr.Error()
	}
	if state == ipc.StateRunning {
		detail = ""
	}
	return ipc.NewState(state, c.hw.Active(), ff.RestartCount, c.now().Sub(since), detail)
}

func (c *Controller) pushState() { c.sendIPC(c.State()) }

// stateLoop は state を1秒ごとに送る(プレイヤーの「制御切断」判定を支える)。
func (c *Controller) stateLoop(ctx context.Context) {
	defer logx.Recover("controller.stateLoop")
	tk := time.NewTicker(statePushEvery)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			c.pushState()
		}
	}
}

// ---- 設定の操作 ----

// RestartKey は「ffmpeg(と MediaMTX)の再起動が必要な設定」だけを取り出して比べるための値。
func RestartKey(g config.Config) config.Config {
	k := config.Default()
	k.Input = g.Input
	k.Video.Width, k.Video.Height, k.Video.HWAccel = g.Video.Width, g.Video.Height, g.Video.HWAccel
	k.Video.ExtraArgs = g.Video.ExtraArgs
	k.MediaMTX = g.MediaMTX
	return k
}

// PendingRestart は draft に、まだ適用していない「再起動が必要な設定」の変更があるか。
func (c *Controller) PendingRestart(draft config.Config) bool {
	cur := c.Config()
	a, b := RestartKey(cur), RestartKey(draft)
	return !equalCfg(a, b)
}

func equalCfg(a, b config.Config) bool {
	x, _ := config.Marshal(a)
	y, _ := config.Marshal(b)
	return string(x) == string(y)
}

// SetLive は「即時」の項目を変える。検証して保存し、すぐ反映する。再起動はしない。
func (c *Controller) SetLive(mutate func(*config.Config)) error {
	c.mu.Lock()
	if c.cfg.Lock.Enabled {
		c.mu.Unlock()
		return ErrLocked
	}
	n := c.cfg.Clone()
	c.mu.Unlock()
	mutate(&n)
	return c.commit(n, false)
}

// ApplyRestart は draft を保存して反映し、ffmpeg を再起動する(MediaMTX の設定が変わったときはそれも)。
func (c *Controller) ApplyRestart(draft config.Config) error {
	c.mu.Lock()
	if c.cfg.Lock.Enabled {
		c.mu.Unlock()
		return ErrLocked
	}
	old := c.cfg.Clone()
	c.mu.Unlock()
	return c.commit(draft, true, old)
}

// SetLock は本番ロックの入り切り(解除の確認はGUI側で行う)。
func (c *Controller) SetLock(on bool) error {
	c.mu.Lock()
	n := c.cfg.Clone()
	c.mu.Unlock()
	n.Lock.Enabled = on
	return c.commit(n, false)
}

func (c *Controller) commit(n config.Config, restart bool, olds ...config.Config) error {
	if err := n.Validate(); err != nil {
		return err
	}
	if err := config.Save(c.paths.ConfigFile, n); err != nil {
		return err
	}
	c.mu.Lock()
	old := c.cfg
	c.cfg = n.Clone()
	c.mu.Unlock()
	if len(olds) > 0 {
		old = olds[0]
	}

	eff := n.Effective()
	if c.poll != nil {
		c.poll.SetConfig(eff.Metrics.URL, time.Duration(n.Metrics.IntervalMS)*time.Millisecond,
			time.Duration(n.Metrics.TimeoutMS)*time.Millisecond)
	}
	c.sendIPC(ipc.NewConfig(n))

	if restart {
		c.hw.Reset() // 設定の適用で、ハードウェアデコードの自動フォールバックを解除する
		c.mu.Lock()
		c.portInUse = false
		c.mu.Unlock()
		mtxChanged := !equalCfg(configMediaMTXOnly(old), configMediaMTXOnly(n))
		switch {
		case n.MediaMTX.Enabled && mtxChanged:
			c.mtxSV.Restart() // ffmpeg は gate で先に止まり、準備完了のあとで起動し直す
			c.ffSV.Restart()
		case !n.MediaMTX.Enabled && old.MediaMTX.Enabled:
			c.mtxSV.Stop()
			c.ffSV.Restart()
		case n.MediaMTX.Enabled && !old.MediaMTX.Enabled:
			c.mtxSV.Start()
			c.ffSV.Restart()
		default:
			c.ffSV.Restart()
		}
	}
	c.pushState()
	return nil
}

func configMediaMTXOnly(g config.Config) config.Config {
	k := config.Default()
	k.MediaMTX = g.MediaMTX
	return k
}

// SaveProfile は現在の設定をプロファイルとして保存する。
func (c *Controller) SaveProfile(name string) error {
	return config.SaveProfile(c.paths.ConfigFile, name, c.Config())
}

// LoadProfile はプロファイルを読んで返す(適用は ApplyRestart で)。
func (c *Controller) LoadProfile(name string) (config.Config, error) {
	return config.LoadProfile(c.paths.ConfigFile, name)
}

// Profiles は保存済みのプロファイル名。
func (c *Controller) Profiles() []string { return config.ListProfiles(c.paths.ConfigFile) }
