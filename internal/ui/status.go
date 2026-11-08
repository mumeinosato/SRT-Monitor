package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"srtrx/internal/controller"
	"srtrx/internal/supervisor"
)

type statusPage struct {
	app  *App
	root *gtk.Box

	mtxState, mtxRestarts, mtxErr *gtk.Label
	ffState, ffRestarts, ffErr    *gtk.Label
	hw, diag, notes               *gtk.Label
	playerState, playerFPS, age   *gtk.Label
	conn, drop, loss, retrans     *gtk.Label
	frameErr, rtt                 *gtk.Label

	mtxRow []gtk.Widgetter
}

func newStatusPage(a *App) *statusPage {
	p := &statusPage{app: a}
	p.root = gtk.NewBox(gtk.OrientationVertical, 12)
	p.root.SetMarginTop(16)
	p.root.SetMarginBottom(16)
	p.root.SetMarginStart(16)
	p.root.SetMarginEnd(16)

	grid := gtk.NewGrid()
	grid.SetRowSpacing(6)
	grid.SetColumnSpacing(16)
	row := 0
	section := func(title string) {
		l := gtk.NewLabel(title)
		l.AddCSSClass("big")
		l.SetXAlign(0)
		l.SetMarginTop(8)
		grid.Attach(l, 0, row, 2, 1)
		row++
	}
	kv := func(key string) *gtk.Label {
		k := gtk.NewLabel(key)
		k.SetXAlign(0)
		k.AddCSSClass("dim-label")
		v := gtk.NewLabel("--")
		v.SetXAlign(0)
		v.SetSelectable(true)
		v.SetWrap(true)
		v.SetHExpand(true)
		grid.Attach(k, 0, row, 1, 1)
		grid.Attach(v, 1, row, 1, 1)
		row++
		return v
	}

	section("MediaMTX(同梱)")
	p.mtxState = kv("状態")
	p.mtxRestarts = kv("再起動回数")
	p.mtxErr = kv("最後のエラー")
	section("ffmpeg")
	p.ffState = kv("状態")
	p.ffRestarts = kv("再起動回数")
	p.ffErr = kv("最後のエラー")
	p.hw = kv("デコード")
	p.diag = kv("自己診断")
	section("プレイヤー")
	p.playerState = kv("応答")
	p.playerFPS = kv("実測FPS")
	p.age = kv("最新フレームからの経過")
	section("統計(リセットからの累計)")
	p.conn = kv("接続状態")
	p.drop = kv("ドロップ")
	p.loss = kv("ロス")
	p.retrans = kv("再送受信")
	p.frameErr = kv("フレームエラー")
	p.rtt = kv("RTT")
	p.notes = kv("設定の読み込み")
	p.root.Append(grid)

	btns := gtk.NewBox(gtk.OrientationHorizontal, 8)
	for _, b := range []struct {
		label string
		f     func()
	}{
		{"開始", a.ctl.StartAll},
		{"停止", a.ctl.StopAll},
		{"再起動", a.ctl.RestartAll},
		{"カウンターをリセット (Ctrl+R)", a.ctl.ResetCounters},
	} {
		b := b
		btn := gtk.NewButtonWithLabel(b.label)
		btn.ConnectClicked(b.f)
		btns.Append(btn)
	}
	p.root.Append(btns)

	sw := gtk.NewScrolledWindow()
	sw.SetChild(p.root)
	// root をそのまま返すため、スクロールは付けない(内容が少ない)
	return p
}

func stateClass(l *gtk.Label, state string) {
	for _, s := range []string{supervisor.Starting, supervisor.Running, supervisor.Backoff, supervisor.Stopped} {
		l.RemoveCSSClass("state-" + s)
	}
	l.AddCSSClass("state-" + state)
}

var stateJP = map[string]string{
	supervisor.Starting: "接続中(starting)",
	supervisor.Running:  "動作中(running)",
	supervisor.Backoff:  "再起動待ち(backoff)",
	supervisor.Stopped:  "停止中(stopped)",
}

