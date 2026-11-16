// Package webui は、制御の操作画面を、ローカルの Web 画面として出す(cgo なし)。
// 状態、設定、ログ、情報。net/http と埋め込みの HTML/CSS/JS だけで動く。
//
// 認証は既定で無い。既定の待ち受けは 127.0.0.1(同じ機械のブラウザからだけ)。
// 他の機械から使うときは、トークンを付ける(Options.Token)。
package webui

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"srtrx/internal/config"
	"srtrx/internal/controller"
	"srtrx/internal/logx"
)

//go:embed static
var staticFiles embed.FS

// DefaultAddr は既定の待ち受け(同じ機械からだけ)。
const DefaultAddr = "127.0.0.1:8088"

// Options は Web 画面の指定。
type Options struct {
	Addr  string // 待ち受け(host:port)。空なら DefaultAddr
	Token string // 空でなければ、この文字列を知っている人だけが使える(?token=… か Authorization: Bearer …)
}

// Server は Web 画面。
type Server struct {
	ctl  *controller.Controller
	logs *logx.Logs
	opt  Options
	srv  *http.Server
	ln   net.Listener
}

// New は Server を作る(まだ待ち受けない)。
func New(ctl *controller.Controller, logs *logx.Logs, opt Options) *Server {
	if opt.Addr == "" {
		opt.Addr = DefaultAddr
	}
	return &Server{ctl: ctl, logs: logs, opt: opt}
}

// Handler は全体のハンドラ(テスト用にも公開)。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(staticFiles, "static")
	mux.Handle("/", http.FileServer(http.FS(sub)))

	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/config", s.handleConfigGet)
	mux.HandleFunc("POST /api/config/check", s.handleConfigCheck)
	mux.HandleFunc("POST /api/config/live", s.handleConfigLive)
	mux.HandleFunc("POST /api/config/apply", s.handleConfigApply)
	mux.HandleFunc("POST /api/action", s.handleAction)
	mux.HandleFunc("POST /api/lock", s.handleLock)
	mux.HandleFunc("GET /api/profiles", s.handleProfilesList)
	mux.HandleFunc("POST /api/profiles/save", s.handleProfileSave)
	mux.HandleFunc("POST /api/profiles/load", s.handleProfileLoad)
	mux.HandleFunc("GET /api/logs", s.handleLogs)
	mux.HandleFunc("GET /api/info", s.handleInfo)
	return s.guard(mux)
}

// Start は待ち受けを始める。ポートが使えないときはエラー(映像の経路には影響させない。呼び出し側で記録する)。
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.opt.Addr)
	if err != nil {
		return err
	}
	s.ln = ln
	s.srv = &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		defer logx.Recover("webui.Serve")
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("Web 画面が止まった", "err", err)
		}
	}()
	return nil
}

// URL はブラウザで開く先(トークンがあれば、付けた形)。
func (s *Server) URL() string {
	host, port, _ := net.SplitHostPort(s.opt.Addr)
	if s.ln != nil {
		host, port, _ = net.SplitHostPort(s.ln.Addr().String())
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	u := "http://" + net.JoinHostPort(host, port) + "/"
	if s.opt.Token != "" {
		u += "?token=" + url.QueryEscape(s.opt.Token)
	}
	return u
}

// Shutdown は待ち受けを止める。
func (s *Server) Shutdown(ctx context.Context) {
	if s.srv != nil {
		_ = s.srv.Shutdown(ctx)
	}
}

// ---- 守り ----

func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return host == "localhost" || (ip != nil && ip.IsLoopback())
}

// guard は、(1) DNS リバインディング対策としての Host の検査、(2) トークン、
// (3) 書き込みの CSRF 対策(JSON と独自ヘッダ、Origin の一致)を行う。
func (s *Server) guard(next http.Handler) http.Handler {
	loop := isLoopbackAddr(s.opt.Addr)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// (1) 待ち受けがループバックのときは、Host もループバックの名前だけ受ける
		if loop {
			h, _, err := net.SplitHostPort(r.Host)
			if err != nil {
				h = r.Host
			}
			h = strings.Trim(h, "[]")
			if ip := net.ParseIP(h); !(h == "localhost" || (ip != nil && ip.IsLoopback())) {
				http.Error(w, "forbidden host", http.StatusForbidden)
				return
			}
		}
		// (2) トークン
		if s.opt.Token != "" && !s.tokenOK(w, r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// (3) 書き込み
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get("X-SRTRX") != "1" || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			if o := r.Header.Get("Origin"); o != "" {
				if u, err := url.Parse(o); err != nil || u.Host != r.Host {
					http.Error(w, "forbidden origin", http.StatusForbidden)
					return
				}
			}
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

const cookieName = "srtrx_token"

func (s *Server) tokenOK(w http.ResponseWriter, r *http.Request) bool {
	eq := func(a string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(s.opt.Token)) == 1 }
	if t := r.URL.Query().Get("token"); t != "" && eq(t) {
		// 以降は Cookie で通す(アドレスバーにトークンを残さない)
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: t, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		if r.Method == http.MethodGet && !strings.HasPrefix(r.URL.Path, "/api/") {
			http.Redirect(w, r, r.URL.Path, http.StatusFound)
			return true
		}
		return true
	}
	if c, err := r.Cookie(cookieName); err == nil && eq(c.Value) {
		return true
	}
	if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") && eq(strings.TrimPrefix(a, "Bearer ")) {
		return true
	}
	return false
}

// ---- 共通の入出力 ----

type apiError struct {
	Error  string            `json:"error"`
	Errors map[string]string `json:"errors,omitempty"` // キー → 理由(範囲外の欄)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "JSON を読めない: " + err.Error()})
		return false
	}
	return true
}

// failFrom は、設定の変更のエラーを、状態コードと本文にする。
func failFrom(w http.ResponseWriter, err error) {
	var ve config.ValidationError
	switch {
	case errors.Is(err, controller.ErrLocked):
		writeJSON(w, http.StatusLocked, apiError{Error: err.Error()})
	case errors.As(err, &ve):
		m := map[string]string{}
		for _, fe := range ve {
			if _, dup := m[fe.Field]; !dup {
				m[fe.Field] = fe.Msg
			}
		}
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: err.Error(), Errors: m})
	default:
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
	}
}

func (s *Server) String() string { return fmt.Sprintf("webui(%s)", s.opt.Addr) }
