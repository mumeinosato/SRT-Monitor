package webui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"srtrx/internal/config"
	"srtrx/internal/controller"
	"srtrx/internal/logx"
)

type rig struct {
	t    *testing.T
	ctl  *controller.Controller
	srv  *Server
	h    http.Handler
	logs *logx.Logs
}

func newRig(t *testing.T, opt Options) *rig {
	t.Helper()
	dir := t.TempDir()
	paths := config.PathsFrom(filepath.Join(dir, "run"), filepath.Join(dir, "cfg"), filepath.Join(dir, "state"))
	ctl, err := controller.New(controller.Options{Paths: paths})
	if err != nil {
		t.Fatal(err)
	}
	logs := &logx.Logs{Ring: logx.NewRing(50)}
	s := New(ctl, logs, opt)
	return &rig{t: t, ctl: ctl, srv: s, h: s.Handler(), logs: logs}
}

func (r *rig) do(method, path string, body any, mut ...func(*http.Request)) *httptest.ResponseRecorder {
	r.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Host = "127.0.0.1:8088"
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-SRTRX", "1")
	}
	for _, m := range mut {
		m(req)
	}
	w := httptest.NewRecorder()
	r.h.ServeHTTP(w, req)
	return w
}

func decode(t *testing.T, w *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), v); err != nil {
		t.Fatalf("JSON でない: %v\n%s", err, w.Body.String())
	}
}

func TestStaticFilesServed(t *testing.T) {
	r := newRig(t, Options{})
	for path, want := range map[string]string{"/": "<title>srtrx 制御</title>", "/app.js": "'use strict'", "/app.css": "--bg"} {
		w := r.do("GET", path, nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), want) {
			t.Errorf("%s: %d", path, w.Code)
		}
	}
	if w := r.do("GET", "/", nil); w.Header().Get("Content-Security-Policy") == "" || w.Header().Get("Cache-Control") != "no-store" {
		t.Error("CSP と no-store を付ける")
	}
}

func TestHostHeaderCheckOnLoopback(t *testing.T) {
	r := newRig(t, Options{Addr: "127.0.0.1:8088"})
	for host, want := range map[string]int{
		"127.0.0.1:8088": 200, "localhost:8088": 200, "[::1]:8088": 200,
		"evil.example.com": 403, "evil.example.com:8088": 403, "192.168.1.5:8088": 403,
	} {
		w := r.do("GET", "/api/status", nil, func(q *http.Request) { q.Host = host })
		if w.Code != want {
			t.Errorf("Host=%s: want %d got %d(DNS リバインディング対策)", host, want, w.Code)
		}
	}
	// ループバックでない待ち受け(明示)では、Host の名前は問わない
	r2 := newRig(t, Options{Addr: "0.0.0.0:8088", Token: "tok"})
	w := r2.do("GET", "/api/status", nil, func(q *http.Request) { q.Host = "kiosk.local:8088"; q.Header.Set("Authorization", "Bearer tok") })
	if w.Code != 200 {
		t.Errorf("LAN 向けでは Host を問わない(トークンで守る): %d", w.Code)
	}
}

func TestWritesRequireJSONHeaderAndSameOrigin(t *testing.T) {
	r := newRig(t, Options{})
	body := map[string]string{"action": "reset"}
	cases := map[string]struct {
		mut  func(*http.Request)
		want int
	}{
		"正しい":              {func(q *http.Request) {}, 200},
		"X-SRTRX が無い":      {func(q *http.Request) { q.Header.Del("X-SRTRX") }, 400},
		"Content-Type が違う": {func(q *http.Request) { q.Header.Set("Content-Type", "text/plain") }, 400},
		"別のオリジン":           {func(q *http.Request) { q.Header.Set("Origin", "http://evil.example.com") }, 403},
		"同じオリジン":           {func(q *http.Request) { q.Header.Set("Origin", "http://127.0.0.1:8088") }, 200},
	}
	for name, c := range cases {
		if w := r.do("POST", "/api/action", body, c.mut); w.Code != c.want {
			t.Errorf("%s: want %d got %d", name, c.want, w.Code)
		}
	}
}

