package player

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"srtrx/internal/config"
	"srtrx/internal/ipc"
)

var colors = []string{ColorNeutral, ColorGreen, ColorRed, ColorOrange, ColorBlue, ColorGray, ColorYellow}

// css はタイルの大きさ(tile_scale)を反映したスタイル。色と角丸はCSSで指定する。
func css(scale float64) string {
	px := func(v float64) string { return fmt.Sprintf("%.1fpx", v*scale) }
	return fmt.Sprintf(`
window.player { background: #000; }
.tiles { padding: %s; }
.tile { background: rgba(14,16,21,0.88); border: 1px solid rgba(255,255,255,0.09); border-top: %s solid rgba(255,255,255,0.18);
        border-radius: %s; padding: %s %s; margin: 0 %s; min-width: %s; box-shadow: 0 4px 16px rgba(0,0,0,0.45); }
.tile .title { font-size: %s; font-weight: 700; letter-spacing: 1px; color: rgba(255,255,255,0.55); }
.tile .value { font-size: %s; font-weight: 700; color: #f4f6fa; font-feature-settings: "tnum"; }
.tile-green { border-top-color: #34d399; } .tile-green .value { color: #34d399; }
.tile-red { border-top-color: #f87171; } .tile-red .value { color: #f87171; }
.tile-orange { border-top-color: #fb923c; } .tile-orange .value { color: #fb923c; }
.tile-blue { border-top-color: #60a5fa; } .tile-blue .value { color: #60a5fa; }
.tile-gray { border-top-color: rgba(255,255,255,0.18); } .tile-gray .value { color: rgba(255,255,255,0.45); }
.tile-yellow { border-top-color: #fbbf24; } .tile-yellow .value { color: #fbbf24; }
.badge { background: rgba(14,16,21,0.85); border: 1px solid rgba(255,255,255,0.12); border-radius: 999px;
         padding: 5px 18px; font-size: 18px; font-weight: 700; box-shadow: 0 4px 16px rgba(0,0,0,0.5); }
.badge-yellow { color: #fbbf24; }
.badge-orange { color: #fb923c; }
.badge-gray { color: #b4bac6; }
.badge-neutral { color: #f4f6fa; }
.sw { background: rgba(14,16,21,0.85); border: 1px solid rgba(255,255,255,0.12); border-radius: 999px;
      padding: 2px 9px; font-size: 11px; font-weight: 700; color: #b4bac6; margin-left: 6px; }
.ctl-lost { margin: 10px; font-size: 14px; padding: 4px 14px; }
`, px(6), px(2), px(10), px(6), px(10), px(3), px(104), px(9), px(19))
}

type tileWidget struct {
	box          *gtk.Box
	title, value *gtk.Label
	color        string
}

type gui struct {
	rt  *Runtime
	app *gtk.Application
	win *gtk.ApplicationWindow
	pic *gtk.Picture

	tilesBox *gtk.Box
	tiles    []*tileWidget
	tileIDs  string

	badgeBox *gtk.Box
	badge    *gtk.Label
	sw       *gtk.Label
	badgeCol string
	ctlLost  *gtk.Label
	lostCol  string

	cssProv  *gtk.CSSProvider
	cssScale float64

	showOverlay bool // ホットキーで切り替える実行時の状態
	hotkey      rune

	lastSeq      uint64
	dst          []byte // 受け取りバッファ。最後に受け取ったフレームが残る(無信号表示から戻るときに使う)
	hasFrame     bool
	lastW, lastH int
	noSigTex     gdk.Paintabler
	noSigPath    string
	showingNS    bool

	applied appliedDisplay
}

type appliedDisplay struct {
	monitor    string
	fullscreen bool
	set        bool
}

var currentApp *gtk.Application

// Quit はアプリを終了させる(どのgoroutineからでもよい)。
func Quit() {
	glib.IdleAdd(func() {
		if currentApp != nil {
			currentApp.Quit()
		}
	})
}

// Run はプレイヤーのGTKアプリを動かし、終了コードを返す。
func Run(rt *Runtime) int {
	app := gtk.NewApplication("jp.nc.srtrx.player", gio.ApplicationNonUnique)
	currentApp = app
	g := &gui{rt: rt, app: app, showOverlay: true}
	app.ConnectActivate(func() { g.build() })
	return app.Run(os.Args[:1])
}

