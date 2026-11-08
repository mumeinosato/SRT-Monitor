package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"srtrx/internal/config"
	"srtrx/internal/controller"
	"srtrx/internal/ipc"
	"srtrx/internal/player"
	"srtrx/internal/supervisor"
)

// ---- 共通の部品 ----

// newCard は、小さな見出しつきのカードを返す(中身は同じ箱へ足す)。
func newCard(title string) *gtk.Box {
	card := gtk.NewBox(gtk.OrientationVertical, 10)
	card.AddCSSClass("card")
	if title != "" {
		t := gtk.NewLabel(title)
		t.AddCSSClass("card-title")
		t.SetXAlign(0)
		card.Append(t)
	}
	return card
}

func newLabel(text string, classes ...string) *gtk.Label {
	l := gtk.NewLabel(text)
	l.SetXAlign(0)
	l.SetWrap(true)
	for _, c := range classes {
		l.AddCSSClass(c)
	}
	return l
}

// setPill はピルの文言と色(pill-<kind>)を差し替える。
func setPill(l *gtk.Label, text, kind string, prev *string) {
	l.SetText(text)
	if *prev == kind {
		return
	}
	if *prev != "" {
		l.RemoveCSSClass("pill-" + *prev)
	}
	l.AddCSSClass("pill-" + kind)
	*prev = kind
}

func newPill() *gtk.Label {
	l := gtk.NewLabel("--")
	l.AddCSSClass("pill")
	l.SetHAlign(gtk.AlignStart)
	return l
}

// kvRow は「項目名 … 値」の1行。
func kvRow(box *gtk.Box, key string) *gtk.Label {
	row := gtk.NewBox(gtk.OrientationHorizontal, 12)
	k := newLabel(key, "kv-key")
	k.SetWidthChars(10)
	k.SetMaxWidthChars(10)
	k.SetYAlign(0)
	v := newLabel("--", "kv-val")
	v.SetHExpand(true)
	v.SetSelectable(true)
	row.Append(k)
	row.Append(v)
	box.Append(row)
	return v
}

// ---- 状態画面 ----

type statusPage struct {
	app  *App
	root *gtk.ScrolledWindow

	mtxPill, ffPill, plPill       *gtk.Label
	mtxK, ffK, plK                string
	mtxSince, mtxRestarts, mtxErr *gtk.Label
	ffSince, ffRestarts, ffErr    *gtk.Label
	decode, diag                  *gtk.Label
	fps                           *gtk.Label
	plPID, plAge                  *gtk.Label

	statGrid *gtk.Grid
	stats    []*gtkStat
	statIDs  string
	banner   *gtk.Label
	bannerK  string
}

type gtkStat struct {
	box          *gtk.Box
	title, value *gtk.Label
	color        string
}

