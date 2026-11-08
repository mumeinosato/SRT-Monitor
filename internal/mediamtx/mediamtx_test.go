package mediamtx

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"srtrx/internal/config"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func testCfg(t *testing.T) config.MediaMTX {
	c := config.Default().MediaMTX
	c.SRTListen = "127.0.0.1:" + strconv.Itoa(freePort(t))
	c.MetricsListen = "127.0.0.1:" + strconv.Itoa(freePort(t))
	return c
}

func TestEmbeddedChecksumMatchesPinned(t *testing.T) {
	if err := VerifyEmbedded(); err != nil {
		t.Fatal(err)
	}
	if Version() == "" || !strings.Contains(License, "MIT") {
		t.Errorf("VERSION=%q, LICENSE が MIT でない", Version())
	}
}

func TestGenConfigHasOnlyExpectedKeys(t *testing.T) {
	c := config.Default().MediaMTX
	y := GenConfig(c)
	want := `logLevel: warn
rtsp: no
rtmp: no
hls: no
webrtc: no
api: no
srt: yes
srtAddress: ":8890"
metrics: yes
metricsAddress: "127.0.0.1:9998"
paths:
  all_others:
`
	if y != want {
		t.Errorf("\n got:\n%s\nwant:\n%s", y, want)
	}
	c.SRTListen, c.MetricsListen, c.LogLevel = ":9000", "127.0.0.1:9100", "error"
	y = GenConfig(c)
	for _, w := range []string{`srtAddress: ":9000"`, `metricsAddress: "127.0.0.1:9100"`, "logLevel: error"} {
		if !strings.Contains(y, w) {
			t.Errorf("%s が無い", w)
		}
	}
}

func TestExtractIdempotentAndExecutable(t *testing.T) {
	dir := t.TempDir()
	p1, err := Extract(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := "mediamtx-" + ExpectedSHA256()[:8]
	if filepath.Base(p1) != want {
		t.Errorf("名前: %s", p1)
	}
	st, _ := os.Stat(p1)
	if st.Mode().Perm() != 0o700 {
		t.Errorf("モード: %o", st.Mode().Perm())
	}
	mt1 := st.ModTime()
	time.Sleep(20 * time.Millisecond)
	p2, err := Extract(dir)
	if err != nil || p2 != p1 {
		t.Fatal(err)
	}
	if st2, _ := os.Stat(p2); !st2.ModTime().Equal(mt1) {
		t.Error("SHA-256 が一致するなら書き直さない")
	}
	// 壊れていたら書き直す
	os.WriteFile(p1, []byte("corrupt"), 0o700)
	if _, err := Extract(dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p1); sum(b) != ExpectedSHA256() {
		t.Error("壊れたファイルを直していない")
	}
}

func startReal(t *testing.T, cfg config.MediaMTX) (*Process, string) {
	t.Helper()
	dir := t.TempDir()
	bin, err := Extract(filepath.Join(dir, "bin"))
	if err != nil {
		t.Fatal(err)
	}
	ymlPath := filepath.Join(dir, "mediamtx.yml")
	if err := WriteConfig(ymlPath, cfg); err != nil {
		t.Fatal(err)
	}
	p, err := Start(bin, ymlPath, filepath.Join(dir, "mediamtx.pid"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Stop(StopGrace) })
	return p, dir
}

// 生成した設定で同梱バイナリが起動し、/metrics が応答する。
func TestRealBinaryServesMetrics(t *testing.T) {
	cfg := testCfg(t)
	p, dir := startReal(t, cfg)
	url := "http://" + cfg.MetricsListen + "/metrics"
	client := &http.Client{Timeout: 500 * time.Millisecond}

	deadline := time.Now().Add(10 * time.Second)
	var err error
	for time.Now().Before(deadline) {
		if p.Exited() {
			t.Fatalf("MediaMTX が終了した: %s\n%v", p.Exit(), p.Tail(20))
		}
		if err = Ping(context.Background(), client, url); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("/metrics が応答しない: %v\n%v", err, p.Tail(20))
	}
	// pid ファイルがある
	if b, _ := os.ReadFile(filepath.Join(dir, "mediamtx.pid")); strings.TrimSpace(string(b)) != strconv.Itoa(p.PID()) {
		t.Error("pid ファイル")
	}
	// SRTポートも待ち受けている(UDP)
	if _, err := net.ResolveUDPAddr("udp", cfg.SRTListen); err != nil {
		t.Error(err)
	}
}

func TestPortConflictDetected(t *testing.T) {
	cfg := testCfg(t)
	p1, _ := startReal(t, cfg)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	url := "http://" + cfg.MetricsListen + "/metrics"
	for i := 0; i < 100 && Ping(context.Background(), client, url) != nil; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	// 同じポートで2つ目を起動する
	p2, _ := startReal(t, cfg)
	select {
	case <-p2.PortConflict():
	case <-time.After(10 * time.Second):
		t.Fatalf("already in use を検出できない: %v", p2.Tail(10))
	}
	<-p2.Done()
	// 1つ目は無事(他のプロセスは止めない)
	if p1.Exited() {
		t.Error("先に動いていた MediaMTX が止まった")
	}
	if err := syscall.Kill(p1.PID(), 0); err != nil {
		t.Error(err)
	}
}