func TestTokenRequired(t *testing.T) {
	r := newRig(t, Options{Token: "s3cret"})
	if w := r.do("GET", "/api/status", nil); w.Code != 401 {
		t.Errorf("トークン無しは 401: %d", w.Code)
	}
	if w := r.do("GET", "/api/status", nil, func(q *http.Request) { q.Header.Set("Authorization", "Bearer wrong") }); w.Code != 401 {
		t.Errorf("違うトークンは 401: %d", w.Code)
	}
	if w := r.do("GET", "/api/status", nil, func(q *http.Request) { q.Header.Set("Authorization", "Bearer s3cret") }); w.Code != 200 {
		t.Errorf("Bearer で通る: %d", w.Code)
	}
	// ?token= で Cookie が付き、以降は Cookie で通る。ページは、トークンの無い URL へ戻す
	w := r.do("GET", "/?token=s3cret", nil)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/" {
		t.Errorf("トークンを URL から消す: %d %s", w.Code, w.Header().Get("Location"))
	}
	ck := w.Result().Cookies()
	if len(ck) != 1 || !ck[0].HttpOnly || ck[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("Cookie: %+v", ck)
	}
	if w := r.do("GET", "/api/status", nil, func(q *http.Request) { q.AddCookie(ck[0]) }); w.Code != 200 {
		t.Errorf("Cookie で通る: %d", w.Code)
	}
}

func TestStatusShape(t *testing.T) {
	r := newRig(t, Options{})
	var st statusDTO
	decode(t, r.do("GET", "/api/status", nil), &st)
	if len(st.Tiles) != 6 || st.Tiles[0].ID != "connection" || st.Tiles[0].Title != "CONNECTION" {
		t.Errorf("統計タイルはオーバーレイと同じ6枚: %+v", st.Tiles)
	}
	if st.ResetIntervalMS != 300000 || !st.MediaMTXEnabled || st.LoadNotes == nil {
		t.Errorf("%+v", st)
	}
	if st.FFmpeg.State == "" || st.MediaMTX.State == "" {
		t.Error("状態が入る")
	}
}

func TestConfigGetHasSchemaAndValues(t *testing.T) {
	r := newRig(t, Options{})
	var c configResp
	decode(t, r.do("GET", "/api/config", nil), &c)
	if len(c.Schema) != len(config.Schema()) || c.Values["input.port"] != float64(8890) {
		t.Errorf("schema=%d port=%v", len(c.Schema), c.Values["input.port"])
	}
	if c.Effective["input.host"] != "127.0.0.1" || c.Effective["metrics.url"] == nil {
		t.Errorf("自動で決まる値: %v", c.Effective)
	}
	if _, ok := c.Values["lock.enabled"]; ok {
		t.Error("本番ロックは設定の値に出さない(専用の操作)")
	}
	if len(c.AllTiles) != 9 || c.TileTitles["loss"] != "ロス" {
		t.Errorf("%v %v", c.AllTiles, c.TileTitles)
	}
}

func TestCheckReportsRangeErrorsPerKeyAndPending(t *testing.T) {
	r := newRig(t, Options{})
	var base configResp
	decode(t, r.do("GET", "/api/config", nil), &base)

	// 変更なし: OK、保留なし
	var ck checkResp
	decode(t, r.do("POST", "/api/config/check", valuesReq{Values: base.Values}), &ck)
	if !ck.OK || ck.PendingRestart || len(ck.Errors) != 0 {
		t.Errorf("変更なし: %+v", ck)
	}
	// 再起動が必要な項目を変えた: OK、保留あり
	v := cloneValues(base.Values)
	v["input.latency_ms"] = 500.0
	decode(t, r.do("POST", "/api/config/check", valuesReq{Values: v}), &ck)
	if !ck.OK || !ck.PendingRestart {
		t.Errorf("latency を変えたら保留: %+v", ck)
	}
	// 即時の項目だけ: 保留なし
	v = cloneValues(base.Values)
	v["overlay.opacity"] = 0.4
	decode(t, r.do("POST", "/api/config/check", valuesReq{Values: v}), &ck)
	if !ck.OK || ck.PendingRestart {
		t.Errorf("即時の項目は保留にならない: %+v", ck)
	}
	// 範囲外: 欄ごとの理由
	v = cloneValues(base.Values)
	v["input.port"] = 0.0
	v["overlay.opacity"] = 5.0
	decode(t, r.do("POST", "/api/config/check", valuesReq{Values: v}), &ck)
	if ck.OK || ck.Errors["input.port"] == "" || ck.Errors["overlay.opacity"] == "" {
		t.Errorf("範囲外の欄ごとの理由: %+v", ck)
	}
	// 型が違う、未知のキー、画面から変えられないキー
	v = cloneValues(base.Values)
	v["input.port"] = "abc"
	v["no.such"] = 1.0
	v["lock.enabled"] = true
	decode(t, r.do("POST", "/api/config/check", valuesReq{Values: v}), &ck)
	if ck.OK || ck.Errors["input.port"] == "" || ck.Errors["no.such"] == "" || ck.Errors["lock.enabled"] == "" {
		t.Errorf("%+v", ck)
	}
	// mediamtx.enabled を切ると、自動で決まる値(effective)が、入力先に変わる
	v = cloneValues(base.Values)
	v["mediamtx.enabled"] = false
	v["input.host"] = "10.0.0.9"
	decode(t, r.do("POST", "/api/config/check", valuesReq{Values: v}), &ck)
	if ck.Effective["input.host"] != "10.0.0.9" {
		t.Errorf("外部のMediaMTXなら入力先をそのまま使う: %v", ck.Effective)
	}
}