func newStatusPage(a *App) *statusPage {
	p := &statusPage{app: a}
	body := gtk.NewBox(gtk.OrientationVertical, 14)
	body.SetMarginTop(18)
	body.SetMarginBottom(18)
	body.SetMarginStart(20)
	body.SetMarginEnd(20)

	// 1段目: 3つのカード
	row := gtk.NewBox(gtk.OrientationHorizontal, 14)
	row.SetHomogeneous(true)

	mb := newCard("MediaMTX(同梱)")
	p.mtxPill = newPill()
	mb.Append(p.mtxPill)
	p.mtxSince = kvRow(mb, "経過")
	p.mtxRestarts = kvRow(mb, "再起動")
	p.mtxErr = kvRow(mb, "最後のエラー")
	row.Append(mb)

	fb := newCard("ffmpeg")
	p.ffPill = newPill()
	fb.Append(p.ffPill)
	p.ffSince = kvRow(fb, "経過")
	p.ffRestarts = kvRow(fb, "再起動")
	p.ffErr = kvRow(fb, "最後のエラー")
	p.decode = kvRow(fb, "デコード")
	p.diag = kvRow(fb, "自己診断")
	row.Append(fb)

	pb := newCard("プレイヤー")
	p.plPill = newPill()
	pb.Append(p.plPill)
	p.fps = newLabel("--", "big-num")
	pb.Append(p.fps)
	pb.Append(newLabel("実測FPS", "dim"))
	p.plPID = kvRow(pb, "プロセス")
	p.plAge = kvRow(pb, "最新フレーム")
	row.Append(pb)
	body.Append(row)

	// 2段目: 統計(リセットからの累計)
	body.Append(newLabel("統計(リセットからの累計)", "section-title"))
	p.statGrid = gtk.NewGrid()
	p.statGrid.SetColumnSpacing(12)
	p.statGrid.SetRowSpacing(12)
	p.statGrid.SetColumnHomogeneous(true)
	body.Append(p.statGrid)

	// 帯(自己診断の失敗、ポート使用中、設定の読み込み)
	p.banner = newLabel("", "banner")
	body.Append(p.banner)

	// 操作
	btns := gtk.NewBox(gtk.OrientationHorizontal, 10)
	btns.SetMarginTop(4)
	mk := func(label string, f func(), classes ...string) {
		b := gtk.NewButtonWithLabel(label)
		b.AddCSSClass("btn")
		for _, c := range classes {
			b.AddCSSClass(c)
		}
		b.ConnectClicked(f)
		btns.Append(b)
	}
	mk("開始", a.ctl.StartAll, "btn-primary")
	mk("再起動", a.ctl.RestartAll)
	mk("停止", a.ctl.StopAll, "btn-danger")
	spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
	spacer.SetHExpand(true)
	btns.Append(spacer)
	mk("カウンターをリセット  Ctrl+R", a.ctl.ResetCounters)
	body.Append(btns)

	p.root = gtk.NewScrolledWindow()
	p.root.SetChild(body)
	p.root.SetHExpand(true)
	p.root.SetVExpand(true)
	return p
}

var stateJP = map[string]string{
	supervisor.Starting: "● 接続中",
	supervisor.Running:  "● 動作中",
	supervisor.Backoff:  "● 再起動待ち",
	supervisor.Stopped:  "● 停止中",
}

func since(s supervisor.Snapshot, now time.Time) string {
	t := fmtDur(now.Sub(s.Since))
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

	// MediaMTX
	if st.MediaMTXEnabled {
		setPill(p.mtxPill, stateJP[st.MediaMTX.State], st.MediaMTX.State, &p.mtxK)
		p.mtxSince.SetText(since(st.MediaMTX, now))
		p.mtxRestarts.SetText(fmt.Sprintf("%d 回", st.MediaMTX.RestartCount))
		e := orDash(st.MediaMTX.LastError)
		if st.PortInUse {
			e = "ポート使用中(外部のMediaMTXが動いている可能性)"
		}
		p.mtxErr.SetText(e)
	} else {
		setPill(p.mtxPill, "● 外部を使用", "stopped", &p.mtxK)
		p.mtxSince.SetText("--")
		p.mtxRestarts.SetText("--")
		p.mtxErr.SetText("同梱は使わない設定")
	}

	// ffmpeg
	setPill(p.ffPill, stateJP[st.FFmpeg.State], st.FFmpeg.State, &p.ffK)
	p.ffSince.SetText(since(st.FFmpeg, now))
	p.ffRestarts.SetText(fmt.Sprintf("%d 回", st.FFmpeg.RestartCount))
	p.ffErr.SetText(orDash(st.FFmpeg.LastError))
	hw := map[string]string{"none": "ソフトウェア", "vaapi": "VAAPI", "qsv": "QSV"}[st.Config.Video.HWAccel]
	if st.HWFallback {
		hw = "ソフトウェア(" + st.Config.Video.HWAccel + " の失敗が続いたため自動で切替)"
	}
	p.decode.SetText(hw)
	if st.DiagError != "" {
		p.diag.SetText("失敗")
	} else {
		p.diag.SetText("合格(ffmpeg " + st.FFmpegVersion + ")")
	}

	// プレイヤー
	if st.Player.Connected {
		setPill(p.plPill, "● 応答あり", "running", &p.plK)
		p.fps.SetText(fmt.Sprintf("%.1f", st.Player.FPS))
		p.plPID.SetText(fmt.Sprintf("pid %d", st.Player.PID))
		if st.Player.FrameAgeMS < 0 {
			p.plAge.SetText("まだ無い")
		} else {
			p.plAge.SetText(fmt.Sprintf("%d ms 前", st.Player.FrameAgeMS))
		}
	} else {
		setPill(p.plPill, "● 応答なし", "error", &p.plK)
		p.fps.SetText("--")
		p.plPID.SetText("--")
		p.plAge.SetText("--")
	}

	p.updateStats(st)
	p.updateBanner(st)
}

