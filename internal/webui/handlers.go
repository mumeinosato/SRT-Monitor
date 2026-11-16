package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"srtrx/internal/config"
	"srtrx/internal/controller"
	"srtrx/internal/ipc"
	"srtrx/internal/mediamtx"
	"srtrx/internal/player"
	"srtrx/internal/supervisor"
)

// ---- 状態 ----

type procDTO struct {
	State        string  `json:"state"`
	RestartCount int     `json:"restart_count"`
	SinceSec     float64 `json:"since_sec"`
	LastError    string  `json:"last_error"`
	GateWait     bool    `json:"gate_wait"`
}

type tileDTO struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Value string `json:"value"`
	Color string `json:"color"`
}

type statusDTO struct {
	FFmpeg          procDTO `json:"ffmpeg"`
	MediaMTX        procDTO `json:"mediamtx"`
	MediaMTXEnabled bool    `json:"mediamtx_enabled"`
	HWFallback      bool    `json:"hw_fallback"`
	HWAccel         string  `json:"hwaccel"`
	DiagError       string  `json:"diag_error"`
	FFmpegVersion   string  `json:"ffmpeg_version"`
	PortInUse       bool    `json:"port_in_use"`
	Player          struct {
		Connected  bool    `json:"connected"`
		PID        int     `json:"pid"`
		FPS        float64 `json:"fps"`
		FrameAgeMS int64   `json:"frame_age_ms"`
	} `json:"player"`
	Stats struct {
		OK          bool    `json:"ok"`
		Connected   bool    `json:"connected"`
		Drop        int64   `json:"drop"`
		Loss        int64   `json:"loss"`
		Retrans     int64   `json:"retrans"`
		FrameErrors int64   `json:"frame_errors"`
		RTTMS       float64 `json:"rtt_ms"`
	} `json:"stats"`
	Tiles           []tileDTO `json:"tiles"`
	LoadNotes       []string  `json:"load_notes"`
	Locked          bool      `json:"locked"`
	ResetIntervalMS int       `json:"reset_interval_ms"`
}

func proc(s supervisor.Snapshot, now time.Time) procDTO {
	return procDTO{State: s.State, RestartCount: s.RestartCount, SinceSec: now.Sub(s.Since).Seconds(), LastError: s.LastError, GateWait: s.GateWait}
}

func buildStatus(st controller.Status, now time.Time) statusDTO {
	d := statusDTO{
		FFmpeg: proc(st.FFmpeg, now), MediaMTX: proc(st.MediaMTX, now), MediaMTXEnabled: st.MediaMTXEnabled,
		HWFallback: st.HWFallback, HWAccel: st.Config.Video.HWAccel, DiagError: st.DiagError, FFmpegVersion: st.FFmpegVersion,
		PortInUse: st.PortInUse, LoadNotes: st.LoadNotes, Locked: st.Locked, ResetIntervalMS: st.Config.Metrics.ResetIntervalMS,
	}
	if d.LoadNotes == nil {
		d.LoadNotes = []string{}
	}
	d.Player.Connected, d.Player.PID, d.Player.FPS, d.Player.FrameAgeMS = st.Player.Connected, st.Player.PID, st.Player.FPS, st.Player.FrameAgeMS
	d.Stats.OK, d.Stats.Connected = st.Stats.OK, st.Stats.Connected
	d.Stats.Drop, d.Stats.Loss, d.Stats.Retrans, d.Stats.FrameErrors, d.Stats.RTTMS = st.Stats.Drop, st.Stats.Loss, st.Stats.Retrans, st.Stats.FrameErrors, st.Stats.RTTMS
	// 統計タイルは、プレイヤーのオーバーレイと同じ値・色の規則(常に既定の6枚)
	snap := player.Snapshot{Cfg: config.Default(), HaveStats: true, HaveState: true,
		Stats: ipc.Stats{OK: st.Stats.OK, Connected: st.Stats.Connected, Drop: st.Stats.Drop, Loss: st.Stats.Loss,
			Retrans: st.Stats.Retrans, FrameErrors: st.Stats.FrameErrors, RTTMS: st.Stats.RTTMS}}
	d.Tiles = []tileDTO{}
	for _, t := range snap.Tiles(-1) {
		d.Tiles = append(d.Tiles, tileDTO{ID: t.ID, Title: t.Title, Value: t.Value, Color: t.Color})
	}
	return d
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, buildStatus(s.ctl.Status(), time.Now()))
}