func cloneValues(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

func TestLiveAppliesSavesAndRejects(t *testing.T) {
	r := newRig(t, Options{})
	w := r.do("POST", "/api/config/live", liveReq{Key: "overlay.opacity", Value: 0.5})
	if w.Code != 200 || r.ctl.Config().Overlay.Opacity != 0.5 {
		t.Fatalf("即時に反映される: %d %s", w.Code, w.Body.String())
	}
	saved, info := config.Load(r.ctl.Paths().ConfigFile)
	if info.Source == "" || saved.Overlay.Opacity != 0.5 {
		t.Error("保存される")
	}
	// 再起動が要る項目は、即時では受けない
	if w := r.do("POST", "/api/config/live", liveReq{Key: "input.latency_ms", Value: 500.0}); w.Code != 400 {
		t.Errorf("再起動の項目: %d", w.Code)
	}
	// 範囲外は欄ごとの理由つきで 422、設定は変わらない
	var e apiError
	w = r.do("POST", "/api/config/live", liveReq{Key: "overlay.opacity", Value: 9.0})
	decode(t, w, &e)
	if w.Code != 422 || e.Errors["overlay.opacity"] == "" || r.ctl.Config().Overlay.Opacity != 0.5 {
		t.Errorf("範囲外: %d %+v", w.Code, e)
	}
	// 型違い
	if w := r.do("POST", "/api/config/live", liveReq{Key: "overlay.opacity", Value: "x"}); w.Code != 422 {
		t.Errorf("型違い: %d", w.Code)
	}
}

func TestApplyRestartAndLock(t *testing.T) {
	r := newRig(t, Options{})
	var base configResp
	decode(t, r.do("GET", "/api/config", nil), &base)
	v := cloneValues(base.Values)
	v["input.latency_ms"] = 500.0
	if w := r.do("POST", "/api/config/apply", valuesReq{Values: v}); w.Code != 200 || r.ctl.Config().Input.LatencyMS != 500 {
		t.Fatalf("適用: %d %s", w.Code, w.Body.String())
	}
	// 範囲外は適用できない(設定は変わらない)
	v["input.latency_ms"] = 5.0
	var e apiError
	w := r.do("POST", "/api/config/apply", valuesReq{Values: v})
	decode(t, w, &e)
	if w.Code != 422 || r.ctl.Config().Input.LatencyMS != 500 {
		t.Errorf("範囲外: %d %+v", w.Code, e)
	}

	// 本番ロック: 変更は 423
	if w := r.do("POST", "/api/lock", map[string]bool{"on": true}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var st statusDTO
	decode(t, r.do("GET", "/api/status", nil), &st)
	if !st.Locked {
		t.Error("ロック中と出る")
	}
	v["input.latency_ms"] = 600.0
	if w := r.do("POST", "/api/config/apply", valuesReq{Values: v}); w.Code != 423 {
		t.Errorf("ロック中は適用できない: %d", w.Code)
	}
	if w := r.do("POST", "/api/config/live", liveReq{Key: "overlay.opacity", Value: 0.4}); w.Code != 423 {
		t.Errorf("ロック中は即時も不可: %d", w.Code)
	}
	if w := r.do("POST", "/api/lock", map[string]bool{"on": false}); w.Code != 200 {
		t.Fatal("解除できる")
	}
	if w := r.do("POST", "/api/config/live", liveReq{Key: "overlay.opacity", Value: 0.4}); w.Code != 200 {
		t.Errorf("解除後は変更できる: %d", w.Code)
	}
}

func TestActions(t *testing.T) {
	r := newRig(t, Options{})
	for _, a := range []string{"start", "stop", "restart", "reset"} {
		if w := r.do("POST", "/api/action", map[string]string{"action": a}); w.Code != 200 {
			t.Errorf("%s: %d", a, w.Code)
		}
	}
	if w := r.do("POST", "/api/action", map[string]string{"action": "format-disk"}); w.Code != 400 {
		t.Errorf("未知の操作: %d", w.Code)
	}
}

func TestProfilesSaveListLoad(t *testing.T) {
	r := newRig(t, Options{})
	if w := r.do("POST", "/api/config/live", liveReq{Key: "overlay.opacity", Value: 0.3}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := r.do("POST", "/api/profiles/save", map[string]string{"name": "本番"}); w.Code != 200 {
		t.Fatalf("%s", w.Body.String())
	}
	if w := r.do("POST", "/api/profiles/save", map[string]string{"name": "../evil"}); w.Code != 400 {
		t.Errorf("不正な名前: %d", w.Code)
	}
	if w := r.do("POST", "/api/profiles/save", map[string]string{"name": " "}); w.Code != 400 {
		t.Errorf("空の名前: %d", w.Code)
	}
	var l struct{ Profiles []string }
	decode(t, r.do("GET", "/api/profiles", nil), &l)
	if len(l.Profiles) != 1 || l.Profiles[0] != "本番" {
		t.Errorf("%v", l.Profiles)
	}
	var ld struct{ Values map[string]any }
	decode(t, r.do("POST", "/api/profiles/load", map[string]string{"name": "本番"}), &ld)
	if ld.Values["overlay.opacity"] != 0.3 {
		t.Errorf("%v", ld.Values["overlay.opacity"])
	}
	if w := r.do("POST", "/api/profiles/load", map[string]string{"name": "無い"}); w.Code != 400 {
		t.Errorf("無いプロファイル: %d", w.Code)
	}
}

func TestLogsAndInfo(t *testing.T) {
	r := newRig(t, Options{})
	r.logs.Ring.Add(`{"time":"2026-11-16T10:00:00Z","level":"WARN","msg":"障害","target":"ffmpeg","reason":"x"}`)
	r.logs.Ring.Add("not json")
	var lg struct {
		Faults []faultDTO
		Lines  []logLine
	}
	decode(t, r.do("GET", "/api/logs", nil), &lg)
	if len(lg.Lines) != 2 || lg.Lines[0].Level != "WARN" || lg.Lines[0].Msg != "障害" || !strings.Contains(lg.Lines[0].Attrs, "target=ffmpeg") {
		t.Errorf("%+v", lg.Lines)
	}
	if lg.Lines[1].Msg != "not json" {
		t.Errorf("JSON でない行はそのまま: %+v", lg.Lines[1])
	}
	if lg.Faults == nil {
		t.Error("障害は空の配列(null にしない)")
	}
	var info struct{ MediaMTXVersion, License string }
	var raw map[string]string
	decode(t, r.do("GET", "/api/info", nil), &raw)
	info.MediaMTXVersion, info.License = raw["mediamtx_version"], raw["license"]
	if info.MediaMTXVersion == "" || !strings.Contains(info.License, "MIT") {
		t.Errorf("%+v", info)
	}
}

func TestBadJSONAndOversizedBody(t *testing.T) {
	r := newRig(t, Options{})
	req := httptest.NewRequest("POST", "/api/action", strings.NewReader("{broken"))
	req.Host = "127.0.0.1:8088"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-SRTRX", "1")
	w := httptest.NewRecorder()
	r.h.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Errorf("壊れた JSON: %d", w.Code)
	}
	big := strings.NewReader(`{"action":"` + strings.Repeat("a", 2<<20) + `"}`)
	req = httptest.NewRequest("POST", "/api/action", big)
	req.Host = "127.0.0.1:8088"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-SRTRX", "1")
	w = httptest.NewRecorder()
	r.h.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Errorf("大きすぎる本文は拒否: %d", w.Code)
	}
}

func TestStartFailsCleanlyWhenPortBusy(t *testing.T) {
	a := newRig(t, Options{Addr: "127.0.0.1:0"})
	if err := a.srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer a.srv.Shutdown(nil2())
	busy := a.srv.ln.Addr().String()
	b := newRig(t, Options{Addr: busy})
	if err := b.srv.Start(); err == nil {
		t.Error("使用中のポートはエラー(映像には影響させない。呼び出し側で記録する)")
	}
	if !strings.HasPrefix(a.srv.URL(), "http://127.0.0.1:") {
		t.Errorf("URL: %s", a.srv.URL())
	}
	c := newRig(t, Options{Addr: "127.0.0.1:0", Token: "a b"})
	if err := c.srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.srv.Shutdown(nil2())
	if !strings.HasSuffix(c.srv.URL(), "/?token=a+b") {
		t.Errorf("トークンは URL エスケープ: %s", c.srv.URL())
	}
}
