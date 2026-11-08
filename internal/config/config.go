// Package config は設定ファイルの読み書き、既定値、検証、世代保存を担う。
package config

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Config は config.toml 全体。GUIの項目とTOMLのキーは1対1。
type Config struct {
	Input      Input      `toml:"input" json:"input"`
	Video      Video      `toml:"video" json:"video"`
	Supervisor Supervisor `toml:"supervisor" json:"supervisor"`
	Metrics    Metrics    `toml:"metrics" json:"metrics"`
	Display    Display    `toml:"display" json:"display"`
	Overlay    Overlay    `toml:"overlay" json:"overlay"`
	Lock       Lock       `toml:"lock" json:"lock"`
	MediaMTX   MediaMTX   `toml:"mediamtx" json:"mediamtx"`
}

type Input struct {
	Host             string `toml:"host" json:"host"`
	Port             int    `toml:"port" json:"port"`
	Path             string `toml:"path" json:"path"`
	Mode             string `toml:"mode" json:"mode"` // caller | listener
	LatencyMS        int    `toml:"latency_ms" json:"latency_ms"`
	TimeoutMS        int    `toml:"timeout_ms" json:"timeout_ms"`
	ConnectTimeoutMS int    `toml:"connect_timeout_ms" json:"connect_timeout_ms"`
	RecvBufferBytes  int    `toml:"recv_buffer_bytes" json:"recv_buffer_bytes"`
	TLPktDrop        bool   `toml:"tlpktdrop" json:"tlpktdrop"`
	// ffmpeg の入力解析の上限。キーフレームの途中から繋ぐと、SPS を含む次のキーフレームまで解析できない。
	// 上限が小さいと(旧既定の32KB)、ffmpeg 8 は諦めて終了する。早く見つかれば早く返るので、大きくして遅延は増えない。
	ProbeSizeBytes    int `toml:"probesize_bytes" json:"probesize_bytes"`
	AnalyzeDurationMS int `toml:"analyzeduration_ms" json:"analyzeduration_ms"`
}

type Video struct {
	Width              int      `toml:"width" json:"width"`
	Height             int      `toml:"height" json:"height"`
	FPS                int      `toml:"fps" json:"fps"`
	HWAccel            string   `toml:"hwaccel" json:"hwaccel"` // none | vaapi | qsv
	PlayerBufferFrames int      `toml:"player_buffer_frames" json:"player_buffer_frames"`
	ExtraArgs          []string `toml:"extra_args" json:"extra_args"`
}

type Supervisor struct {
	WatchdogMinMS         int     `toml:"watchdog_min_ms" json:"watchdog_min_ms"`
	WatchdogLatencyFactor float64 `toml:"watchdog_latency_factor" json:"watchdog_latency_factor"`
	StartTimeoutMS        int     `toml:"start_timeout_ms" json:"start_timeout_ms"`
	BackoffInitialMS      int     `toml:"backoff_initial_ms" json:"backoff_initial_ms"`
	BackoffMaxMS          int     `toml:"backoff_max_ms" json:"backoff_max_ms"`
	BackoffResetAfterMS   int     `toml:"backoff_reset_after_ms" json:"backoff_reset_after_ms"`
	HWFallbackFailMS      int     `toml:"hw_fallback_fail_ms" json:"hw_fallback_fail_ms"`
	HWFallbackFailCount   int     `toml:"hw_fallback_fail_count" json:"hw_fallback_fail_count"`
}

type Metrics struct {
	URL        string `toml:"url" json:"url"`
	IntervalMS int    `toml:"interval_ms" json:"interval_ms"`
	TimeoutMS  int    `toml:"timeout_ms" json:"timeout_ms"`
}

type Display struct {
	Monitor        string `toml:"monitor" json:"monitor"` // primary | コネクター名
	Fullscreen     bool   `toml:"fullscreen" json:"fullscreen"`
	ShowStateBadge bool   `toml:"show_state_badge" json:"show_state_badge"`
	NoSignal       string `toml:"no_signal" json:"no_signal"` // last_frame | black | image
	NoSignalImage  string `toml:"no_signal_image" json:"no_signal_image"`
}

