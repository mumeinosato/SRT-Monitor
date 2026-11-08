package player

import (
	"testing"
	"time"

	"srtrx/internal/config"
	"srtrx/internal/ipc"
)

func snap(mut func(*Snapshot)) Snapshot {
	s := Snapshot{Cfg: config.Default(), HaveState: true, HaveStats: true,
		State: ipc.State{State: ipc.StateRunning}, Stats: ipc.Stats{OK: true, Connected: true, RTTMS: 1.034}}
	if mut != nil {
		mut(&s)
	}
	return s
}

func tileByID(ts []TileView, id string) TileView {
	for _, t := range ts {
		if t.ID == id {
			return t
		}
	}
	return TileView{}
}

func TestSixDefaultTilesInOrder(t *testing.T) {
	ts := snap(nil).Tiles(-1)
	ids := []string{"connection", "drop", "loss", "retrans", "frame_errors", "rtt"}
	if len(ts) != 6 {
		t.Fatalf("%d 枚", len(ts))
	}
	for i, id := range ids {
		if ts[i].ID != id {
			t.Errorf("%d 番目: %s", i, ts[i].ID)
		}
	}
}

func TestColorRules(t *testing.T) {
	cases := []struct {
		name  string
		mut   func(*Snapshot)
		id    string
		value string
		color string
	}{
		{"接続中は緑", nil, "connection", "接続中", ColorGreen},
		{"切断は赤", func(s *Snapshot) { s.Stats.Connected = false }, "connection", "切断", ColorRed},
		{"ドロップ0", nil, "drop", "0", ColorNeutral},
		{"ドロップ1以上は赤", func(s *Snapshot) { s.Stats.Drop = 1 }, "drop", "1", ColorRed},
		{"ロス0", nil, "loss", "0", ColorNeutral},
		{"ロス1以上はオレンジ", func(s *Snapshot) { s.Stats.Loss = 12 }, "loss", "12", ColorOrange},
		{"再送は青", func(s *Snapshot) { s.Stats.Retrans = 12 }, "retrans", "12", ColorBlue},
		{"再送0も青", nil, "retrans", "0", ColorBlue},
		{"フレームエラー0", nil, "frame_errors", "0", ColorNeutral},
		{"フレームエラー1以上は赤", func(s *Snapshot) { s.Stats.FrameErrors = 2 }, "frame_errors", "2", ColorRed},
		{"RTTは青", nil, "rtt", "1.03 ms", ColorBlue},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := tileByID(snap(c.mut).Tiles(-1), c.id)
			if got.Value != c.value || got.Color != c.color {
				t.Errorf("%+v", got)
			}
		})
	}
}

func TestStatsFailureGraysEverythingButKeepsNothingElse(t *testing.T) {
	// ok=false のとき、すべてのタイルがグレーの "--"
	s := snap(func(s *Snapshot) { s.Stats.OK = false; s.Stats.Loss = 99 })
	for _, tl := range s.Tiles(-1) {
		if tl.Value != "--" || tl.Color != ColorGray {
			t.Errorf("%s: %+v", tl.ID, tl)
		}
	}
	// 統計をまだ受け取っていない間も同じ
	s = snap(func(s *Snapshot) { s.HaveStats = false })
	if got := tileByID(s.Tiles(-1), "drop"); got.Value != "--" {
		t.Errorf("%+v", got)
	}
}

func TestDisconnectedRedAndOnlyRTTDashed(t *testing.T) {
	s := snap(func(s *Snapshot) { s.Stats.Connected = false; s.Stats.Loss = 7 })
	ts := s.Tiles(-1)
	if c := tileByID(ts, "connection"); c.Value != "切断" || c.Color != ColorRed {
		t.Errorf("%+v", c)
	}
	if r := tileByID(ts, "rtt"); r.Value != "--" || r.Color != ColorGray {
		t.Errorf("RTTだけ --: %+v", r)
	}
	if l := tileByID(ts, "loss"); l.Value != "7" || l.Color != ColorOrange {
		t.Errorf("累計の値は保持: %+v", l)
	}
}

func TestSelectedTilesOnly(t *testing.T) {
	s := snap(func(s *Snapshot) {
		s.Cfg.Overlay.Tiles = []string{"fps", "no_signal", "restarts"}
		s.FPS = 29.97
		s.State.RestartCount = 4
	})
	ts := s.Tiles(1500 * time.Millisecond)
	if len(ts) != 3 || ts[0].Value != "30.0" || ts[1].Value != "1.5 秒" || ts[1].Color != ColorOrange || ts[2].Value != "4" {
		t.Errorf("%+v", ts)
	}
	if v := tileByID(snap(func(s *Snapshot) { s.Cfg.Overlay.Tiles = []string{"no_signal"} }).Tiles(-1), "no_signal"); v.Value != "--" {
		t.Errorf("フレームがまだ無い: %+v", v)
	}
}

