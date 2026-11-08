package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("既定値が検証を通らない: %v", err)
	}
}

func TestDefaultsMatchSpec(t *testing.T) {
	d := Default()
	if d.Input.LatencyMS != 120 || d.Input.TimeoutMS != 5000 || d.Input.ConnectTimeoutMS != 3000 ||
		d.Input.Port != 8890 || d.Input.Path != "nc" || d.Input.Mode != "caller" || !d.Input.TLPktDrop {
		t.Errorf("input の既定値が仕様と違う: %+v", d.Input)
	}
	if d.Video.Width != 1920 || d.Video.Height != 1080 || d.Video.FPS != 30 || d.Video.HWAccel != "none" {
		t.Errorf("video の既定値が仕様と違う: %+v", d.Video)
	}
	s := d.Supervisor
	if s.BackoffInitialMS != 200 || s.BackoffMaxMS != 2000 || s.BackoffResetAfterMS != 10000 ||
		s.HWFallbackFailMS != 2000 || s.HWFallbackFailCount != 3 || s.WatchdogMinMS != 3000 {
		t.Errorf("supervisor の既定値が仕様と違う: %+v", s)
	}
	if len(d.Overlay.Tiles) != 6 || d.Overlay.Opacity != 0.85 || d.Overlay.Hotkey != "o" {
		t.Errorf("overlay の既定値が仕様と違う: %+v", d.Overlay)
	}
	if d.MediaMTX.ReadyTimeoutMS != 5000 || d.MediaMTX.HangMS != 10000 {
		t.Errorf("mediamtx の既定値が仕様と違う: %+v", d.MediaMTX)
	}
}

func TestValidateRanges(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
		field  string
	}{
		{"port 0", func(c *Config) { c.Input.Port = 0 }, "input.port"},
		{"port 65536", func(c *Config) { c.Input.Port = 65536 }, "input.port"},
		{"latency 19", func(c *Config) { c.Input.LatencyMS = 19 }, "input.latency_ms"},
		{"latency 10001", func(c *Config) { c.Input.LatencyMS = 10001 }, "input.latency_ms"},
		{"timeout 999", func(c *Config) { c.Input.TimeoutMS = 999 }, "input.timeout_ms"},
		{"connect 499", func(c *Config) { c.Input.ConnectTimeoutMS = 499 }, "input.connect_timeout_ms"},
		{"width 319", func(c *Config) { c.Video.Width = 319 }, "video.width"},
		{"width odd", func(c *Config) { c.Video.Width = 1921 }, "video.width"},
		{"height odd", func(c *Config) { c.Video.Height = 1081 }, "video.height"},
		{"height 2162", func(c *Config) { c.Video.Height = 2162 }, "video.height"},
		{"fps 4", func(c *Config) { c.Video.FPS = 4 }, "video.fps"},
		{"fps 61", func(c *Config) { c.Video.FPS = 61 }, "video.fps"},
		{"buffer 31", func(c *Config) { c.Video.PlayerBufferFrames = 31 }, "video.player_buffer_frames"},
		{"backoff init 49", func(c *Config) { c.Supervisor.BackoffInitialMS = 49 }, "supervisor.backoff_initial_ms"},
		{"backoff max < init", func(c *Config) { c.Supervisor.BackoffMaxMS = 100 }, "supervisor.backoff_max_ms"},
		{"backoff max 30001", func(c *Config) { c.Supervisor.BackoffMaxMS = 30001 }, "supervisor.backoff_max_ms"},
		{"watchdog 999", func(c *Config) { c.Supervisor.WatchdogMinMS = 999 }, "supervisor.watchdog_min_ms"},
		{"interval 199", func(c *Config) { c.Metrics.IntervalMS = 199 }, "metrics.interval_ms"},
		{"opacity 0.09", func(c *Config) { c.Overlay.Opacity = 0.09 }, "overlay.opacity"},
		{"opacity 1.01", func(c *Config) { c.Overlay.Opacity = 1.01 }, "overlay.opacity"},
		{"scale 0.4", func(c *Config) { c.Overlay.TileScale = 0.4 }, "overlay.tile_scale"},
		{"scale 3.1", func(c *Config) { c.Overlay.TileScale = 3.1 }, "overlay.tile_scale"},
		{"mode", func(c *Config) { c.Input.Mode = "rendezvous" }, "input.mode"},
		{"hwaccel", func(c *Config) { c.Video.HWAccel = "cuda" }, "video.hwaccel"},
		{"tile", func(c *Config) { c.Overlay.Tiles = []string{"bogus"} }, "overlay.tiles"},
		{"image path", func(c *Config) { c.Display.NoSignal = "image" }, "display.no_signal_image"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			tc.mutate(&c)
			err := c.Validate()
			ve, ok := err.(ValidationError)
			if !ok || !ve.Has(tc.field) {
				t.Fatalf("%s のエラーを期待したが: %v", tc.field, err)
			}
		})
	}
}