func (g *gui) build() {
	g.win = gtk.NewApplicationWindow(g.app)
	g.win.SetTitle("srtrx player")
	g.win.SetDefaultSize(960, 540)
	g.win.AddCSSClass("player")

	g.pic = gtk.NewPicture()
	g.pic.SetCanShrink(true)
	g.pic.SetContentFit(gtk.ContentFitContain)
	g.pic.SetHExpand(true)
	g.pic.SetVExpand(true)

	ov := gtk.NewOverlay()
	ov.SetChild(g.pic)

	g.tilesBox = gtk.NewBox(gtk.OrientationHorizontal, 0)
	g.tilesBox.AddCSSClass("tiles")
	g.tilesBox.SetHAlign(gtk.AlignCenter)
	g.tilesBox.SetVAlign(gtk.AlignStart)
	ov.AddOverlay(g.tilesBox)

	g.badgeBox = gtk.NewBox(gtk.OrientationHorizontal, 0)
	g.badgeBox.SetHAlign(gtk.AlignCenter)
	g.badgeBox.SetVAlign(gtk.AlignEnd)
	g.badgeBox.SetMarginBottom(18)
	g.badge = gtk.NewLabel("")
	g.badge.AddCSSClass("badge")
	g.sw = gtk.NewLabel("SW")
	g.sw.AddCSSClass("sw")
	g.badgeBox.Append(g.badge)
	g.badgeBox.Append(g.sw)
	g.badgeBox.SetVisible(false)
	ov.AddOverlay(g.badgeBox)

	g.ctlLost = gtk.NewLabel("Control lost")
	g.ctlLost.AddCSSClass("badge")
	g.ctlLost.AddCSSClass("ctl-lost")
	g.ctlLost.SetHAlign(gtk.AlignEnd)
	g.ctlLost.SetVAlign(gtk.AlignEnd)
	g.ctlLost.SetVisible(false)
	ov.AddOverlay(g.ctlLost)

	g.win.SetChild(ov)

	g.cssProv = gtk.NewCSSProvider()
	g.cssScale = -1
	gtk.StyleContextAddProviderForDisplay(gdk.DisplayGetDefault(), g.cssProv, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)

	// ホットキー: オーバーレイ全体の表示を切り替える
	kc := gtk.NewEventControllerKey()
	kc.ConnectKeyPressed(func(keyval, _ uint, _ gdk.ModifierType) bool {
		if g.hotkey != 0 && rune(gdk.KeyvalToUnicode(keyval)) == g.hotkey {
			g.showOverlay = !g.showOverlay
			g.refresh()
			return true
		}
		return false
	})
	g.win.AddController(kc)

	// 描画はGTKのフレームクロックで、最新のフレームだけを取る
	g.pic.AddTickCallback(func(_ gtk.Widgetter, _ gdk.FrameClocker) bool {
		g.tick()
		return true
	})
	glib.TimeoutAdd(200, func() bool { g.refresh(); return true })

	g.rt.OnConfig = func(ipc.Config) { glib.IdleAdd(func() { g.refresh() }) }

	g.win.Present()
	g.refresh()
}

// tick はメインスレッドで、フレームごとに呼ばれる。
// 最新の1枚だけを取り、前回と同じなら何もしない。
// 受け取りバッファ(g.dst)は使い回す。setFrame がCへコピーするので、取り出し直す必要はない。
func (g *gui) tick() {
	w, h := g.rt.Store.Size()
	if need := w * h * 3; len(g.dst) != need {
		g.dst, g.lastSeq = make([]byte, need), 0
	}
	seq, changed := g.rt.Store.Take(g.dst, g.lastSeq, time.Now())
	if !changed {
		return
	}
	if w2, h2 := g.rt.Store.Size(); w2 != w || h2 != h {
		return // 取り出す間にサイズが変わった。次のフレームで取り直す
	}
	g.lastSeq = seq
	g.hasFrame, g.lastW, g.lastH = true, w, h
	if !g.showingNS {
		setFrame(coreglib.BaseObject(g.pic).Native(), g.dst, w, h)
	}
}

// refresh はタイル、バッジ、表示設定を、いまのモデルに合わせる(メインスレッド)。
func (g *gui) refresh() {
	now := time.Now()
	snap := g.rt.Model.Snapshot(now)
	age := g.rt.FrameAge(now)
	g.hotkey = firstRune(snap.Cfg.Overlay.Hotkey)

	if g.cssScale != snap.Cfg.Overlay.TileScale {
		g.cssScale = snap.Cfg.Overlay.TileScale
		g.cssProv.LoadFromString(css(g.cssScale))
	}
	g.applyDisplay(snap.Cfg.Display)
	g.applyNoSignal(snap)

	// タイル
	ov := snap.Cfg.Overlay
	g.tilesBox.SetVisible(ov.Enabled && g.showOverlay)
	g.tilesBox.SetOpacity(ov.Opacity)
	if ov.Position == "bottom" {
		g.tilesBox.SetVAlign(gtk.AlignEnd)
	} else {
		g.tilesBox.SetVAlign(gtk.AlignStart)
	}
	g.updateTiles(snap.Tiles(age))
	// 下端に出すタイルとバッジが重ならないよう、バッジを上へ逃がす
	if ov.Position == "bottom" && ov.Enabled && g.showOverlay {
		g.badgeBox.SetMarginBottom(90)
	} else {
		g.badgeBox.SetMarginBottom(18)
	}

	// バッジ
	b := snap.StateBadge(age)
	g.badgeBox.SetVisible(b.Text != "")
	if b.Text != "" {
		g.badge.SetText(b.Text)
		setColorClass(g.badge, "badge-", &g.badgeCol, b.Color)
		g.sw.SetVisible(b.SW)
	}
	cb := snap.ControlLostBadge()
	g.ctlLost.SetVisible(cb.Text != "")
	if cb.Text != "" {
		setColorClass(g.ctlLost, "badge-", &g.lostCol, cb.Color)
	}
}