func snapText(s supervisor.Snapshot, now time.Time) string {
	t := stateJP[s.State] + " — " + fmtDur(now.Sub(s.Since))
	if s.GateWait {
		t += "(前提の準備待ち)"
	}
	return t
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "--"
	}
	return s
}

func (p *statusPage) update(st controller.Status) {
	now := time.Now()
	if st.MediaMTXEnabled {
		p.mtxState.SetText(snapText(st.MediaMTX, now))
		stateClass(p.mtxState, st.MediaMTX.State)
		p.mtxRestarts.SetText(fmt.Sprint(st.MediaMTX.RestartCount))
		e := orDash(st.MediaMTX.LastError)
		if st.PortInUse {
			e = "ポート使用中(外部のMediaMTXが動いている可能性)"
			p.mtxErr.AddCSSClass("err-label")
		} else {
			p.mtxErr.RemoveCSSClass("err-label")
		}
		p.mtxErr.SetText(e)
	} else {
		p.mtxState.SetText("使わない(外部のMediaMTXを使う設定)")
		p.mtxRestarts.SetText("--")
		p.mtxErr.SetText("--")
	}

	p.ffState.SetText(snapText(st.FFmpeg, now))
	stateClass(p.ffState, st.FFmpeg.State)
	p.ffRestarts.SetText(fmt.Sprint(st.FFmpeg.RestartCount))
	p.ffErr.SetText(orDash(st.FFmpeg.LastError))
	hw := st.Config.Video.HWAccel
	switch {
	case st.HWFallback:
		p.hw.SetText("ソフトウェア(" + hw + " の起動失敗が続いたため自動で切り替えた)")
		p.hw.AddCSSClass("warn-label")
	default:
		p.hw.SetText(map[string]string{"none": "ソフトウェア", "vaapi": "VAAPI", "qsv": "QSV"}[hw])
		p.hw.RemoveCSSClass("warn-label")
	}
	if st.DiagError != "" {
		p.diag.SetText("失敗: " + st.DiagError + "(10秒ごとに再診断)")
		p.diag.AddCSSClass("err-label")
		p.diag.RemoveCSSClass("ok-label")
	} else {
		p.diag.SetText("合格(ffmpeg " + st.FFmpegVersion + ")")
		p.diag.AddCSSClass("ok-label")
		p.diag.RemoveCSSClass("err-label")
	}

	if st.Player.Connected {
		p.playerState.SetText(fmt.Sprintf("応答あり(pid %d)", st.Player.PID))
		p.playerState.RemoveCSSClass("err-label")
		p.playerFPS.SetText(fmt.Sprintf("%.2f", st.Player.FPS))
		if st.Player.FrameAgeMS < 0 {
			p.age.SetText("まだフレームが無い")
		} else {
			p.age.SetText(fmt.Sprintf("%d ms", st.Player.FrameAgeMS))
		}
	} else {
		p.playerState.SetText("プレイヤー応答なし")
		p.playerState.AddCSSClass("err-label")
		p.playerFPS.SetText("--")
		p.age.SetText("--")
	}

	s := st.Stats
	if !s.OK {
		for _, l := range []*gtk.Label{p.conn, p.rtt} {
			l.SetText("--")
		}
		p.conn.SetText("-- (統計を取得できない)")
	} else if s.Connected {
		p.conn.SetText("接続中")
		p.rtt.SetText(fmt.Sprintf("%.2f ms", s.RTTMS))
	} else {
		p.conn.SetText("切断")
		p.rtt.SetText("--")
	}
	p.drop.SetText(fmt.Sprint(s.Drop))
	p.loss.SetText(fmt.Sprint(s.Loss))
	p.retrans.SetText(fmt.Sprint(s.Retrans))
	p.frameErr.SetText(fmt.Sprint(s.FrameErrors))
	p.notes.SetText(orDash(strings.Join(st.LoadNotes, " / ")))
}