// updateStats はオーバーレイと同じ規則で、6枚の統計タイルを更新する。
func (p *statusPage) updateStats(st controller.Status) {
	snap := player.Snapshot{
		Cfg: config.Default(), HaveStats: true, HaveState: true, // 常に既定の6枚(オーバーレイの選択とは独立)
		Stats: ipc.Stats{OK: st.Stats.OK, Connected: st.Stats.Connected, Drop: st.Stats.Drop, Loss: st.Stats.Loss,
			Retrans: st.Stats.Retrans, FrameErrors: st.Stats.FrameErrors, RTTMS: st.Stats.RTTMS},
	}
	views := snap.Tiles(-1)
	ids := make([]string, len(views))
	for i, v := range views {
		ids[i] = v.ID
	}
	if key := strings.Join(ids, ","); key != p.statIDs {
		for _, s := range p.stats {
			p.statGrid.Remove(s.box)
		}
		p.stats = nil
		for i := range views {
			s := &gtkStat{box: gtk.NewBox(gtk.OrientationVertical, 2), title: newLabel("", "stat-title"), value: newLabel("", "stat-value")}
			s.box.AddCSSClass("stat")
			s.box.Append(s.title)
			s.box.Append(s.value)
			p.statGrid.Attach(s.box, i, 0, 1, 1)
			p.stats = append(p.stats, s)
		}
		p.statIDs = key
	}
	for i, v := range views {
		s := p.stats[i]
		s.title.SetText(v.Title)
		s.value.SetText(v.Value)
		if s.color != v.Color {
			if s.color != "" {
				s.box.RemoveCSSClass("stat-" + s.color)
			}
			s.box.AddCSSClass("stat-" + v.Color)
			s.color = v.Color
		}
	}
}

// updateBanner は、注意の要る事柄を1つだけ、帯で出す(重要なものを優先)。
func (p *statusPage) updateBanner(st controller.Status) {
	text, kind := "", ""
	switch {
	case st.DiagError != "":
		text, kind = "ffmpeg の自己診断に失敗: "+st.DiagError+"(10秒ごとに再診断します)", "error"
	case st.PortInUse:
		text, kind = "ポート使用中: 外部のMediaMTXが動いている可能性があります。他のプロセスは止めません。", "error"
	case !st.Player.Connected:
		text, kind = "プレイヤー応答なし。systemd が再起動します(制御はプレイヤーを止めません)。", "warn"
	case st.HWFallback:
		text, kind = "ハードウェアデコードの起動失敗が続いたため、ソフトウェアデコードで動いています。設定を適用すると戻ります。", "warn"
	case len(st.LoadNotes) > 0:
		text, kind = "設定の読み込み: "+strings.Join(st.LoadNotes, " / "), "ok"
	}
	p.banner.SetVisible(text != "")
	p.banner.SetText(text)
	if kind != p.bannerK {
		if p.bannerK != "" {
			p.banner.RemoveCSSClass("banner-" + p.bannerK)
		}
		if kind != "" {
			p.banner.AddCSSClass("banner-" + kind)
		}
		p.bannerK = kind
	}
}