// ---- 設定 ----

var tileTitlesJA = map[string]string{
	"connection": "接続状態", "drop": "ドロップ", "loss": "ロス", "retrans": "再送受信",
	"frame_errors": "フレームエラー", "rtt": "RTT", "fps": "受信FPS", "no_signal": "無信号時間", "restarts": "ffmpeg再起動回数",
}

type configResp struct {
	Values     map[string]any    `json:"values"`
	Effective  map[string]any    `json:"effective"` // derived の項目の、自動で決まる値
	Schema     []config.Field    `json:"schema"`
	AllTiles   []string          `json:"all_tiles"`
	TileTitles map[string]string `json:"tile_titles"`
	Profiles   []string          `json:"profiles"`
	Locked     bool              `json:"locked"`
}

func valuesOf(c config.Config) map[string]any {
	m := map[string]any{}
	for _, f := range config.Schema() {
		if v, ok := config.Get(c, f.Key); ok {
			m[f.Key] = v
		}
	}
	return m
}

func effectiveOf(c config.Config) map[string]any {
	eff := c.Effective()
	m := map[string]any{}
	for _, f := range config.Schema() {
		if f.Derived != "" {
			if v, ok := config.Get(eff, f.Key); ok {
				m[f.Key] = v
			}
		}
	}
	return m
}

func (s *Server) handleConfigGet(w http.ResponseWriter, r *http.Request) {
	c := s.ctl.Config()
	profiles := s.ctl.Profiles()
	if profiles == nil {
		profiles = []string{}
	}
	writeJSON(w, http.StatusOK, configResp{
		Values: valuesOf(c), Effective: effectiveOf(c), Schema: config.Schema(), AllTiles: config.AllTiles,
		TileTitles: tileTitlesJA, Profiles: profiles, Locked: c.Lock.Enabled,
	})
}

// buildConfig は、いまの設定へ、画面の値(キー → 値)を重ねた設定を作る。スキーマに無いキー(本番ロックなど)は受けない。
func (s *Server) buildConfig(values map[string]any) (config.Config, map[string]string) {
	c := s.ctl.Config()
	errs := map[string]string{}
	allowed := map[string]bool{}
	for _, f := range config.Schema() {
		allowed[f.Key] = true
	}
	for k, v := range values {
		if !allowed[k] {
			errs[k] = "この項目は画面から変更できない"
			continue
		}
		if err := config.Set(&c, k, v); err != nil {
			errs[k] = err.Error()
		}
	}
	return c, errs
}

func validationMap(err error) map[string]string {
	m := map[string]string{}
	if ve, ok := err.(config.ValidationError); ok {
		for _, fe := range ve {
			if _, dup := m[fe.Field]; !dup {
				m[fe.Field] = fe.Msg
			}
		}
	}
	return m
}

type valuesReq struct {
	Values map[string]any `json:"values"`
}

type checkResp struct {
	OK             bool              `json:"ok"`
	Errors         map[string]string `json:"errors"`
	PendingRestart bool              `json:"pending_restart"`
	Effective      map[string]any    `json:"effective"`
}

func (s *Server) handleConfigCheck(w http.ResponseWriter, r *http.Request) {
	var req valuesReq
	if !readJSON(w, r, &req) {
		return
	}
	c, errs := s.buildConfig(req.Values)
	if len(errs) == 0 {
		for k, msg := range validationMap(c.Validate()) {
			errs[k] = msg
		}
	}
	// 検証のエラーが、画面に無いキー(自動で決まる欄など)に付いたときも、適用は止める
	ok := len(errs) == 0 && c.Validate() == nil
	writeJSON(w, http.StatusOK, checkResp{OK: ok, Errors: errs, PendingRestart: s.ctl.PendingRestart(c), Effective: effectiveOf(c)})
}

