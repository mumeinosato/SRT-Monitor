package ffmpeg

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"srtrx/internal/config"
)

func opts(mutate func(*config.Config)) Options {
	c := config.Default()
	c.MediaMTX.Enabled = false
	if mutate != nil {
		mutate(&c)
	}
	return Options{Config: c, Caps: Caps{Path: "/usr/bin/ffmpeg", Major: 6, Minor: 1, HasSRT: true, HasUnix: true},
		HWAccel: c.Video.HWAccel, Output: "/run/user/1000/srtrx/frame.sock"}
}

func TestGoldenDefault(t *testing.T) {
	want := []string{
		"-hide_banner", "-loglevel", "warning",
		"-fflags", "nobuffer", "-flags", "low_delay", "-probesize", "524288", "-analyzeduration", "1000000",
		"-i", "srt://127.0.0.1:8890?streamid=read:nc&mode=caller&latency=120000&timeout=10000000&connect_timeout=3000&tlpktdrop=1",
		"-map", "0:v:0",
		"-vf", "scale=1920:1080,format=rgb24",
		"-an", "-sn", "-dn", "-fps_mode", "passthrough",
		"-f", "rawvideo", "unix:///run/user/1000/srtrx/frame.sock",
	}
	got := BuildArgs(opts(nil))
	if !reflect.DeepEqual(got, want) {
		t.Errorf("\n got: %q\nwant: %q", got, want)
	}
}

func TestUnitConversionLatency500(t *testing.T) {
	// T3: latency_ms=500 → URLには 500000(マイクロ秒)。timeout は ×1000、connect_timeout はそのまま。
	url := BuildURL(opts(func(c *config.Config) {
		c.Input.LatencyMS, c.Input.TimeoutMS, c.Input.ConnectTimeoutMS = 500, 7000, 2500
	}).Config.Input)
	for _, w := range []string{"latency=500000", "timeout=7000000", "connect_timeout=2500"} {
		if !strings.Contains(url, w) {
			t.Errorf("%s が無い: %s", w, url)
		}
	}
}

func TestGoldenListener(t *testing.T) {
	url := BuildURL(opts(func(c *config.Config) {
		c.Input.Mode = "listener"
		c.Input.Host = "0.0.0.0"
		c.Input.Port = 9000
		c.Input.TLPktDrop = false
	}).Config.Input)
	want := "srt://0.0.0.0:9000?mode=listener&latency=120000&timeout=10000000&connect_timeout=3000"
	if url != want {
		t.Errorf("\n got: %s\nwant: %s", url, want)
	}
	if strings.Contains(url, "streamid") {
		t.Error("listener では streamid を付けない")
	}
}

func TestRecvBuffer(t *testing.T) {
	url := BuildURL(opts(func(c *config.Config) { c.Input.RecvBufferBytes = 8388608 }).Config.Input)
	if !strings.HasSuffix(url, "&tlpktdrop=1&recv_buffer_size=8388608") {
		t.Error(url)
	}
	if strings.Contains(BuildURL(opts(nil).Config.Input), "recv_buffer_size") {
		t.Error("0 のときは付けない")
	}
}

func TestIPv6HostBracketed(t *testing.T) {
	url := BuildURL(opts(func(c *config.Config) { c.Input.Host = "::1" }).Config.Input)
	if !strings.HasPrefix(url, "srt://[::1]:8890?") {
		t.Error(url)
	}
}

func TestGoldenVAAPI(t *testing.T) {
	got := BuildArgs(opts(func(c *config.Config) { c.Video.HWAccel = "vaapi"; c.Video.Width, c.Video.Height = 1280, 720 }))
	want := []string{
		"-hide_banner", "-loglevel", "warning",
		"-fflags", "nobuffer", "-flags", "low_delay", "-probesize", "524288", "-analyzeduration", "1000000",
		"-hwaccel", "vaapi", "-hwaccel_device", "/dev/dri/renderD128", "-hwaccel_output_format", "vaapi",
		"-i", "srt://127.0.0.1:8890?streamid=read:nc&mode=caller&latency=120000&timeout=10000000&connect_timeout=3000&tlpktdrop=1",
		"-map", "0:v:0",
		"-vf", "hwdownload,format=nv12,scale=1280:720,format=rgb24",
		"-an", "-sn", "-dn", "-fps_mode", "passthrough",
		"-f", "rawvideo", "unix:///run/user/1000/srtrx/frame.sock",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("\n got: %q\nwant: %q", got, want)
	}
}