type Overlay struct {
	Enabled   bool     `toml:"enabled" json:"enabled"`
	Position  string   `toml:"position" json:"position"` // top | bottom
	TileScale float64  `toml:"tile_scale" json:"tile_scale"`
	Opacity   float64  `toml:"opacity" json:"opacity"`
	Hotkey    string   `toml:"hotkey" json:"hotkey"`
	Tiles     []string `toml:"tiles" json:"tiles"`
}

type Lock struct {
	Enabled bool `toml:"enabled" json:"enabled"`
}

type MediaMTX struct {
	Enabled        bool   `toml:"enabled" json:"enabled"`
	SRTListen      string `toml:"srt_listen" json:"srt_listen"`
	MetricsListen  string `toml:"metrics_listen" json:"metrics_listen"`
	LogLevel       string `toml:"log_level" json:"log_level"`
	ReadyTimeoutMS int    `toml:"ready_timeout_ms" json:"ready_timeout_ms"`
	HangMS         int    `toml:"hang_ms" json:"hang_ms"`
}

// AllTiles は選べるタイルの全種類(既定の6枚+アプリ状態の3枚)。
var AllTiles = []string{"connection", "drop", "loss", "retrans", "frame_errors", "rtt", "fps", "no_signal", "restarts"}

// Default は仕様書の既定値を返す。
func Default() Config {
	return Config{
		Input: Input{
			Host: "127.0.0.1", Port: 8890, Path: "nc", Mode: "caller",
			LatencyMS: 120, TimeoutMS: 10000, ConnectTimeoutMS: 3000,
			RecvBufferBytes: 0, TLPktDrop: true,
			ProbeSizeBytes: 524_288, AnalyzeDurationMS: 1000,
		},
		Video: Video{
			Width: 1920, Height: 1080, FPS: 30, HWAccel: "none",
			PlayerBufferFrames: 0, ExtraArgs: []string{},
		},
		Supervisor: Supervisor{
			WatchdogMinMS: 3000, WatchdogLatencyFactor: 2.0, StartTimeoutMS: 15000,
			BackoffInitialMS: 200, BackoffMaxMS: 2000, BackoffResetAfterMS: 10000,
			HWFallbackFailMS: 2000, HWFallbackFailCount: 3,
		},
		Metrics: Metrics{URL: "http://127.0.0.1:9998/metrics", IntervalMS: 1000, TimeoutMS: 800},
		Display: Display{
			Monitor: "primary", Fullscreen: true, ShowStateBadge: true,
			NoSignal: "last_frame", NoSignalImage: "",
		},
		Overlay: Overlay{
			Enabled: true, Position: "top", TileScale: 1.0, Opacity: 0.85, Hotkey: "o",
			Tiles: []string{"connection", "drop", "loss", "retrans", "frame_errors", "rtt"},
		},
		Lock: Lock{Enabled: false},
		MediaMTX: MediaMTX{
			Enabled: true, SRTListen: ":8890", MetricsListen: "127.0.0.1:9998",
			LogLevel: "warn", ReadyTimeoutMS: 5000, HangMS: 10000,
		},
	}
}

// Clone は深いコピーを返す(スライスを共有しない)。
func (c Config) Clone() Config {
	d := c
	d.Video.ExtraArgs = append([]string{}, c.Video.ExtraArgs...)
	d.Overlay.Tiles = append([]string{}, c.Overlay.Tiles...)
	return d
}

// WatchdogThresholdMS は max(watchdog_min_ms, latency_ms × 倍率)。
func (c Config) WatchdogThresholdMS() int {
	v := int(float64(c.Input.LatencyMS) * c.Supervisor.WatchdogLatencyFactor)
	if v < c.Supervisor.WatchdogMinMS {
		return c.Supervisor.WatchdogMinMS
	}
	return v
}

