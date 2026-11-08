package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"srtrx/internal/config"
	"srtrx/internal/controller"
)

type kind int

const (
	kInt kind = iota
	kFloat
	kString
	kBool
	kEnum
	kArgs  // 空白区切りの引数列
	kTiles // タイルの選択
)

// field は設定項目1つ。GUIの項目とTOMLのキーは1対1。
type field struct {
	key   string
	label string
	live  bool // 「即時」の項目(変えた瞬間に反映)。それ以外は「適用して再起動」
	kind  kind
	enum  []string

	pInt    func(*config.Config) *int
	pFloat  func(*config.Config) *float64
	pStr    func(*config.Config) *string
	pBool   func(*config.Config) *bool
	pArgs   func(*config.Config) *[]string
	pTiles  func(*config.Config) *[]string
	derived func(config.Config) bool // true の間は無効表示(同梱MediaMTXから自動で決まる)

	// widget
	w        gtk.Widgetter
	entry    *gtk.Entry
	check    *gtk.CheckButton
	drop     *gtk.DropDown
	tileBtns map[string]*gtk.CheckButton
	parseErr bool
}

func ints(key, label string, live bool, p func(*config.Config) *int) *field {
	return &field{key: key, label: label, live: live, kind: kInt, pInt: p}
}
func floats(key, label string, live bool, p func(*config.Config) *float64) *field {
	return &field{key: key, label: label, live: live, kind: kFloat, pFloat: p}
}
func strs(key, label string, live bool, p func(*config.Config) *string) *field {
	return &field{key: key, label: label, live: live, kind: kString, pStr: p}
}
func bools(key, label string, live bool, p func(*config.Config) *bool) *field {
	return &field{key: key, label: label, live: live, kind: kBool, pBool: p}
}
func enums(key, label string, live bool, opts []string, p func(*config.Config) *string) *field {
	return &field{key: key, label: label, live: live, kind: kEnum, enum: opts, pStr: p}
}

func mtxDerived(c config.Config) bool { return c.MediaMTX.Enabled }

// tab は設定画面のタブ(接続、映像、監視、表示)。
type tab struct {
	name   string
	fields []*field
}