func TestValidateBoundariesPass(t *testing.T) {
	c := Default()
	c.Input.Port, c.Input.LatencyMS, c.Input.TimeoutMS, c.Input.ConnectTimeoutMS = 65535, 20, 60000, 30000
	c.Video.Width, c.Video.Height, c.Video.FPS, c.Video.PlayerBufferFrames = 3840, 2160, 60, 30
	c.Overlay.Opacity, c.Overlay.TileScale = 0.1, 3.0
	c.Supervisor.BackoffInitialMS, c.Supervisor.BackoffMaxMS = 50, 50
	if err := c.Validate(); err != nil {
		t.Fatalf("境界値が通らない: %v", err)
	}
}

func TestEffectiveDerivesFromMediaMTX(t *testing.T) {
	c := Default()
	c.Input.Host, c.Input.Port = "10.0.0.1", 1
	c.Metrics.URL = "http://example.invalid/metrics"
	c.MediaMTX.SRTListen = ":9000"
	c.MediaMTX.MetricsListen = "127.0.0.1:9100"
	e := c.Effective()
	if e.Input.Host != "127.0.0.1" || e.Input.Port != 9000 || e.Metrics.URL != "http://127.0.0.1:9100/metrics" {
		t.Errorf("Effective: %+v %s", e.Input, e.Metrics.URL)
	}
	c.MediaMTX.Enabled = false
	e = c.Effective()
	if e.Input.Host != "10.0.0.1" || e.Input.Port != 1 || e.Metrics.URL != "http://example.invalid/metrics" {
		t.Errorf("外部MediaMTXのときは input/metrics をそのまま使う: %+v", e.Input)
	}
}

func TestWatchdogThreshold(t *testing.T) {
	c := Default()
	if got := c.WatchdogThresholdMS(); got != 3000 {
		t.Errorf("120ms×2 < 3000 なので 3000, got %d", got)
	}
	c.Input.LatencyMS = 3000
	if got := c.WatchdogThresholdMS(); got != 6000 {
		t.Errorf("3000ms×2=6000, got %d", got)
	}
	c.Input.LatencyMS = 500
	if got := c.WatchdogThresholdMS(); got != 3000 {
		t.Errorf("500ms×2=1000 < 3000, got %d", got)
	}
}

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	c := Default()
	c.Input.LatencyMS = 500
	c.Overlay.Tiles = []string{"rtt", "fps"}
	c.Video.ExtraArgs = []string{"-threads", "2"}
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	got, info := Load(p)
	if info.Source != p || len(info.Notes) != 0 {
		t.Fatalf("info: %+v", info)
	}
	if got.Input.LatencyMS != 500 || len(got.Overlay.Tiles) != 2 || got.Video.ExtraArgs[1] != "2" {
		t.Errorf("往復で値が変わった: %+v", got)
	}
}

func TestFirstStartUsesDefaults(t *testing.T) {
	got, info := Load(filepath.Join(t.TempDir(), "config.toml"))
	if !info.FirstStart || got.Input.Port != 8890 {
		t.Errorf("初回は既定値: %+v %+v", got, info)
	}
}

func TestMissingKeysUseDefaultsUnknownIgnored(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(p, []byte("[input]\nlatency_ms = 250\nbogus = 1\n[nosuch]\nx = 1\n"), 0o644)
	got, info := Load(p)
	if got.Input.LatencyMS != 250 || got.Input.Port != 8890 {
		t.Errorf("無いキーは既定値: %+v", got.Input)
	}
	joined := strings.Join(info.UnknownKeys, ",")
	if !strings.Contains(joined, "input.bogus") || !strings.Contains(joined, "nosuch") {
		t.Errorf("未知のキーを記録する: %v", info.UnknownKeys)
	}
}

func saveN(t *testing.T, p string, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		c := Default()
		c.Input.LatencyMS = 100 * i // 世代 i の目印
		if err := Save(p, c); err != nil {
			t.Fatal(err)
		}
	}
}

func latencyOf(t *testing.T, p string) int {
	t.Helper()
	c, _, err := loadFile(p)
	if err != nil {
		t.Fatalf("%s: %v", p, err)
	}
	return c.Input.LatencyMS
}

