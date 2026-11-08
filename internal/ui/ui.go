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

// appCSS はモダンな暗色テーマ。色は 1か所(:root 相当の @define-color)に集める。
const appCSS = `
@define-color bg #0f1115;
@define-color surface #171a21;
@define-color surface2 #1e222b;
@define-color border #2a2f3a;
@define-color text #e6e8ee;
@define-color muted #8b93a3;
@define-color accent #5b8cff;
@define-color green #34d399;
@define-color red #f87171;
@define-color orange #fb923c;
@define-color blue #60a5fa;
@define-color yellow #fbbf24;

window, window.background { background: @bg; color: @text; }
headerbar { background: @surface; border-bottom: 1px solid @border; box-shadow: none; min-height: 52px; color: @text; }
headerbar button { background: @surface2; border: 1px solid @border; border-radius: 999px; padding: 4px 16px; box-shadow: none; color: @text; }
headerbar button:hover { background: #262b36; }
headerbar button:checked { background: alpha(@orange, 0.18); border-color: @orange; color: @orange; }

/* ページ切り替え(ピル型) */
stackswitcher { background: @surface2; border-radius: 999px; padding: 3px; border: 1px solid @border; }
stackswitcher button { background: transparent; border: none; border-radius: 999px; padding: 5px 22px; color: @muted; box-shadow: none; font-weight: 600; }
stackswitcher button:checked { background: @accent; color: #fff; }
stackswitcher button:hover:not(:checked) { color: @text; }

/* カード */
.card { background: @surface; border: 1px solid @border; border-radius: 16px; padding: 16px 18px; }
.card-title { color: @muted; font-size: 13px; font-weight: 700; }
.section-title { font-size: 13px; font-weight: 700; color: @muted; letter-spacing: 1px; margin: 4px 2px; }
.kv-key { color: @muted; font-size: 13px; }
.kv-val { color: @text; font-size: 13px; }
.big-num { font-size: 34px; font-weight: 700; color: @text; }
.dim-label, .dim { color: @muted; }

/* ピル(状態) */
.pill { border-radius: 999px; padding: 3px 12px; font-weight: 700; font-size: 13px; background: alpha(@muted, 0.18); color: @muted; }
.pill-running, .pill-ok { background: alpha(@green, 0.16); color: @green; }
.pill-starting, .pill-warn { background: alpha(@yellow, 0.16); color: @yellow; }
.pill-backoff { background: alpha(@orange, 0.16); color: @orange; }
.pill-stopped { background: alpha(@muted, 0.18); color: @muted; }
.pill-error { background: alpha(@red, 0.16); color: @red; }
.tag { border-radius: 999px; padding: 1px 9px; font-size: 11px; font-weight: 700; }
.tag-live { background: alpha(@green, 0.14); color: @green; }
.tag-restart { background: alpha(@orange, 0.14); color: @orange; }

/* 統計タイル */
.stat { background: @surface2; border: 1px solid @border; border-radius: 14px; padding: 12px 16px; border-top: 3px solid @border; }
.stat .stat-title { color: @muted; font-size: 12px; font-weight: 600; }
.stat .stat-value { font-size: 24px; font-weight: 700; color: @text; }
.stat-green { border-top-color: @green; } .stat-green .stat-value { color: @green; }
.stat-red { border-top-color: @red; } .stat-red .stat-value { color: @red; }
.stat-orange { border-top-color: @orange; } .stat-orange .stat-value { color: @orange; }
.stat-blue { border-top-color: @blue; } .stat-blue .stat-value { color: @blue; }
.stat-gray .stat-value { color: @muted; }

/* バナー */
.banner { border-radius: 12px; padding: 10px 14px; background: alpha(@muted, 0.12); color: @text; border-left: 4px solid @muted; }
.banner-error { background: alpha(@red, 0.12); border-left-color: @red; }
.banner-warn { background: alpha(@orange, 0.12); border-left-color: @orange; }
.banner-ok { background: alpha(@green, 0.12); border-left-color: @green; }
.err-label { color: @red; font-weight: 700; }
.warn-label { color: @orange; font-weight: 700; }
.ok-label { color: @green; font-weight: 700; }

/* 入力 */
entry { background: @surface2; color: @text; border: 1px solid @border; border-radius: 10px; padding: 6px 10px; min-height: 22px; box-shadow: none; }
entry:focus-within { border-color: @accent; box-shadow: 0 0 0 2px alpha(@accent, 0.25); }
entry:disabled { color: @muted; background: alpha(@surface2, 0.5); }
entry.error { border-color: @red; background: alpha(@red, 0.12); box-shadow: 0 0 0 2px alpha(@red, 0.2); }
dropdown > button, dropdown button { background: @surface2; color: @text; border: 1px solid @border; border-radius: 10px; box-shadow: none; }
switch { background: @border; border-radius: 999px; border: none; }
switch:checked { background: @accent; }
switch slider { background: #fff; border-radius: 999px; border: none; box-shadow: none; }
checkbutton check { border-radius: 6px; background: @surface2; border: 1px solid @border; }
checkbutton check:checked { background: @accent; border-color: @accent; color: #fff; }

/* ボタン */
button.btn { background: @surface2; color: @text; border: 1px solid @border; border-radius: 10px; padding: 8px 18px; box-shadow: none; font-weight: 600; }
button.btn:hover { background: #262b36; }
button.btn-primary { background: @accent; border-color: @accent; color: #fff; }
button.btn-primary:hover { background: #7aa2ff; }
button.btn-primary:disabled { background: alpha(@accent, 0.25); border-color: transparent; color: alpha(#fff, 0.5); }
button.btn-danger { background: alpha(@red, 0.14); border-color: alpha(@red, 0.5); color: @red; }
button.btn-danger:hover { background: alpha(@red, 0.24); }

/* ログ */
textview, textview text { background: transparent; color: @text; }
.log { font-family: monospace; font-size: 12px; }
scrolledwindow { border-radius: 12px; }
scrollbar slider { background: alpha(@muted, 0.4); border-radius: 999px; min-width: 6px; min-height: 6px; }
paned > separator { background: transparent; min-height: 10px; }

/* ダイアログ */
window.dialog-win { background: @surface; }
.dialog-title { font-size: 16px; font-weight: 700; }
tooltip { background: @surface2; color: @text; border: 1px solid @border; border-radius: 8px; }
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

	stack   *gtk.Stack
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
	if st := gtk.SettingsGetDefault(); st != nil {
		st.SetObjectProperty("gtk-application-prefer-dark-theme", true)
	}
	prov := gtk.NewCSSProvider()
	prov.LoadFromString(appCSS)
	gtk.StyleContextAddProviderForDisplay(gdk.DisplayGetDefault(), prov, gtk.STYLE_PROVIDER_PRIORITY_USER)

	a.win = gtk.NewApplicationWindow(a.app)
	a.win.SetTitle("srtrx 制御")
	a.win.SetDefaultSize(1000, 760)

	a.status = newStatusPage(a)
	a.settings = newSettingsPage(a)
	a.logPage = newLogPage(a)

	stack := gtk.NewStack()
	stack.SetTransitionType(gtk.StackTransitionTypeCrossfade)
	stack.SetTransitionDuration(150)
	stack.AddTitled(a.status.root, "status", "状態")
	stack.AddTitled(a.settings.root, "settings", "設定")
	stack.AddTitled(a.logPage.root, "logs", "ログ")
	a.stack = stack

	// ヘッダー: 左に本番ロック、中央にページ切り替え、右に情報
	hb := gtk.NewHeaderBar()
	hb.SetShowTitleButtons(true)
	a.lockBtn = gtk.NewToggleButtonWithLabel("本番ロック")
	a.lockBtn.SetTooltipText("ロック中は設定を変更できません。解除は確認ダイアログで行います。")
	a.lockBtn.ConnectToggled(a.onLockToggled)
	hb.PackStart(a.lockBtn)
	sw := gtk.NewStackSwitcher()
	sw.SetStack(stack)
	hb.SetTitleWidget(sw)
	info := gtk.NewButtonWithLabel("情報")
	info.ConnectClicked(a.showInfo)
	hb.PackEnd(info)
	a.win.SetTitlebar(hb)
	a.win.SetChild(stack)

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
	w.AddCSSClass("dialog-win")
	box := gtk.NewBox(gtk.OrientationVertical, 14)
	box.SetMarginTop(16)
	box.SetMarginBottom(16)
	box.SetMarginStart(20)
	box.SetMarginEnd(20)
	h := gtk.NewLabel(title)
	h.AddCSSClass("dialog-title")
	h.SetXAlign(0)
	box.Append(h)
	m := gtk.NewLabel(msg)
	m.SetWrap(true)
	m.SetMaxWidthChars(48)
	box.Append(m)
	row := gtk.NewBox(gtk.OrientationHorizontal, 8)
	row.SetHAlign(gtk.AlignEnd)
	cancel := gtk.NewButtonWithLabel("キャンセル")
	cancel.AddCSSClass("btn")
	ok := gtk.NewButtonWithLabel(okLabel)
	ok.AddCSSClass("btn")
	ok.AddCSSClass("btn-primary")
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
	w.AddCSSClass("dialog-win")
	box := gtk.NewBox(gtk.OrientationVertical, 12)
	box.SetMarginTop(16)
	box.SetMarginBottom(16)
	box.SetMarginStart(20)
	box.SetMarginEnd(20)
	m := gtk.NewLabel(msg)
	m.SetWrap(true)
	m.SetMaxWidthChars(48)
	m.SetXAlign(0)
	box.Append(m)
	ok := gtk.NewButtonWithLabel("閉じる")
	ok.AddCSSClass("btn")
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