func buildTabs() []tab {
	conn := []*field{
		bools("mediamtx.enabled", "同梱のMediaMTXを使う(オフなら外部のMediaMTX)", false, func(c *config.Config) *bool { return &c.MediaMTX.Enabled }),
		strs("mediamtx.srt_listen", "MediaMTX SRT待ち受け(host:port)", false, func(c *config.Config) *string { return &c.MediaMTX.SRTListen }),
		strs("mediamtx.metrics_listen", "MediaMTX /metrics待ち受け", false, func(c *config.Config) *string { return &c.MediaMTX.MetricsListen }),
		enums("mediamtx.log_level", "MediaMTX ログレベル", false, []string{"error", "warn", "info", "debug"}, func(c *config.Config) *string { return &c.MediaMTX.LogLevel }),
		ints("mediamtx.ready_timeout_ms", "MediaMTX 準備完了の待ち(ms)", false, func(c *config.Config) *int { return &c.MediaMTX.ReadyTimeoutMS }),
		ints("mediamtx.hang_ms", "MediaMTX 固まり判定(/metrics無応答, ms)", false, func(c *config.Config) *int { return &c.MediaMTX.HangMS }),
		strs("input.host", "入力先ホスト", false, func(c *config.Config) *string { return &c.Input.Host }),
		ints("input.port", "入力先ポート", false, func(c *config.Config) *int { return &c.Input.Port }),
		strs("input.path", "パス名", false, func(c *config.Config) *string { return &c.Input.Path }),
		enums("input.mode", "SRTモード", false, []string{"caller", "listener"}, func(c *config.Config) *string { return &c.Input.Mode }),
		ints("input.latency_ms", "レイテンシ(受信バッファの遅延, ms)", false, func(c *config.Config) *int { return &c.Input.LatencyMS }),
		ints("input.timeout_ms", "無通信タイムアウト(ms)", false, func(c *config.Config) *int { return &c.Input.TimeoutMS }),
		ints("input.connect_timeout_ms", "接続タイムアウト(ms)", false, func(c *config.Config) *int { return &c.Input.ConnectTimeoutMS }),
		ints("input.recv_buffer_bytes", "受信バッファのサイズ(byte, 0=libsrtの既定)", false, func(c *config.Config) *int { return &c.Input.RecvBufferBytes }),
		bools("input.tlpktdrop", "遅れたパケットの破棄(tlpktdrop)", false, func(c *config.Config) *bool { return &c.Input.TLPktDrop }),
	}
	conn[6].derived, conn[7].derived = mtxDerived, mtxDerived // input.host / port

	video := []*field{
		ints("video.width", "出力解像度 幅", false, func(c *config.Config) *int { return &c.Video.Width }),
		ints("video.height", "出力解像度 高さ", false, func(c *config.Config) *int { return &c.Video.Height }),
		ints("video.fps", "表示間隔のFPS(バッファ使用時)", true, func(c *config.Config) *int { return &c.Video.FPS }),
		enums("video.hwaccel", "ハードウェアデコード", false, []string{"none", "vaapi", "qsv"}, func(c *config.Config) *string { return &c.Video.HWAccel }),
		ints("video.player_buffer_frames", "プレイヤー側のバッファ(フレーム数, 0=最新優先)", true, func(c *config.Config) *int { return &c.Video.PlayerBufferFrames }),
		{key: "video.extra_args", label: "ffmpegの追加引数(上級者向け)", kind: kArgs, pArgs: func(c *config.Config) *[]string { return &c.Video.ExtraArgs }},
	}

	sup := []*field{
		ints("supervisor.watchdog_min_ms", "ウォッチドッグのしきい値の下限(ms)", true, func(c *config.Config) *int { return &c.Supervisor.WatchdogMinMS }),
		floats("supervisor.watchdog_latency_factor", "ウォッチドッグ: レイテンシの倍率", true, func(c *config.Config) *float64 { return &c.Supervisor.WatchdogLatencyFactor }),
		ints("supervisor.backoff_initial_ms", "再起動の待ち時間 開始(ms)", true, func(c *config.Config) *int { return &c.Supervisor.BackoffInitialMS }),
		ints("supervisor.backoff_max_ms", "再起動の待ち時間 上限(ms)", true, func(c *config.Config) *int { return &c.Supervisor.BackoffMaxMS }),
		ints("supervisor.backoff_reset_after_ms", "待ち時間を初期値へ戻すまでの安定時間(ms)", true, func(c *config.Config) *int { return &c.Supervisor.BackoffResetAfterMS }),
		ints("supervisor.hw_fallback_fail_ms", "HWデコード: 起動失敗とみなす時間(ms)", true, func(c *config.Config) *int { return &c.Supervisor.HWFallbackFailMS }),
		ints("supervisor.hw_fallback_fail_count", "HWデコード: 外すまでの連続失敗回数", true, func(c *config.Config) *int { return &c.Supervisor.HWFallbackFailCount }),
		strs("metrics.url", "/metrics のURL", true, func(c *config.Config) *string { return &c.Metrics.URL }),
		ints("metrics.interval_ms", "/metrics の取得間隔(ms)", true, func(c *config.Config) *int { return &c.Metrics.IntervalMS }),
		ints("metrics.timeout_ms", "/metrics の取得タイムアウト(ms)", true, func(c *config.Config) *int { return &c.Metrics.TimeoutMS }),
	}
	sup[7].derived = mtxDerived

	disp := []*field{
		strs("display.monitor", "表示するモニター(primary またはコネクター名)", true, func(c *config.Config) *string { return &c.Display.Monitor }),
		bools("display.fullscreen", "全画面", true, func(c *config.Config) *bool { return &c.Display.Fullscreen }),
		bools("display.show_state_badge", "状態バッジを出す", true, func(c *config.Config) *bool { return &c.Display.ShowStateBadge }),
		enums("display.no_signal", "無信号時の表示", true, []string{"last_frame", "black", "image"}, func(c *config.Config) *string { return &c.Display.NoSignal }),
		strs("display.no_signal_image", "無信号時の画像(PNGのパス)", true, func(c *config.Config) *string { return &c.Display.NoSignalImage }),
		bools("overlay.enabled", "オーバーレイを出す", true, func(c *config.Config) *bool { return &c.Overlay.Enabled }),
		enums("overlay.position", "オーバーレイの位置", true, []string{"top", "bottom"}, func(c *config.Config) *string { return &c.Overlay.Position }),
		floats("overlay.tile_scale", "タイルの大きさ(0.5〜3.0)", true, func(c *config.Config) *float64 { return &c.Overlay.TileScale }),
		floats("overlay.opacity", "透過度(0.1〜1.0)", true, func(c *config.Config) *float64 { return &c.Overlay.Opacity }),
		strs("overlay.hotkey", "ホットキー(表示の切り替え, 1文字)", true, func(c *config.Config) *string { return &c.Overlay.Hotkey }),
		{key: "overlay.tiles", label: "表示するタイル", live: true, kind: kTiles, pTiles: func(c *config.Config) *[]string { return &c.Overlay.Tiles }},
	}
	return []tab{{"接続", conn}, {"映像", video}, {"監視", sup}, {"表示", disp}}
}