func TestGoldenQSV(t *testing.T) {
	got := strings.Join(BuildArgs(opts(func(c *config.Config) { c.Video.HWAccel = "qsv" })), " ")
	if !strings.Contains(got, "-hwaccel qsv -hwaccel_output_format qsv -i ") ||
		!strings.Contains(got, "-vf hwdownload,format=nv12,scale=1920:1080,format=rgb24") {
		t.Error(got)
	}
}

func TestHWFallbackForcesNone(t *testing.T) {
	o := opts(func(c *config.Config) { c.Video.HWAccel = "vaapi" })
	o.HWAccel = "none" // 自動フォールバック中
	got := strings.Join(BuildArgs(o), " ")
	if strings.Contains(got, "hwaccel") || !strings.Contains(got, "-vf scale=1920:1080,format=rgb24") {
		t.Error(got)
	}
}

func TestExtraArgsJustBeforeOutputTail(t *testing.T) {
	got := BuildArgs(opts(func(c *config.Config) { c.Video.ExtraArgs = []string{"-threads", "2"} }))
	s := strings.Join(got, " ")
	if !strings.Contains(s, "-vf scale=1920:1080,format=rgb24 -threads 2 -an -sn -dn -fps_mode passthrough -f rawvideo unix://") {
		t.Error(s)
	}
}

func TestOldFFmpegUsesVsync0(t *testing.T) {
	for _, v := range [][2]int{{4, 4}, {5, 0}} {
		o := opts(nil)
		o.Caps.Major, o.Caps.Minor = v[0], v[1]
		s := strings.Join(BuildArgs(o), " ")
		if !strings.Contains(s, "-vsync 0") || strings.Contains(s, "-fps_mode") {
			t.Errorf("%v: %s", v, s)
		}
	}
	for _, v := range [][2]int{{5, 1}, {6, 0}, {7, 1}} {
		o := opts(nil)
		o.Caps.Major, o.Caps.Minor = v[0], v[1]
		s := strings.Join(BuildArgs(o), " ")
		if strings.Contains(s, "-vsync") || !strings.Contains(s, "-fps_mode passthrough") {
			t.Errorf("%v: %s", v, s)
		}
	}
	o := opts(nil)
	o.Caps.Major = -1 // 版不明(gitビルド)は新しい扱い
	if strings.Contains(strings.Join(BuildArgs(o), " "), "-vsync") {
		t.Error("版不明は -fps_mode")
	}
}

func TestFIFOWhenNoUnixProtocol(t *testing.T) {
	o := opts(nil)
	o.Caps.HasUnix = false
	o.Output = "/run/user/1000/srtrx/frame.fifo"
	got := BuildArgs(o)
	if got[len(got)-1] != "/run/user/1000/srtrx/frame.fifo" || strings.Contains(strings.Join(got, " "), "unix://") {
		t.Errorf("FIFO のパスをそのまま使う: %q", got)
	}
}

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in           string
		text         string
		major, minor int
	}{
		{"ffmpeg version 6.1.1-3ubuntu5 Copyright (c) 2000-2023", "6.1.1-3ubuntu5", 6, 1},
		{"ffmpeg version n6.0 Copyright", "6.0", 6, 0},
		{"ffmpeg version 5.1.4-0+deb12u1 Copyright", "5.1.4-0+deb12u1", 5, 1},
		{"ffmpeg version 7.0 Copyright", "7.0", 7, 0},
		{"ffmpeg version N-113200-g1234abc Copyright", "", -1, 0},
		{"garbage", "", -1, 0},
	}
	for _, c := range cases {
		text, ma, mi := ParseVersion(c.in)
		if text != c.text || ma != c.major || mi != c.minor {
			t.Errorf("%q → %q %d %d", c.in, text, ma, mi)
		}
	}
}

