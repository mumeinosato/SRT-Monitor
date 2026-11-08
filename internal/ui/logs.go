package ui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

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
		box := gtk.NewBox(gtk.OrientationVertical, 4)
		l := gtk.NewLabel(title)
		l.SetXAlign(0)
		l.AddCSSClass("big")
		l.SetMarginStart(8)
		l.SetMarginTop(8)
		box.Append(l)
		tv := gtk.NewTextView()
		tv.SetEditable(false)
		tv.SetMonospace(true)
		tv.SetWrapMode(gtk.WrapWordChar)
		sw := gtk.NewScrolledWindow()
		sw.SetVExpand(true)
		sw.SetChild(tv)
		box.Append(sw)
		return box, tv
	}
	top, ftv := mk("障害履歴(直近100件、新しい順)")
	bot, ltv := mk("ログ(ffmpegのstderrを含む)")
	p.faults, p.logs = ftv, ltv
	p.root = gtk.NewPaned(gtk.OrientationVertical)
	p.root.SetStartChild(top)
	p.root.SetEndChild(bot)
	p.root.SetPosition(260)
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
			fmt.Fprintf(&b, "%s [%s] %s", f.Time.Format("15:04:05"), f.Target, f.Reason)
			if len(f.Stderr) > 0 {
				b.WriteString("\n    stderr: " + strings.Join(f.Stderr, " | "))
			}
			b.WriteString("\n")
		}
		if len(fl) == 0 {
			b.WriteString("(障害はありません)\n")
		}
		p.faults.Buffer().SetText(b.String())
	}
	// ログ
	if p.app.logs == nil {
		return
	}
	if v := p.app.logs.Ring.Version(); v != p.lastVer {
		p.lastVer = v
		var b strings.Builder
		for _, l := range p.app.logs.Ring.Tail(500) {
			b.WriteString(formatLogLine(l))
			b.WriteString("\n")
		}
		p.logs.Buffer().SetText(b.String())
		it := p.logs.Buffer().EndIter()
		p.logs.ScrollToIter(it, 0, false, 0, 0)
	}
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