var tileTitles = map[string]string{
	"connection": "接続状態", "drop": "ドロップ", "loss": "ロス", "retrans": "再送受信",
	"frame_errors": "フレームエラー", "rtt": "RTT", "fps": "受信FPS", "no_signal": "無信号時間", "restarts": "ffmpeg再起動回数",
}

type settingsPage struct {
	app  *App
	root *gtk.Box
	tabs []tab

	draft   config.Config
	loading bool // ウィジェットへ値を流し込んでいる間は、変更として扱わない

	applyBtn *gtk.Button
	note     *gtk.Label
	profDrop *gtk.DropDown
	profName *gtk.Entry
	profList []string
	form     *gtk.Box
	locked   bool
}

func newSettingsPage(a *App) *settingsPage {
	p := &settingsPage{app: a, tabs: buildTabs(), draft: a.ctl.Config()}
	p.root = gtk.NewBox(gtk.OrientationVertical, 8)
	p.root.SetMarginTop(12)
	p.root.SetMarginBottom(12)
	p.root.SetMarginStart(12)
	p.root.SetMarginEnd(12)

	p.form = gtk.NewBox(gtk.OrientationVertical, 8)
	p.form.SetVExpand(true)

	// プロファイル
	prof := gtk.NewBox(gtk.OrientationHorizontal, 6)
	prof.Append(gtk.NewLabel("プロファイル"))
	p.profDrop = gtk.NewDropDownFromStrings([]string{""})
	prof.Append(p.profDrop)
	sw := gtk.NewButtonWithLabel("切り替え")
	sw.SetTooltipText("選んだプロファイルを適用して、ffmpegを再起動します")
	sw.ConnectClicked(p.switchProfile)
	prof.Append(sw)
	p.profName = gtk.NewEntry()
	p.profName.SetPlaceholderText("保存名(例: 本番)")
	prof.Append(p.profName)
	save := gtk.NewButtonWithLabel("現在の設定を保存")
	save.ConnectClicked(p.saveProfile)
	prof.Append(save)
	p.form.Append(prof)

	nb := gtk.NewNotebook()
	nb.SetVExpand(true)
	for i := range p.tabs {
		nb.AppendPage(p.buildTab(&p.tabs[i]), gtk.NewLabel(p.tabs[i].name))
	}
	p.form.Append(nb)
	p.root.Append(p.form)

	bar := gtk.NewBox(gtk.OrientationHorizontal, 12)
	p.note = gtk.NewLabel("")
	p.note.SetXAlign(0)
	p.note.SetHExpand(true)
	bar.Append(p.note)
	p.applyBtn = gtk.NewButtonWithLabel("適用して再起動")
	p.applyBtn.AddCSSClass("suggested-action")
	p.applyBtn.ConnectClicked(p.applyRestart)
	bar.Append(p.applyBtn)
	p.root.Append(bar)

	p.pushToWidgets()
	p.revalidate()
	return p
}