func TestBadges(t *testing.T) {
	cases := []struct {
		state string
		age   time.Duration
		text  string
		color string
	}{
		{ipc.StateStarting, -1, "接続中", ColorYellow},
		{ipc.StateRunning, 0, "", ""},
		{ipc.StateBackoff, 4200 * time.Millisecond, "再接続中 4秒", ColorOrange},
		{ipc.StateBackoff, -1, "再接続中", ColorOrange},
		{ipc.StateStopped, 0, "停止中", ColorGray},
	}
	for _, c := range cases {
		s := snap(func(s *Snapshot) { s.State.State = c.state })
		b := s.StateBadge(c.age)
		if b.Text != c.text || b.Color != c.color {
			t.Errorf("%s: %+v", c.state, b)
		}
	}
}

func TestSWMarker(t *testing.T) {
	s := snap(func(s *Snapshot) { s.State.State = ipc.StateStarting; s.State.HWFallback = true })
	if b := s.StateBadge(-1); !b.SW {
		t.Error("バッジの横に SW")
	}
	s = snap(func(s *Snapshot) { s.State.HWFallback = true })
	if b := s.StateBadge(-1); b.Text != "" || b.SW {
		t.Error("running ではバッジなし")
	}
}

func TestControlLostBadgeSeparateAndHidesStateBadge(t *testing.T) {
	s := snap(func(s *Snapshot) { s.ControlLost = true; s.State.State = ipc.StateBackoff })
	if b := s.StateBadge(1 * time.Second); b.Text != "" {
		t.Error("制御切断中は、古い状態のバッジを出さない")
	}
	if b := s.ControlLostBadge(); b.Text != "制御切断" || b.Color != ColorGray {
		t.Errorf("%+v", b)
	}
	if b := snap(nil).ControlLostBadge(); b.Text != "" {
		t.Error("接続中は出さない")
	}
}

func TestShowStateBadgeFalseHidesAll(t *testing.T) {
	s := snap(func(s *Snapshot) {
		s.Cfg.Display.ShowStateBadge = false
		s.State.State = ipc.StateBackoff
		s.ControlLost = true
	})
	if s.StateBadge(0).Text != "" || s.ControlLostBadge().Text != "" {
		t.Error("show_state_badge=false ですべて消す")
	}
}

func TestControlLostAfter3s(t *testing.T) {
	m := NewModel(config.Default())
	t0 := time.Unix(100, 0)
	if !m.Snapshot(t0).ControlLost {
		t.Error("まだ何も来ていない間は制御切断")
	}
	m.Touch(t0)
	if m.Snapshot(t0.Add(2900 * time.Millisecond)).ControlLost {
		t.Error("3秒以内は正常")
	}
	if !m.Snapshot(t0.Add(3100 * time.Millisecond)).ControlLost {
		t.Error("3秒を超えたら制御切断")
	}
	m.Touch(t0.Add(3 * time.Second))
	if m.Snapshot(t0.Add(4 * time.Second)).ControlLost {
		t.Error("来たら復帰")
	}
}

func TestShowNoSignal(t *testing.T) {
	s := snap(func(s *Snapshot) { s.Cfg.Display.NoSignal = "black"; s.State.State = ipc.StateBackoff })
	if !s.ShowNoSignal() {
		t.Error("black: 再接続中は黒")
	}
	s.State.State = ipc.StateRunning
	if s.ShowNoSignal() {
		t.Error("running では映像")
	}
	s.Cfg.Display.NoSignal = "last_frame"
	s.State.State = ipc.StateBackoff
	if s.ShowNoSignal() {
		t.Error("last_frame: 最後のフレームを保持")
	}
}

func TestModelApplyConfig(t *testing.T) {
	m := NewModel(config.Default())
	c := config.Default()
	c.Overlay.Opacity = 0.5
	c.Video.Width = 1280
	m.SetConfig(ipc.NewConfig(c))
	s := m.Snapshot(time.Now())
	if s.Cfg.Overlay.Opacity != 0.5 || s.Cfg.Video.Width != 1280 {
		t.Error("config を反映する")
	}
}