func firstRune(s string) rune {
	for _, r := range s {
		return r
	}
	return 0
}

func setColorClass(w interface {
	AddCSSClass(string)
	RemoveCSSClass(string)
}, prefix string, cur *string, color string) {
	if *cur == color {
		return
	}
	if *cur != "" {
		w.RemoveCSSClass(prefix + *cur)
	}
	w.AddCSSClass(prefix + color)
	*cur = color
}

func (g *gui) updateTiles(views []TileView) {
	ids := make([]string, len(views))
	for i, v := range views {
		ids[i] = v.ID
	}
	key := strings.Join(ids, ",")
	if key != g.tileIDs { // 選択や順序が変わった: 作り直す
		for _, t := range g.tiles {
			g.tilesBox.Remove(t.box)
		}
		g.tiles = nil
		for range views {
			t := &tileWidget{box: gtk.NewBox(gtk.OrientationVertical, 2), title: gtk.NewLabel(""), value: gtk.NewLabel("")}
			t.box.AddCSSClass("tile")
			t.title.AddCSSClass("title")
			t.value.AddCSSClass("value")
			t.box.Append(t.title)
			t.box.Append(t.value)
			g.tilesBox.Append(t.box)
			g.tiles = append(g.tiles, t)
		}
		g.tileIDs = key
	}
	for i, v := range views {
		t := g.tiles[i]
		t.title.SetText(v.Title)
		t.value.SetText(v.Value)
		setColorClass(t.box, "tile-", &t.color, v.Color)
	}
}

// applyDisplay は表示するモニターと全画面を反映する(変わったときだけ)。
func (g *gui) applyDisplay(d config.Display) {
	cur := appliedDisplay{monitor: d.Monitor, fullscreen: d.Fullscreen, set: true}
	if g.applied == cur {
		return
	}
	g.applied = cur
	if !d.Fullscreen {
		g.win.Unfullscreen()
		g.win.SetCursor(nil)
		return
	}
	if mon := findMonitor(d.Monitor); mon != nil {
		g.win.FullscreenOnMonitor(mon)
	} else {
		if d.Monitor != "primary" {
			slog.Warn("指定のモニターが見つからないので、既定のモニターで全画面にする", "monitor", d.Monitor)
		}
		g.win.Fullscreen()
	}
	g.win.SetCursorFromName("none") // 展示では、マウスカーソルを出さない
}

// findMonitor は connector 名(例 HDMI-A-1)でモニターを探す。"primary" なら nil。
func findMonitor(name string) *gdk.Monitor {
	if name == "" || name == "primary" {
		return nil
	}
	disp := gdk.DisplayGetDefault()
	if disp == nil {
		return nil
	}
	list := disp.Monitors()
	for i := uint(0); i < list.NItems(); i++ {
		obj := list.Item(i)
		if mon, ok := obj.Cast().(*gdk.Monitor); ok && mon.Connector() == name {
			return mon
		}
	}
	return nil
}

// applyNoSignal は、無信号時の表示(最後のフレーム、黒、画像)を切り替える。
func (g *gui) applyNoSignal(s Snapshot) {
	want := s.ShowNoSignal()
	if want == g.showingNS && (!want || g.noSigPath == s.Cfg.Display.NoSignalImage || s.Cfg.Display.NoSignal != "image") {
		return
	}
	g.showingNS = want
	switch {
	case !want:
		if g.hasFrame {
			setFrame(coreglib.BaseObject(g.pic).Native(), g.dst, g.lastW, g.lastH) // 最後のフレームへ戻す
		}
	case s.Cfg.Display.NoSignal == "image":
		if g.noSigTex == nil || g.noSigPath != s.Cfg.Display.NoSignalImage {
			g.noSigPath = s.Cfg.Display.NoSignalImage
			tex, err := gdk.NewTextureFromFilename(g.noSigPath)
			if err != nil {
				slog.Error("無信号画像を読めない", "path", g.noSigPath, "err", err)
				g.noSigTex = nil
			} else {
				g.noSigTex = tex
			}
		}
		g.pic.SetPaintable(g.noSigTex) // 読めなければ nil = 黒
	default:
		g.pic.SetPaintable(nil) // 黒
	}
}