func (p *settingsPage) buildTab(t *tab) gtk.Widgetter {
	grid := gtk.NewGrid()
	grid.SetRowSpacing(6)
	grid.SetColumnSpacing(12)
	grid.SetMarginTop(10)
	grid.SetMarginStart(10)
	grid.SetMarginEnd(10)
	for row, f := range t.fields {
		f := f
		lbl := gtk.NewLabel(f.label)
		lbl.SetXAlign(0)
		lbl.SetHExpand(true)
		tag := "反映: ffmpeg再起動"
		if f.live {
			tag = "反映: 即時"
		}
		lbl.SetTooltipText(tag + "(" + f.key + ")")
		grid.Attach(lbl, 0, row, 1, 1)
		w := p.makeWidget(f)
		f.w = w
		grid.Attach(w, 1, row, 1, 1)
		hint := gtk.NewLabel(map[bool]string{true: "即時", false: "再起動"}[f.live])
		hint.AddCSSClass("dim-label")
		grid.Attach(hint, 2, row, 1, 1)
	}
	sw := gtk.NewScrolledWindow()
	sw.SetChild(grid)
	sw.SetVExpand(true)
	return sw
}

func (p *settingsPage) makeWidget(f *field) gtk.Widgetter {
	changed := func() {
		if !p.loading {
			p.onChange(f)
		}
	}
	switch f.kind {
	case kBool:
		f.check = gtk.NewCheckButton()
		f.check.SetHAlign(gtk.AlignStart)
		f.check.ConnectToggled(changed)
		return f.check
	case kEnum:
		f.drop = gtk.NewDropDownFromStrings(f.enum)
		f.drop.SetHAlign(gtk.AlignStart)
		f.drop.NotifyProperty("selected", changed)
		return f.drop
	case kTiles:
		box := gtk.NewFlowBox()
		box.SetSelectionMode(gtk.SelectionNone)
		box.SetMaxChildrenPerLine(3)
		f.tileBtns = map[string]*gtk.CheckButton{}
		for _, id := range config.AllTiles {
			cb := gtk.NewCheckButtonWithLabel(tileTitles[id])
			cb.ConnectToggled(changed)
			f.tileBtns[id] = cb
			box.Append(cb)
		}
		return box
	default:
		f.entry = gtk.NewEntry()
		f.entry.SetWidthChars(24)
		f.entry.ConnectChanged(changed)
		return f.entry
	}
}

// pushToWidgets は draft をウィジェットへ流し込む(変更としては扱わない)。
func (p *settingsPage) pushToWidgets() {
	p.loading = true
	defer func() { p.loading = false }()
	eff := p.draft.Effective()
	for i := range p.tabs {
		for _, f := range p.tabs[i].fields {
			src := p.draft
			if f.derived != nil && f.derived(p.draft) {
				src = eff // 同梱MediaMTXのときは、自動で決まる値を見せる
			}
			switch f.kind {
			case kInt:
				f.entry.SetText(strconv.Itoa(*f.pInt(&src)))
			case kFloat:
				f.entry.SetText(strconv.FormatFloat(*f.pFloat(&src), 'g', -1, 64))
			case kString:
				f.entry.SetText(*f.pStr(&src))
			case kBool:
				f.check.SetActive(*f.pBool(&src))
			case kEnum:
				v := *f.pStr(&src)
				for i, o := range f.enum {
					if o == v {
						f.drop.SetSelected(uint(i))
					}
				}
			case kArgs:
				f.entry.SetText(strings.Join(*f.pArgs(&src), " "))
			case kTiles:
				on := map[string]bool{}
				for _, id := range *f.pTiles(&src) {
					on[id] = true
				}
				for id, cb := range f.tileBtns {
					cb.SetActive(on[id])
				}
			}
			f.parseErr = false
			f.w.(interface{ RemoveCSSClass(string) }).RemoveCSSClass("error")
			f.w.(interface{ SetSensitive(bool) }).SetSensitive(!p.locked && !(f.derived != nil && f.derived(p.draft)))
		}
	}
}

