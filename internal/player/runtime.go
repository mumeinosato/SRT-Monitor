package player

import (
	"context"
	"log/slog"
	"os"
	"time"

	"srtrx/internal/config"
	"srtrx/internal/frame"
	"srtrx/internal/ipc"
	"srtrx/internal/logx"
)

// Runtime はGUIに依存しない、プレイヤーの常駐部分。
// フレームの受信、制御ソケットの待ち受け、ハートビートの送信を担う。
type Runtime struct {
	Paths config.Paths
	Store *frame.Store
	Recv  *frame.Receiver
	Model *Model

	srv *ipc.Server

	// OnConfig は config を受け取ったとき(IPCのgoroutineから)呼ばれる。GUIはここでメインスレッドへ渡す。
	OnConfig func(ipc.Config)
}

// NewRuntime は設定ファイルを読んで(動画サイズ等は起動時に自分で読む)、受け口を作る。
func NewRuntime(paths config.Paths) (*Runtime, config.Config) {
	cfg, info := config.Load(paths.ConfigFile)
	for _, n := range info.Notes {
		slog.Warn("設定の読み込み", "note", n)
	}
	st := frame.NewStore(cfg.Video.Width, cfg.Video.Height, cfg.Video.PlayerBufferFrames, cfg.Video.FPS)
	return &Runtime{Paths: paths, Store: st, Recv: frame.NewReceiver(st), Model: NewModel(cfg)}, cfg
}

// Start は2つのソケット(+FIFO)を待ち受け、ハートビートを始める。
func (r *Runtime) Start(ctx context.Context) error {
	if err := r.Paths.EnsureDirs(); err != nil {
		return err
	}
	if _, err := r.Recv.ListenUnix(ctx, r.Paths.FrameSock); err != nil {
		return err
	}
	// unix プロトコルの無い ffmpeg 向けの予備。失敗しても致命的ではない。
	if err := r.Recv.RunFIFO(ctx, r.Paths.FrameFIFO); err != nil {
		slog.Warn("FIFO を作れない(unix ソケットのみで動く)", "err", err)
	}
	srv, err := ipc.Listen(r.Paths.ControlSock)
	if err != nil {
		return err
	}
	r.srv = srv
	go r.acceptLoop(ctx)
	go r.heartbeatLoop(ctx)
	go func() { <-ctx.Done(); srv.Close() }()
	return nil
}

func (r *Runtime) acceptLoop(ctx context.Context) {
	defer logx.Recover("player.acceptLoop")
	for {
		conn, err := r.srv.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("制御ソケットの accept に失敗", "err", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		slog.Info("制御が接続した")
		_ = conn.Send(ipc.NewHello(os.Getpid()))
		go r.serve(conn)
	}
}

func (r *Runtime) serve(conn *ipc.Conn) {
	defer logx.Recover("player.serve")
	defer conn.Close()
	for {
		msg, err := conn.Recv()
		if err != nil {
			if ipc.IsFatal(err) {
				slog.Warn("制御との接続が切れた", "err", err)
				return
			}
			slog.Warn("制御からの不正なメッセージを無視した", "err", err)
			continue
		}
		r.Model.Touch(time.Now())
		switch m := msg.(type) {
		case ipc.Config:
			r.applyVideo(m)
			r.Model.SetConfig(m)
			if r.OnConfig != nil {
				r.OnConfig(m)
			}
		case ipc.State:
			r.Model.SetState(m)
		case ipc.Stats:
			r.Model.SetStats(m)
		case ipc.CountersReset:
			// 累計は制御が持つ。リセット後の stats が直後に届く。
		case ipc.Unknown:
			slog.Warn("未知の type を無視した", "type", m.Type)
		}
	}
}

// applyVideo は動画の設定をストアへ反映する(サイズが変わればバッファを確保し直す)。
func (r *Runtime) applyVideo(m ipc.Config) {
	r.Store.Reset(m.Video.Width, m.Video.Height, m.Video.PlayerBufferFrames, m.Video.FPS)
}

// FrameAge は最後のフレームからの経過(まだ無ければ負)。
func (r *Runtime) FrameAge(now time.Time) time.Duration {
	t := r.Store.LastReceived()
	if t.IsZero() {
		return -1
	}
	return now.Sub(t)
}

func (r *Runtime) heartbeatLoop(ctx context.Context) {
	defer logx.Recover("player.heartbeatLoop")
	tk := time.NewTicker(time.Second)
	defer tk.Stop()
	last, lastT := r.Store.Total(), time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tk.C:
			total := r.Store.Total()
			fps := float64(total-last) / now.Sub(lastT).Seconds()
			last, lastT = total, now
			r.Model.SetFPS(fps)
			age := int64(-1)
			if a := r.FrameAge(now); a >= 0 {
				age = a.Milliseconds()
			}
			_ = r.srv.Send(ipc.NewHeartbeat(fps, age, total))
		}
	}
}
