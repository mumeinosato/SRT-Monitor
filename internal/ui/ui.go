// Package ui は制御側のGTK4画面(状態、設定、ログ)。controller を呼ぶだけの薄い層。
package ui

import (
	"fmt"
	"os"
	"time"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"srtrx/internal/controller"
	"srtrx/internal/logx"
	"srtrx/internal/mediamtx"
)

const appCSS = `
.error { border: 2px solid #e03131; background: #fff0f0; color: #000; }
.warn-label { color: #e8590c; font-weight: bold; }
.err-label { color: #e03131; font-weight: bold; }
.ok-label { color: #2f9e44; font-weight: bold; }
.dim-label { color: #868e96; }
.state-running { color: #2f9e44; font-weight: bold; }
.state-starting { color: #f08c00; font-weight: bold; }
.state-backoff { color: #e8590c; font-weight: bold; }
.state-stopped { color: #868e96; font-weight: bold; }
.big { font-size: 15px; }
`

// App は制御GUI。
type App struct {
	ctl  *controller.Controller
	logs *logx.Logs
	app  *gtk.Application
	win  *gtk.ApplicationWindow

	status   *statusPage
	settings *settingsPage
	logPage  *logPage

	lockBtn *gtk.ToggleButton
	syncing bool
}

var current *gtk.Application

// Quit は制御GUIを終了させる(どのgoroutineからでもよい)。
func Quit() {
	glib.IdleAdd(func() {
		if current != nil {
			current.Quit()
		}
	})
}

// Run は制御GUIを動かし、終了コードを返す。
func Run(ctl *controller.Controller, logs *logx.Logs) int {
	app := gtk.NewApplication("jp.nc.srtrx.control", gio.ApplicationNonUnique)
	current = app
	a := &App{ctl: ctl, logs: logs, app: app}
	app.ConnectActivate(a.build)
	return app.Run(os.Args[:1])
}

func (a *App) build() {
	prov := gtk.NewCSSProvider()
	prov.LoadFromString(appCSS)
	gtk.StyleContextAddProviderForDisplay(gdk.DisplayGetDefault(), prov, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)

	a.win = gtk.NewApplicationWindow(a.app)
	a.win.SetTitle("srtrx 制御")
	a.win.SetDefaultSize(920, 720)

	// ヘッダー: 本番ロック、情報
	hb := gtk.NewHeaderBar()
	a.lockBtn = gtk.NewToggleButtonWithLabel("本番ロック")
	a.lockBtn.SetTooltipText("ロック中は設定を変更できません。解除は確認ダイアログで行います。")
	a.lockBtn.ConnectToggled(a.onLockToggled)
	hb.PackStart(a.lockBtn)
	info := gtk.NewButtonWithLabel("情報")
	info.ConnectClicked(a.showInfo)
	hb.PackEnd(info)
	a.win.SetTitlebar(hb)

	a.status = newStatusPage(a)
	a.settings = newSettingsPage(a)
	a.logPage = newLogPage(a)

	nb := gtk.NewNotebook()
	nb.AppendPage(a.status.root, gtk.NewLabel("状態"))
	nb.AppendPage(a.settings.root, gtk.NewLabel("設定"))
	nb.AppendPage(a.logPage.root, gtk.NewLabel("ログ"))
	a.win.SetChild(nb)

	// Ctrl+R: 累計のリセット
	kc := gtk.NewEventControllerKey()
	kc.ConnectKeyPressed(func(keyval, _ uint, state gdk.ModifierType) bool {
		if state&gdk.ControlMask != 0 && (keyval == gdk.KEY_r || keyval == gdk.KEY_R) {
			a.ctl.ResetCounters()
			return true
		}
		return false
	})
	a.win.AddController(kc)

	glib.TimeoutAdd(500, func() bool { a.refresh(); return true })
	a.refresh()
	a.win.Present()
}

func (a *App) refresh() {
	st := a.ctl.Status()
	a.syncing = true
	a.lockBtn.SetActive(st.Locked)
	a.syncing = false
	a.status.update(st)
	a.settings.update(st)
	a.logPage.update()
}