// read はウィジェットの値を c へ書く。解釈できなければ false。
func (f *field) read(c *config.Config) bool {
	switch f.kind {
	case kInt:
		n, err := strconv.Atoi(strings.TrimSpace(f.entry.Text()))
		if err != nil {
			return false
		}
		*f.pInt(c) = n
	case kFloat:
		v, err := strconv.ParseFloat(strings.TrimSpace(f.entry.Text()), 64)
		if err != nil {
			return false
		}
		*f.pFloat(c) = v
	case kString:
		*f.pStr(c) = f.entry.Text()
	case kBool:
		*f.pBool(c) = f.check.Active()
	case kEnum:
		i := int(f.drop.Selected())
		if i < 0 || i >= len(f.enum) {
			return false
		}
		*f.pStr(c) = f.enum[i]
	case kArgs:
		*f.pArgs(c) = strings.Fields(f.entry.Text())
	case kTiles:
		var ids []string
		for _, id := range config.AllTiles {
			if f.tileBtns[id].Active() {
				ids = append(ids, id)
			}
		}
		*f.pTiles(c) = ids
	}
	return true
}

func (p *settingsPage) onChange(f *field) {
	if p.locked {
		return
	}
	if !f.read(&p.draft) {
		f.parseErr = true
	} else {
		f.parseErr = false
	}
	p.revalidate()
	if f.parseErr {
		return
	}
	// 同梱MediaMTXの切り替えで、自動で決まる欄の見え方が変わる
	if f.key == "mediamtx.enabled" || f.key == "mediamtx.srt_listen" || f.key == "mediamtx.metrics_listen" {
		p.refreshDerived()
	}
	if f.live {
		// 「即時」の項目は、変えた瞬間に反映する(検証に通ったときだけ)
		err := p.app.ctl.SetLive(func(c *config.Config) { f.read(c) })
		if err != nil {
			p.setNote("反映できない: "+err.Error(), "err-label")
		}
	}
}

func (p *settingsPage) refreshDerived() {
	p.loading = true
	defer func() { p.loading = false }()
	eff := p.draft.Effective()
	for i := range p.tabs {
		for _, f := range p.tabs[i].fields {
			if f.derived == nil {
				continue
			}
			on := f.derived(p.draft)
			f.w.(interface{ SetSensitive(bool) }).SetSensitive(!p.locked && !on)
			if on {
				switch f.kind {
				case kInt:
					f.entry.SetText(strconv.Itoa(*f.pInt(&eff)))
				case kString:
					f.entry.SetText(*f.pStr(&eff))
				}
			}
		}
	}
}

// revalidate は、範囲外の欄を赤くし、「適用して再起動」の可否を決める。
func (p *settingsPage) revalidate() {
	err := p.draft.Validate()
	ve, _ := err.(config.ValidationError)
	bad := 0
	for i := range p.tabs {
		for _, f := range p.tabs[i].fields {
			red := f.parseErr || ve.Has(f.key)
			// 無効表示(自動で決まる)の欄は検証の対象にしない
			if f.derived != nil && f.derived(p.draft) {
				red = false
			}
			w := f.w.(interface {
				AddCSSClass(string)
				RemoveCSSClass(string)
			})
			if red {
				w.AddCSSClass("error")
				bad++
			} else {
				w.RemoveCSSClass("error")
			}
		}
	}
	valid := bad == 0 && err == nil
	if err != nil && bad == 0 {
		// 画面に出ていないキーのエラー(自動で決まる欄など)も、適用は止める
		valid = false
	}
	pending := p.app.ctl.PendingRestart(p.draft)
	p.applyBtn.SetSensitive(valid && pending && !p.locked)
	switch {
	case !valid:
		p.setNote("範囲外の値があります(赤い欄)。直すまで「適用」できません", "err-label")
	case pending:
		p.setNote("未適用の変更があります。「適用して再起動」で反映します(映像が一瞬止まります)", "warn-label")
	default:
		p.setNote("", "dim-label")
	}
}