const protocolsSample = `Supported file protocols:
Input:
  async
  file
  srt
  tcp
  unix
Output:
  file
  srt
  tcp
  unix
`

func TestParseProtocols(t *testing.T) {
	in, out := ParseProtocols(protocolsSample)
	if !in["srt"] || !in["unix"] || !out["unix"] || in["gopher"] {
		t.Errorf("%v %v", in, out)
	}
	_, out = ParseProtocols("Input:\n  srt\nOutput:\n  file\n")
	if out["unix"] {
		t.Error("unix が無い")
	}
}

func fakeFFmpeg(t *testing.T, version, protocols string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "ffmpeg")
	script := "#!/bin/sh\ncase \"$*\" in\n*-version*) cat <<'EOF'\n" + version + "\nEOF\n;;\n*-protocols*) cat <<'EOF'\n" + protocols + "\nEOF\n;;\nesac\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDiagnose(t *testing.T) {
	ctx := context.Background()

	caps, err := Diagnose(ctx, fakeFFmpeg(t, "ffmpeg version 6.1.1 Copyright", protocolsSample))
	if err != nil || !caps.HasSRT || !caps.HasUnix || caps.Major != 6 {
		t.Fatalf("正常: %+v %v", caps, err)
	}

	_, err = Diagnose(ctx, fakeFFmpeg(t, "ffmpeg version 5.1.2 Copyright", protocolsSample))
	if !IsDiagError(err) || !strings.Contains(err.Error(), "6.0") {
		t.Errorf("版が古い: %v", err)
	}

	_, err = Diagnose(ctx, fakeFFmpeg(t, "ffmpeg version 6.0 Copyright", "Input:\n  file\nOutput:\n  file\n"))
	if !IsDiagError(err) || !strings.Contains(err.Error(), "srt") {
		t.Errorf("srt が無い: %v", err)
	}

	caps, err = Diagnose(ctx, fakeFFmpeg(t, "ffmpeg version 6.0 Copyright", "Input:\n  srt\nOutput:\n  file\n"))
	if err != nil || caps.HasUnix {
		t.Errorf("unix が無いのはエラーではない(FIFOへ切り替える): %+v %v", caps, err)
	}

	_, err = Diagnose(ctx, filepath.Join(t.TempDir(), "no-such-ffmpeg"))
	if !IsDiagError(err) || !strings.Contains(err.Error(), "見つからない") {
		t.Errorf("見つからない: %v", err)
	}
}

func TestDiagnoseRealFFmpegIfPresent(t *testing.T) {
	caps, err := Diagnose(context.Background(), "")
	if err != nil {
		t.Skipf("この環境の ffmpeg は要件を満たさない: %v", err)
	}
	t.Logf("ffmpeg %s srt=%v unix=%v", caps.VersionText, caps.HasSRT, caps.HasUnix)
}

func TestProbeOptionsAreConfigurableAndInMicroseconds(t *testing.T) {
	got := strings.Join(BuildArgs(opts(func(c *config.Config) {
		c.Input.ProbeSizeBytes, c.Input.AnalyzeDurationMS = 5_000_000, 3000
	})), " ")
	if !strings.Contains(got, "-probesize 5000000 -analyzeduration 3000000 ") {
		t.Errorf("analyzeduration はミリ秒×1000(マイクロ秒): %s", got)
	}
	// 既定は旧既定(32KB)より大きい。キーフレームの途中から繋いでも、次のキーフレームまで解析を続けられる
	d := config.Default().Input
	if d.ProbeSizeBytes <= 32768 || d.AnalyzeDurationMS <= 0 {
		t.Errorf("既定の解析の上限が小さい: %+v", d)
	}
}