func TestGenerationsRotate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	saveN(t, p, 5)
	want := map[string]int{p: 500, p + ".bak1": 400, p + ".bak2": 300, p + ".bak3": 200}
	for f, v := range want {
		if got := latencyOf(t, f); got != v {
			t.Errorf("%s: want %d got %d", filepath.Base(f), v, got)
		}
	}
	if _, err := os.Stat(p + ".bak4"); err == nil {
		t.Error("bak4 が出来てはいけない")
	}
}

func TestSameContentDoesNotShiftGenerations(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	saveN(t, p, 2)
	c, _, _ := loadFile(p)
	for i := 0; i < 5; i++ {
		if err := Save(p, c); err != nil {
			t.Fatal(err)
		}
	}
	if got := latencyOf(t, p+".bak1"); got != 100 {
		t.Errorf("同一内容の保存で bak1 が押し出された: %d", got)
	}
}

func TestSaveRejectsInvalidAndLeavesFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	saveN(t, p, 1)
	c := Default()
	c.Input.Port = 0
	if err := Save(p, c); err == nil {
		t.Fatal("範囲外は保存できない")
	}
	if got := latencyOf(t, p); got != 100 {
		t.Errorf("失敗した保存で元のファイルが変わった: %d", got)
	}
}

func TestCorruptFallsBackThroughBackups(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	saveN(t, p, 4) // 現在400, bak1=300, bak2=200, bak3=100

	// 壊れた構文
	os.WriteFile(p, []byte("[input\nthis is not toml"), 0o644)
	got, info := Load(p)
	if got.Input.LatencyMS != 300 || info.Source != p+".bak1" {
		t.Fatalf("bak1 で起動する: %d %+v", got.Input.LatencyMS, info)
	}
	if !strings.Contains(strings.Join(info.Notes, "\n"), "bak1") {
		t.Errorf("理由に bak1 が出る: %v", info.Notes)
	}

	// 範囲外の値(構文は正しい)は、ファイル全体を使わない
	os.WriteFile(p, []byte("[input]\nport = 99999\n"), 0o644)
	os.WriteFile(p+".bak1", []byte("[input]\nlatency_ms = 1\n"), 0o644) // これも範囲外
	got, info = Load(p)
	if got.Input.LatencyMS != 200 || info.Source != p+".bak2" {
		t.Fatalf("bak2 で起動する: %d %+v", got.Input.LatencyMS, info)
	}

	// 読めない行
	os.WriteFile(p+".bak2", []byte("garbage ="), 0o644)
	got, info = Load(p)
	if got.Input.LatencyMS != 100 || info.Source != p+".bak3" {
		t.Fatalf("bak3 で起動する: %d %+v", got.Input.LatencyMS, info)
	}

	// 全滅
	os.WriteFile(p+".bak3", []byte("[["), 0o644)
	got, info = Load(p)
	if info.Source != "" || got.Input.LatencyMS != 120 {
		t.Fatalf("全滅なら既定値: %+v %+v", got.Input, info)
	}
	if !strings.Contains(strings.Join(info.Notes, "\n"), "既定値") {
		t.Errorf("理由を残す: %v", info.Notes)
	}
}

func TestProfiles(t *testing.T) {
	cf := filepath.Join(t.TempDir(), "config.toml")
	c := Default()
	c.Input.LatencyMS = 777
	if err := SaveProfile(cf, "本番", c); err != nil {
		t.Fatal(err)
	}
	if err := SaveProfile(cf, "../evil", c); err == nil {
		t.Error("パス区切りを含む名前は拒否する")
	}
	if names := ListProfiles(cf); len(names) != 1 || names[0] != "本番" {
		t.Errorf("ListProfiles: %v", names)
	}
	got, err := LoadProfile(cf, "本番")
	if err != nil || got.Input.LatencyMS != 777 {
		t.Errorf("LoadProfile: %v %+v", err, got.Input)
	}
}

func TestPathsFrom(t *testing.T) {
	p := PathsFrom("/run/user/1000", "/home/u/.config/srtrx", "/home/u/.local/state/srtrx")
	if p.FrameSock != "/run/user/1000/srtrx/frame.sock" || p.ControlSock != "/run/user/1000/srtrx/control.sock" ||
		p.ConfigFile != "/home/u/.config/srtrx/config.toml" || p.LogFile != "/home/u/.local/state/srtrx/srtrx.log" {
		t.Errorf("paths: %+v", p)
	}
}
