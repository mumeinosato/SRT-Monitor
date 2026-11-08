// Package logx は slog(JSON)のセットアップ。ファイルのローテーションと、
// GUIの「ログ」へ出すためのメモリのリングバッファを持つ。
package logx

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"

	"gopkg.in/natefinch/lumberjack.v2"
)

// Ring は直近の行を保持する io.Writer。1回の Write を1行として扱う。
type Ring struct {
	mu    sync.Mutex
	lines []string
	start int
	n     int
	cap   int
	ver   uint64
}

// NewRing は最大 cap 行を保持する。
func NewRing(cap int) *Ring { return &Ring{lines: make([]string, cap), cap: cap} }

func (r *Ring) Write(p []byte) (int, error) {
	for _, l := range bytes.Split(bytes.TrimRight(p, "\n"), []byte("\n")) {
		if len(l) == 0 {
			continue
		}
		r.Add(string(l))
	}
	return len(p), nil
}

// Add は1行を足す。
func (r *Ring) Add(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ver++
	if r.n < r.cap {
		r.lines[(r.start+r.n)%r.cap] = line
		r.n++
		return
	}
	r.lines[r.start] = line
	r.start = (r.start + 1) % r.cap
}

// Lines は古い順の行を返す。
func (r *Ring) Lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, r.n)
	for i := 0; i < r.n; i++ {
		out = append(out, r.lines[(r.start+i)%r.cap])
	}
	return out
}

// Tail は新しい側の最大 n 行を古い順に返す。
func (r *Ring) Tail(n int) []string {
	l := r.Lines()
	if len(l) > n {
		l = l[len(l)-n:]
	}
	return l
}

// Version は行が増えるたびに増える(GUIの更新判定用)。
func (r *Ring) Version() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ver
}

// Logs は Setup の結果。
type Logs struct {
	Ring   *Ring
	closer io.Closer
}

// Close はログファイルを閉じる。
func (l *Logs) Close() {
	if l != nil && l.closer != nil {
		l.closer.Close()
	}
}

// Setup は slog の既定ロガーをJSONで設定する。
// 出力先は stderr、path(10MB×5世代のローテーション。空ならファイル無し)、メモリのリング。
func Setup(path string, ringLines int, level slog.Level) *Logs {
	ring := NewRing(ringLines)
	ws := []io.Writer{os.Stderr, ring}
	var closer io.Closer
	if path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
			lj := &lumberjack.Logger{Filename: path, MaxSize: 10, MaxBackups: 5}
			ws = append(ws, lj)
			closer = lj
		}
	}
	h := slog.NewJSONHandler(io.MultiWriter(ws...), &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(h))
	return &Logs{Ring: ring, closer: closer}
}

// Recover は goroutine の入口で `defer logx.Recover("名前")` と使う。
// panic のスタックを残してプロセスを終了する(再起動は systemd に任せる)。
func Recover(where string) {
	if v := recover(); v != nil {
		slog.Error("panic", "where", where, "value", v, "stack", string(debug.Stack()))
		os.Exit(2)
	}
}
