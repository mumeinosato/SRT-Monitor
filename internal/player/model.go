// Package player はプレイヤー(全画面表示、オーバーレイ、バッジ)。
// model.go は GTK に依存しない表示ロジック(タイルの値と色、バッジの文言)。
package player

import (
	"fmt"
	"sync"
	"time"

	"srtrx/internal/config"
	"srtrx/internal/ipc"
)

// 色の名前(CSSのクラス名 tile-<色> に対応)。
const (
	ColorNeutral = "neutral"
	ColorGreen   = "green"
	ColorRed     = "red"
	ColorOrange  = "orange"
	ColorBlue    = "blue"
	ColorGray    = "gray"
	ColorYellow  = "yellow"
)

// ControlLostAfter は制御から何も来ない時間がこれを超えたら「制御切断」。
const ControlLostAfter = 3 * time.Second

// Model はプレイヤーの表示に要る状態。IPCのgoroutineが書き、GTKのメインスレッドが読む。
type Model struct {
	mu sync.Mutex

	Cfg       config.Config
	State     ipc.State
	HaveState bool
	Stats     ipc.Stats
	HaveStats bool

	lastCtl time.Time // 制御から最後に何か届いた時刻
	FPS     float64   // 直近1秒の受信FPS(自分で数える)
}

// NewModel は設定を持って始める。
func NewModel(cfg config.Config) *Model { return &Model{Cfg: cfg} }

func (m *Model) Touch(now time.Time) {
	m.mu.Lock()
	m.lastCtl = now
	m.mu.Unlock()
}

func (m *Model) SetConfig(c ipc.Config) {
	m.mu.Lock()
	m.Cfg.Video, m.Cfg.Display, m.Cfg.Overlay = c.Video, c.Display, c.Overlay
	m.mu.Unlock()
}

func (m *Model) SetState(s ipc.State) {
	m.mu.Lock()
	m.State, m.HaveState = s, true
	m.mu.Unlock()
}

func (m *Model) SetStats(s ipc.Stats) {
	m.mu.Lock()
	m.Stats, m.HaveStats = s, true
	m.mu.Unlock()
}

func (m *Model) SetFPS(f float64) {
	m.mu.Lock()
	m.FPS = f
	m.mu.Unlock()
}

// Snapshot は描画用の写し。
type Snapshot struct {
	Cfg         config.Config
	State       ipc.State
	HaveState   bool
	Stats       ipc.Stats
	HaveStats   bool
	FPS         float64
	ControlLost bool
}

func (m *Model) Snapshot(now time.Time) Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	lost := m.lastCtl.IsZero() || now.Sub(m.lastCtl) > ControlLostAfter
	return Snapshot{Cfg: m.Cfg.Clone(), State: m.State, HaveState: m.HaveState, Stats: m.Stats,
		HaveStats: m.HaveStats, FPS: m.FPS, ControlLost: lost}
}

// ---- タイル ----

// TileView は1枚のタイルの表示内容。
type TileView struct {
	ID    string
	Title string
	Value string
	Color string
}

// Tiles は、設定で選ばれたタイルを、その順に作る。
// lastFrameAge は最後のフレームからの経過(まだ無ければ負)。
func (s Snapshot) Tiles(lastFrameAge time.Duration) []TileView {
	var out []TileView
	for _, id := range s.Cfg.Overlay.Tiles {
		out = append(out, s.tile(id, lastFrameAge))
	}
	return out
}

func (s Snapshot) tile(id string, age time.Duration) TileView {
	st := s.Stats
	statsOK := s.HaveStats && st.OK
	gray := func(title string) TileView { return TileView{ID: id, Title: title, Value: "--", Color: ColorGray} }
	count := func(title string, v int64, hot string) TileView {
		if !statsOK {
			return gray(title)
		}
		c := ColorNeutral
		if hot != "" && v >= 1 {
			c = hot
		}
		return TileView{ID: id, Title: title, Value: fmt.Sprintf("%d", v), Color: c}
	}

	switch id {
	case "connection":
		switch {
		case !statsOK:
			return gray("CONNECTION")
		case st.Connected:
			return TileView{ID: id, Title: "CONNECTION", Value: "Connected", Color: ColorGreen}
		default:
			return TileView{ID: id, Title: "CONNECTION", Value: "Offline", Color: ColorRed}
		}
	case "drop":
		return count("DROPPED", st.Drop, ColorRed)
	case "loss":
		return count("LOSS", st.Loss, ColorOrange)
	case "retrans":
		t := count("RETRANS", st.Retrans, "")
		if t.Color == ColorNeutral {
			t.Color = ColorBlue
		}
		return t
	case "frame_errors":
		return count("FRAME ERRORS", st.FrameErrors, ColorRed)
	case "rtt":
		if !statsOK || !st.Connected { // 切断のときは RTT だけ "--"
			return gray("RTT")
		}
		return TileView{ID: id, Title: "RTT", Value: fmt.Sprintf("%.2f ms", st.RTTMS), Color: ColorBlue}
	case "fps":
		return TileView{ID: id, Title: "FPS", Value: fmt.Sprintf("%.1f", s.FPS), Color: ColorNeutral}
	case "no_signal":
		if age < 0 {
			return TileView{ID: id, Title: "NO SIGNAL", Value: "--", Color: ColorGray}
		}
		c := ColorNeutral
		if age >= time.Second {
			c = ColorOrange
		}
		return TileView{ID: id, Title: "NO SIGNAL", Value: fmt.Sprintf("%.1f s", age.Seconds()), Color: c}
	case "restarts":
		return TileView{ID: id, Title: "RESTARTS", Value: fmt.Sprintf("%d", s.State.RestartCount), Color: ColorNeutral}
	}
	return TileView{ID: id, Title: id, Value: "?", Color: ColorGray}
}

// ---- バッジ ----

// Badge は映像の下端中央に出す状態バッジ。Text が空なら出さない。
type Badge struct {
	Text  string
	Color string
	SW    bool // バッジの横に小さく「SW」(ハードウェアデコードを外している)
}

// StateBadge は ffmpeg の状態から作る。lastFrameAge は最後のフレームからの経過(まだ無ければ負)。
func (s Snapshot) StateBadge(lastFrameAge time.Duration) Badge {
	if !s.Cfg.Display.ShowStateBadge || s.ControlLost || !s.HaveState {
		return Badge{}
	}
	b := Badge{SW: s.State.HWFallback}
	switch s.State.State {
	case ipc.StateStarting:
		b.Text, b.Color = "Connecting", ColorYellow
	case ipc.StateBackoff:
		b.Text, b.Color = "Reconnecting", ColorOrange
		if lastFrameAge >= 0 {
			b.Text += fmt.Sprintf(" %ds", int(lastFrameAge.Seconds()))
		}
	case ipc.StateStopped:
		b.Text, b.Color = "Stopped", ColorGray
	default: // running: バッジなし
		b.SW = false
	}
	return b
}

// ControlLostBadge は下端右に出す「制御切断」。
func (s Snapshot) ControlLostBadge() Badge {
	if !s.Cfg.Display.ShowStateBadge || !s.ControlLost {
		return Badge{}
	}
	return Badge{Text: "Control lost", Color: ColorGray}
}

// ShowNoSignal は、無信号時の表示(黒、画像)へ切り替えるべきか。
// last_frame のときは常に false(最後のフレームを保持する)。
func (s Snapshot) ShowNoSignal() bool {
	if s.Cfg.Display.NoSignal == "last_frame" {
		return false
	}
	if s.ControlLost || !s.HaveState {
		return true
	}
	return s.State.State != ipc.StateRunning
}
