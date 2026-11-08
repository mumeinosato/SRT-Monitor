// Package ipc は制御↔プレイヤー間の JSON Lines 通信(制御ソケット)の型と送受信。
package ipc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"srtrx/internal/config"
)

// MaxLine は1行(1メッセージ)の上限。
const MaxLine = 64 * 1024

// ProtocolVersion は hello の version。
const ProtocolVersion = 1

// ErrTooLong は上限を超える行を受け取った(読み飛ばして続行できる)。
var ErrTooLong = errors.New("ipc: 1行が64KiBを超えた")

// ErrSendTooLong は上限を超えるメッセージを送ろうとした。
var ErrSendTooLong = errors.New("ipc: 送信メッセージが64KiBを超える")

// State 名。
const (
	StateStarting = "starting"
	StateRunning  = "running"
	StateBackoff  = "backoff"
	StateStopped  = "stopped"
)

// --- 制御→プレイヤー ---

type Config struct {
	Type    string         `json:"type"`
	Video   config.Video   `json:"video"`
	Display config.Display `json:"display"`
	Overlay config.Overlay `json:"overlay"`
}

type State struct {
	Type         string `json:"type"`
	State        string `json:"state"`
	HWFallback   bool   `json:"hw_fallback"`
	RestartCount int    `json:"restart_count"`
	SinceMS      int64  `json:"since_ms"`
	Detail       string `json:"detail"`
}

type Stats struct {
	Type        string  `json:"type"`
	OK          bool    `json:"ok"`
	Connected   bool    `json:"connected"`
	Drop        int64   `json:"drop"`
	Loss        int64   `json:"loss"`
	Retrans     int64   `json:"retrans"`
	FrameErrors int64   `json:"frame_errors"`
	RTTMS       float64 `json:"rtt_ms"`
}

type CountersReset struct {
	Type string `json:"type"`
}

// --- プレイヤー→制御 ---

type Hello struct {
	Type    string `json:"type"`
	Version int    `json:"version"`
	PID     int    `json:"pid"`
}

type Heartbeat struct {
	Type        string  `json:"type"`
	FPS         float64 `json:"fps"`
	FrameAgeMS  int64   `json:"frame_age_ms"` // まだ無ければ -1
	FramesTotal uint64  `json:"frames_total"`
}

// Unknown は未知の type。呼び出し側がログへ残して無視する。
type Unknown struct {
	Type string
	Raw  []byte
}

// コンストラクタ(type を確実に入れる)。

func NewConfig(c config.Config) Config {
	return Config{Type: "config", Video: c.Video, Display: c.Display, Overlay: c.Overlay}
}
func NewState(state string, hwFallback bool, restarts int, since time.Duration, detail string) State {
	return State{Type: "state", State: state, HWFallback: hwFallback, RestartCount: restarts,
		SinceMS: since.Milliseconds(), Detail: detail}
}
func NewStats() Stats                 { return Stats{Type: "stats"} }
func NewCountersReset() CountersReset { return CountersReset{Type: "counters_reset"} }
func NewHello(pid int) Hello          { return Hello{Type: "hello", Version: ProtocolVersion, PID: pid} }
func NewHeartbeat(fps float64, ageMS int64, total uint64) Heartbeat {
	return Heartbeat{Type: "heartbeat", FPS: fps, FrameAgeMS: ageMS, FramesTotal: total}
}

// Conn は JSON Lines の接続。Send は並行に呼んでよい。Recv は1つのgoroutineから。
type Conn struct {
	c  net.Conn
	r  *bufio.Reader
	wm sync.Mutex
}

// NewConn は net.Conn を包む。
func NewConn(c net.Conn) *Conn {
	return &Conn{c: c, r: bufio.NewReaderSize(c, 4096)}
}

func (c *Conn) Close() error { return c.c.Close() }

// Send は msg をJSON1行で送る。
func (c *Conn) Send(msg any) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if len(b)+1 > MaxLine {
		return ErrSendTooLong
	}
	b = append(b, '\n')
	c.wm.Lock()
	defer c.wm.Unlock()
	_ = c.c.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, err = c.c.Write(b)
	return err
}

