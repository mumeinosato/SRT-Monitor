package ui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

type logPage struct {
	app      *App
	root     *gtk.Paned
	faults   *gtk.TextView
	logs     *gtk.TextView
	lastVer  uint64
	lastFlts int
	lastTop  time.Time
}

func newLogPage(a *App) *logPage {
	p := &logPage{app: a}
	mk := func(title string) (*gtk.Box, *gtk.TextView) {
		card := newCard(title)
		card.SetVExpand(true)
		tv := gtk.NewTextView()
		tv.SetEditable(false)
		tv.SetCursorVisible(false)
		tv.AddCSSClass("log")
		tv.SetWrapMode(gtk.WrapWordChar)
		sw := gtk.NewScrolledWindow()
		sw.SetVExpand(true)
		sw.SetChild(tv)
		card.Append(sw)
		return card, tv
	}
	top, ftv := mk("障害履歴(直近100件、新しい順)")
	bot, ltv := mk("ログ(ffmpegのstderrを含む)")
	p.faults, p.logs = ftv, ltv
	p.root = gtk.NewPaned(gtk.OrientationVertical)
	p.root.SetMarginTop(16)
	p.root.SetMarginBottom(16)
	p.root.SetMarginStart(20)
	p.root.SetMarginEnd(20)
	p.root.SetStartChild(top)
	p.root.SetEndChild(bot)
	p.root.SetPosition(250)
	p.root.SetShrinkStartChild(false)
	p.root.SetShrinkEndChild(false)
	return p
}

func (p *logPage) update() {
	// 障害履歴
	fl := p.app.ctl.Faults()
	if len(fl) != p.lastFlts || (len(fl) > 0 && !fl[0].Time.Equal(p.lastTop)) {
		p.lastFlts = len(fl)
		if len(fl) > 0 {
			p.lastTop = fl[0].Time
		}
		var b strings.Builder
		for _, f := range fl {
			fmt.Fprintf(&b, "<span foreground=\"#8b93a3\">%s</span> <b>[%s]</b> <span foreground=\"#fb923c\">%s</span>",
				f.Time.Format("15:04:05"), esc(f.Target), esc(f.Reason))
			if len(f.Stderr) > 0 {
				b.WriteString("\n    <span foreground=\"#8b93a3\">stderr: " + esc(strings.Join(f.Stderr, " | ")) + "</span>")
			}
			b.WriteString("\n")
		}
		if len(fl) == 0 {
			b.WriteString("<span foreground=\"#34d399\">障害はありません</span>\n")
		}
		setMarkup(p.faults, b.String())
	}
	// ログ
	if p.app.logs == nil {
		return
	}
	if v := p.app.logs.Ring.Version(); v != p.lastVer {
		p.lastVer = v
		var b strings.Builder
		for _, l := range p.app.logs.Ring.Tail(500) {
			b.WriteString(colorLogLine(l))
			b.WriteString("\n")
		}
		setMarkup(p.logs, b.String())
		it := p.logs.Buffer().EndIter()
		p.logs.ScrollToIter(it, 0, false, 0, 0)
	}
}

func esc(s string) string { return glib.MarkupEscapeText(s) }

// setMarkup はテキストビューの中身を、Pango マークアップで置き換える。
func setMarkup(tv *gtk.TextView, markup string) {
	buf := tv.Buffer()
	buf.SetText("")
	buf.InsertMarkup(buf.EndIter(), markup)
}

// colorLogLine は整形したログ1行を、レベルに応じた色のマークアップにする。
func colorLogLine(line string) string {
	text := formatLogLine(line)
	color := "#c9ced8"
	switch {
	case strings.Contains(text, " ERROR "):
		color = "#f87171"
	case strings.Contains(text, " WARN "):
		color = "#fb923c"
	case strings.Contains(text, " DEBUG "):
		color = "#8b93a3"
	}
	return "<span foreground=\"" + color + "\">" + esc(text) + "</span>"
}

// formatLogLine は slog のJSON1行を、人が読む形にする。
func formatLogLine(line string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		return line
	}
	ts, _ := m["time"].(string)
	if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
		ts = t.Local().Format("15:04:05")
	}
	level, _ := m["level"].(string)
	msg, _ := m["msg"].(string)
	var keys []string
	for k := range m {
		if k != "time" && k != "level" && k != "msg" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	fmt.Fprintf(&b, "%s %-5s %s", ts, level, msg)
	for _, k := range keys {
		fmt.Fprintf(&b, " %s=%v", k, m[k])
	}
	return b.String()
}