func (a *App) onLockToggled() {
	if a.syncing {
		return
	}
	if a.lockBtn.Active() {
		if err := a.ctl.SetLock(true); err != nil {
			a.errorDialog("ロックできない", err.Error())
		}
		return
	}
	// 解除は確認ダイアログで行う
	a.syncing = true
	a.lockBtn.SetActive(true) // 確認が取れるまで、ロックのまま
	a.syncing = false
	confirmDialog(&a.win.Window, "本番ロックを解除しますか?",
		"解除すると、設定を変更できます。展示中の誤操作にご注意ください。", "解除", func() {
			if err := a.ctl.SetLock(false); err != nil {
				a.errorDialog("解除できない", err.Error())
			}
			a.refresh()
		})
}

func (a *App) showInfo() {
	w := gtk.NewWindow()
	w.SetTitle("情報")
	w.SetTransientFor(&a.win.Window)
	w.SetDefaultSize(640, 480)
	box := gtk.NewBox(gtk.OrientationVertical, 8)
	box.SetMarginTop(12)
	box.SetMarginBottom(12)
	box.SetMarginStart(12)
	box.SetMarginEnd(12)
	box.Append(gtk.NewLabel(fmt.Sprintf("同梱: MediaMTX v%s(MITライセンス)", mediamtx.Version())))
	tv := gtk.NewTextView()
	tv.SetEditable(false)
	tv.SetMonospace(true)
	tv.Buffer().SetText(mediamtx.License)
	sw := gtk.NewScrolledWindow()
	sw.SetVExpand(true)
	sw.SetChild(tv)
	box.Append(sw)
	w.SetChild(box)
	w.Present()
}

func (a *App) errorDialog(title, msg string) {
	infoDialog(&a.win.Window, title, msg)
}

// ---- 小さなモーダルダイアログ ----

func confirmDialog(parent *gtk.Window, title, msg, okLabel string, onOK func()) {
	w := gtk.NewWindow()
	w.SetTitle(title)
	w.SetModal(true)
	w.SetTransientFor(parent)
	w.SetResizable(false)
	box := gtk.NewBox(gtk.OrientationVertical, 12)
	box.SetMarginTop(16)
	box.SetMarginBottom(16)
	box.SetMarginStart(20)
	box.SetMarginEnd(20)
	h := gtk.NewLabel(title)
	h.AddCSSClass("big")
	box.Append(h)
	m := gtk.NewLabel(msg)
	m.SetWrap(true)
	m.SetMaxWidthChars(48)
	box.Append(m)
	row := gtk.NewBox(gtk.OrientationHorizontal, 8)
	row.SetHAlign(gtk.AlignEnd)
	cancel := gtk.NewButtonWithLabel("キャンセル")
	ok := gtk.NewButtonWithLabel(okLabel)
	ok.AddCSSClass("suggested-action")
	cancel.ConnectClicked(func() { w.Close() })
	ok.ConnectClicked(func() { w.Close(); onOK() })
	row.Append(cancel)
	row.Append(ok)
	box.Append(row)
	w.SetChild(box)
	cancel.GrabFocus()
	w.Present()
}

func infoDialog(parent *gtk.Window, title, msg string) {
	w := gtk.NewWindow()
	w.SetTitle(title)
	w.SetModal(true)
	w.SetTransientFor(parent)
	box := gtk.NewBox(gtk.OrientationVertical, 12)
	box.SetMarginTop(16)
	box.SetMarginBottom(16)
	box.SetMarginStart(20)
	box.SetMarginEnd(20)
	m := gtk.NewLabel(msg)
	m.SetWrap(true)
	m.SetMaxWidthChars(48)
	box.Append(m)
	ok := gtk.NewButtonWithLabel("閉じる")
	ok.SetHAlign(gtk.AlignEnd)
	ok.ConnectClicked(func() { w.Close() })
	box.Append(ok)
	w.SetChild(box)
	w.Present()
}

func fmtDur(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return fmt.Sprintf("%.1f秒", d.Seconds())
}