func (p *settingsPage) setNote(text, class string) {
	for _, c := range []string{"err-label", "warn-label", "dim-label", "ok-label"} {
		p.note.RemoveCSSClass(c)
	}
	p.note.AddCSSClass(class)
	p.note.SetText(text)
}

func (p *settingsPage) applyRestart() {
	draft := p.draft.Clone()
	confirmDialog(&p.app.win.Window, "設定を適用して再起動しますか?",
		"ffmpeg(と、MediaMTXの設定が変わったときはMediaMTX)を再起動します。映像が一瞬止まります。最後のフレームは保持されます。",
		"適用して再起動", func() {
			if err := p.app.ctl.ApplyRestart(draft); err != nil {
				p.setNote("適用できない: "+err.Error(), "err-label")
				return
			}
			p.draft = p.app.ctl.Config()
			p.pushToWidgets()
			p.revalidate()
		})
}

func (p *settingsPage) saveProfile() {
	name := strings.TrimSpace(p.profName.Text())
	if name == "" {
		p.setNote("保存名を入れてください", "err-label")
		return
	}
	// 保存するのは「適用済み」の設定
	if err := p.app.ctl.SaveProfile(name); err != nil {
		p.setNote("プロファイルを保存できない: "+err.Error(), "err-label")
		return
	}
	p.setNote(fmt.Sprintf("プロファイル「%s」を保存しました(適用済みの設定)", name), "ok-label")
	p.refreshProfiles()
}

func (p *settingsPage) switchProfile() {
	i := int(p.profDrop.Selected())
	if i < 0 || i >= len(p.profList) {
		return
	}
	name := p.profList[i]
	cfg, err := p.app.ctl.LoadProfile(name)
	if err != nil {
		p.setNote("プロファイルを読めない: "+err.Error(), "err-label")
		return
	}
	confirmDialog(&p.app.win.Window, fmt.Sprintf("プロファイル「%s」へ切り替えますか?", name),
		"設定を適用して、ffmpegを再起動します。", "切り替える", func() {
			if err := p.app.ctl.ApplyRestart(cfg); err != nil {
				p.setNote("切り替えできない: "+err.Error(), "err-label")
				return
			}
			p.draft = p.app.ctl.Config()
			p.pushToWidgets()
			p.revalidate()
		})
}

func (p *settingsPage) refreshProfiles() {
	names := p.app.ctl.Profiles()
	if strings.Join(names, "\x00") == strings.Join(p.profList, "\x00") {
		return
	}
	p.profList = names
	if len(names) == 0 {
		p.profDrop.SetModel(gtk.NewStringList([]string{"(なし)"}))
	} else {
		p.profDrop.SetModel(gtk.NewStringList(names))
	}
}

// update は定期的に呼ばれる。ロックの反映と、プロファイル一覧の更新。
func (p *settingsPage) update(st controller.Status) {
	if st.Locked != p.locked {
		p.locked = st.Locked
		if p.locked {
			p.draft = p.app.ctl.Config() // 未適用の下書きは捨てる
		}
		p.pushToWidgets()
		p.revalidate()
		p.profDrop.SetSensitive(!p.locked)
		p.profName.SetSensitive(!p.locked)
		if p.locked {
			p.setNote("本番ロック中です(ヘッダーの「本番ロック」で解除)", "warn-label")
		}
	}
	p.refreshProfiles()
}