// StartTimeoutMSEffective は ffmpeg の「起動してから最初のフレームまで」に許す時間。
// 送信の途中から繋ぐと、次のキーフレームが来るまで映像が出せないので、ウォッチドッグより長く取る
// (短いと、キーフレーム間隔の長い送信で、起動し直しを永久に繰り返す)。ウォッチドッグのしきい値より短くはしない。
func (c Config) StartTimeoutMSEffective() int {
	if w := c.WatchdogThresholdMS(); c.Supervisor.StartTimeoutMS < w {
		return w
	}
	return c.Supervisor.StartTimeoutMS
}

// Effective は mediamtx.enabled のとき、input.host/port と metrics.url を
// srt_listen / metrics_listen から決めた設定を返す。
func (c Config) Effective() Config {
	e := c.Clone()
	if !e.MediaMTX.Enabled {
		return e
	}
	host, port := splitListen(e.MediaMTX.SRTListen, 8890)
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	e.Input.Host, e.Input.Port = host, port
	mh, mp := splitListen(e.MediaMTX.MetricsListen, 9998)
	if mh == "" || mh == "0.0.0.0" || mh == "::" {
		mh = "127.0.0.1"
	}
	e.Metrics.URL = "http://" + net.JoinHostPort(mh, strconv.Itoa(mp)) + "/metrics"
	return e
}

func splitListen(addr string, defPort int) (string, int) {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "", defPort
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		return h, defPort
	}
	return h, n
}

// FieldError は検証エラー1件。Field は "input.port" のようなキー。
type FieldError struct {
	Field string
	Msg   string
}

func (e FieldError) Error() string { return e.Field + ": " + e.Msg }

// ValidationError は検証エラーの集まり。
type ValidationError []FieldError

func (v ValidationError) Error() string {
	parts := make([]string, len(v))
	for i, e := range v {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "; ")
}

// Has は指定フィールドにエラーがあるか。
func (v ValidationError) Has(field string) bool {
	for _, e := range v {
		if e.Field == field {
			return true
		}
	}
	return false
}