// readLine は1行を読む。上限超過のときは行末まで読み捨てて ErrTooLong を返す。
func (c *Conn) readLine() ([]byte, error) {
	var line []byte
	tooLong := false
	for {
		chunk, err := c.r.ReadSlice('\n')
		if !tooLong {
			if len(line)+len(chunk) > MaxLine {
				tooLong = true
				line = nil
			} else {
				line = append(line, chunk...)
			}
		}
		switch {
		case err == nil:
			if tooLong {
				return nil, ErrTooLong
			}
			return bytes.TrimRight(line, "\r\n"), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		default:
			return nil, err
		}
	}
}

// Recv は次のメッセージを返す。型は Config/State/Stats/CountersReset/Hello/Heartbeat/Unknown。
// 壊れた行や上限超過の行は error(ErrTooLong など)として返すので、読み続けてよい。
// 接続が閉じたときは io.EOF などの終端エラー。
func (c *Conn) Recv() (any, error) {
	for {
		line, err := c.readLine()
		if err != nil {
			return nil, err
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		return Decode(line)
	}
}

// Decode は1行をメッセージへ。
func Decode(line []byte) (any, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &head); err != nil {
		return nil, &DecodeError{fmt.Errorf("JSONを読めない: %w", err)}
	}
	var out any
	switch head.Type {
	case "config":
		out = new(Config)
	case "state":
		out = new(State)
	case "stats":
		out = new(Stats)
	case "counters_reset":
		out = new(CountersReset)
	case "hello":
		out = new(Hello)
	case "heartbeat":
		out = new(Heartbeat)
	default:
		return Unknown{Type: head.Type, Raw: append([]byte(nil), line...)}, nil
	}
	if err := json.Unmarshal(line, out); err != nil {
		return nil, &DecodeError{fmt.Errorf("%s を読めない: %w", head.Type, err)}
	}
	switch v := out.(type) {
	case *Config:
		return *v, nil
	case *State:
		return *v, nil
	case *Stats:
		return *v, nil
	case *CountersReset:
		return *v, nil
	case *Hello:
		return *v, nil
	case *Heartbeat:
		return *v, nil
	}
	return nil, errors.New("unreachable")
}

// DecodeError は壊れた行(読み続けてよい)。
type DecodeError struct{ Err error }

func (e *DecodeError) Error() string { return "ipc: " + e.Err.Error() }
func (e *DecodeError) Unwrap() error { return e.Err }

// IsFatal は Recv のエラーが接続の終端か(読み続けられないか)。
func IsFatal(err error) bool {
	if err == nil || errors.Is(err, ErrTooLong) {
		return false
	}
	var de *DecodeError
	return !errors.As(err, &de)
}

// Server はプレイヤー側。制御ソケットを待ち受け、接続は同時に1つ(新しい接続が古いものを置き換える)。
type Server struct {
	ln   net.Listener
	mu   sync.Mutex
	cur  *Conn
	path string
}

// Listen は path で待ち受ける(古いソケットファイルは消す。モード0600)。
func Listen(path string) (*Server, error) {
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	return &Server{ln: ln, path: path}, nil
}

func (s *Server) Close() error {
	err := s.ln.Close()
	s.mu.Lock()
	if s.cur != nil {
		s.cur.Close()
	}
	s.mu.Unlock()
	_ = os.Remove(s.path)
	return err
}

// Accept は次の接続を返し、前の接続を閉じる。ln が閉じたらエラー。
func (s *Server) Accept() (*Conn, error) {
	nc, err := s.ln.Accept()
	if err != nil {
		return nil, err
	}
	c := NewConn(nc)
	s.mu.Lock()
	if s.cur != nil {
		s.cur.Close()
	}
	s.cur = c
	s.mu.Unlock()
	return c, nil
}

// Send は現在の接続へ送る(接続が無ければ何もしない)。
func (s *Server) Send(msg any) error {
	s.mu.Lock()
	c := s.cur
	s.mu.Unlock()
	if c == nil {
		return nil
	}
	return c.Send(msg)
}

// Dial は制御側。接続できるまで interval ごとに再試行する。ctxDone が閉じたら nil を返す。
func Dial(path string, interval time.Duration, done <-chan struct{}) *Conn {
	for {
		nc, err := net.DialTimeout("unix", path, time.Second)
		if err == nil {
			return NewConn(nc)
		}
		select {
		case <-done:
			return nil
		case <-time.After(interval):
		}
	}
}