type liveReq struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

func (s *Server) handleConfigLive(w http.ResponseWriter, r *http.Request) {
	var req liveReq
	if !readJSON(w, r, &req) {
		return
	}
	var f *config.Field
	for _, sf := range config.Schema() {
		if sf.Key == req.Key {
			sf := sf
			f = &sf
		}
	}
	if f == nil || !f.Live {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "即時に反映できる項目ではない: " + req.Key})
		return
	}
	var setErr error
	err := s.ctl.SetLive(func(c *config.Config) { setErr = config.Set(c, req.Key, req.Value) })
	if setErr != nil {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: setErr.Error(), Errors: map[string]string{req.Key: setErr.Error()}})
		return
	}
	if err != nil {
		failFrom(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleConfigApply(w http.ResponseWriter, r *http.Request) {
	var req valuesReq
	if !readJSON(w, r, &req) {
		return
	}
	c, errs := s.buildConfig(req.Values)
	if len(errs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "値が正しくない", Errors: errs})
		return
	}
	if err := s.ctl.ApplyRestart(c); err != nil {
		failFrom(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- 操作 ----

func (s *Server) handleAction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string `json:"action"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	switch req.Action {
	case "start":
		s.ctl.StartAll()
	case "stop":
		s.ctl.StopAll()
	case "restart":
		s.ctl.RestartAll()
	case "reset":
		s.ctl.ResetCounters()
	default:
		writeJSON(w, http.StatusBadRequest, apiError{Error: "未知の操作: " + req.Action})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLock(w http.ResponseWriter, r *http.Request) {
	var req struct {
		On bool `json:"on"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.ctl.SetLock(req.On); err != nil {
		failFrom(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "locked": req.On})
}

// ---- プロファイル ----

func (s *Server) handleProfilesList(w http.ResponseWriter, r *http.Request) {
	p := s.ctl.Profiles()
	if p == nil {
		p = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": p})
}

func (s *Server) handleProfileSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "保存名を入れてください"})
		return
	}
	if err := s.ctl.SaveProfile(req.Name); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleProfileLoad は、プロファイルの値を返す(適用は、画面が確認のうえ /api/config/apply で行う)。
func (s *Server) handleProfileLoad(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	c, err := s.ctl.LoadProfile(req.Name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"values": valuesOf(c)})
}

// ---- ログと情報 ----

type faultDTO struct {
	Time   string   `json:"time"`
	Target string   `json:"target"`
	Reason string   `json:"reason"`
	Stderr []string `json:"stderr"`
}

type logLine struct {
	Time  string `json:"time"`
	Level string `json:"level"`
	Msg   string `json:"msg"`
	Attrs string `json:"attrs"`
}

func parseLogLine(raw string) logLine {
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return logLine{Level: "INFO", Msg: raw}
	}
	l := logLine{}
	l.Level, _ = m["level"].(string)
	l.Msg, _ = m["msg"].(string)
	if ts, _ := m["time"].(string); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			l.Time = t.Local().Format("15:04:05")
		}
	}
	var keys []string
	for k := range m {
		if k != "time" && k != "level" && k != "msg" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, " %s=%v", k, m[k])
	}
	l.Attrs = strings.TrimSpace(b.String())
	return l
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	faults := []faultDTO{}
	for _, f := range s.ctl.Faults() {
		st := f.Stderr
		if st == nil {
			st = []string{}
		}
		faults = append(faults, faultDTO{Time: f.Time.Local().Format("15:04:05"), Target: f.Target, Reason: f.Reason, Stderr: st})
	}
	lines := []logLine{}
	if s.logs != nil {
		for _, raw := range s.logs.Ring.Tail(500) {
			lines = append(lines, parseLogLine(raw))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"faults": faults, "lines": lines})
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"mediamtx_version": mediamtx.Version(), "license": mediamtx.License})
}