// Validate は範囲検証。エラーが無ければ nil。
func (c Config) Validate() error {
	var errs ValidationError
	add := func(field, format string, a ...any) {
		errs = append(errs, FieldError{field, fmt.Sprintf(format, a...)})
	}
	rng := func(field string, v, lo, hi int) {
		if v < lo || v > hi {
			add(field, "%d は %d から %d の範囲外", v, lo, hi)
		}
	}
	rngf := func(field string, v, lo, hi float64) {
		if v < lo || v > hi {
			add(field, "%g は %g から %g の範囲外", v, lo, hi)
		}
	}
	oneOf := func(field, v string, ok ...string) {
		for _, o := range ok {
			if v == o {
				return
			}
		}
		add(field, "%q は %s のどれかでなければならない", v, strings.Join(ok, " | "))
	}

	if strings.TrimSpace(c.Input.Host) == "" {
		add("input.host", "空にできない")
	}
	rng("input.port", c.Input.Port, 1, 65535)
	if strings.TrimSpace(c.Input.Path) == "" || strings.ContainsAny(c.Input.Path, "?&# ") {
		add("input.path", "空、または ? & # 空白を含めることはできない")
	}
	oneOf("input.mode", c.Input.Mode, "caller", "listener")
	rng("input.latency_ms", c.Input.LatencyMS, 20, 10000)
	rng("input.timeout_ms", c.Input.TimeoutMS, 1000, 60000)
	rng("input.connect_timeout_ms", c.Input.ConnectTimeoutMS, 500, 30000)
	rng("input.probesize_bytes", c.Input.ProbeSizeBytes, 32768, 200_000_000)
	rng("input.analyzeduration_ms", c.Input.AnalyzeDurationMS, 0, 120000)
	if c.Input.RecvBufferBytes < 0 {
		add("input.recv_buffer_bytes", "負の値は不可")
	}

	rng("video.width", c.Video.Width, 320, 3840)
	rng("video.height", c.Video.Height, 240, 2160)
	if c.Video.Width%2 != 0 {
		add("video.width", "偶数でなければならない")
	}
	if c.Video.Height%2 != 0 {
		add("video.height", "偶数でなければならない")
	}
	rng("video.fps", c.Video.FPS, 5, 60)
	oneOf("video.hwaccel", c.Video.HWAccel, "none", "vaapi", "qsv")
	rng("video.player_buffer_frames", c.Video.PlayerBufferFrames, 0, 30)

	rng("supervisor.backoff_initial_ms", c.Supervisor.BackoffInitialMS, 50, 5000)
	if c.Supervisor.BackoffMaxMS < c.Supervisor.BackoffInitialMS || c.Supervisor.BackoffMaxMS > 30000 {
		add("supervisor.backoff_max_ms", "%d は backoff_initial_ms 以上 30000 以下でなければならない", c.Supervisor.BackoffMaxMS)
	}
	rng("supervisor.watchdog_min_ms", c.Supervisor.WatchdogMinMS, 1000, 60000)
	rngf("supervisor.watchdog_latency_factor", c.Supervisor.WatchdogLatencyFactor, 1.0, 20.0)
	rng("supervisor.start_timeout_ms", c.Supervisor.StartTimeoutMS, 1000, 120000)
	rng("supervisor.backoff_reset_after_ms", c.Supervisor.BackoffResetAfterMS, 1000, 600000)
	rng("supervisor.hw_fallback_fail_ms", c.Supervisor.HWFallbackFailMS, 100, 60000)
	rng("supervisor.hw_fallback_fail_count", c.Supervisor.HWFallbackFailCount, 1, 100)

	rng("metrics.interval_ms", c.Metrics.IntervalMS, 200, 10000)
	rng("metrics.timeout_ms", c.Metrics.TimeoutMS, 100, 10000)
	if u, err := url.Parse(c.Metrics.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		add("metrics.url", "http(s) のURLでなければならない")
	}

	if strings.TrimSpace(c.Display.Monitor) == "" {
		add("display.monitor", "空にできない")
	}
	oneOf("display.no_signal", c.Display.NoSignal, "last_frame", "black", "image")
	if c.Display.NoSignal == "image" && c.Display.NoSignalImage == "" {
		add("display.no_signal_image", "no_signal = image のときは画像のパスが要る")
	}

	rngf("overlay.opacity", c.Overlay.Opacity, 0.1, 1.0)
	rngf("overlay.tile_scale", c.Overlay.TileScale, 0.5, 3.0)
	oneOf("overlay.position", c.Overlay.Position, "top", "bottom")
	if len([]rune(c.Overlay.Hotkey)) != 1 {
		add("overlay.hotkey", "1文字でなければならない")
	}
	for _, t := range c.Overlay.Tiles {
		known := false
		for _, a := range AllTiles {
			if a == t {
				known = true
			}
		}
		if !known {
			add("overlay.tiles", "未知のタイル %q", t)
		}
	}

	if c.MediaMTX.Enabled {
		if _, _, err := net.SplitHostPort(c.MediaMTX.MetricsListen); err != nil {
			add("mediamtx.metrics_listen", "host:port 形式でなければならない")
		}
		if _, _, err := net.SplitHostPort(c.MediaMTX.SRTListen); err != nil {
			add("mediamtx.srt_listen", "host:port 形式でなければならない")
		}
	}
	oneOf("mediamtx.log_level", c.MediaMTX.LogLevel, "error", "warn", "info", "debug")
	rng("mediamtx.ready_timeout_ms", c.MediaMTX.ReadyTimeoutMS, 500, 60000)
	rng("mediamtx.hang_ms", c.MediaMTX.HangMS, 1000, 120000)

	if len(errs) == 0 {
		return nil
	}
	return errs
}
