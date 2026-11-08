package ipc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"srtrx/internal/config"
)

func pipePair(t *testing.T) (*Conn, *Conn) {
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() })
	return NewConn(a), NewConn(b)
}

func TestRoundTripAllTypes(t *testing.T) {
	tx, rx := pipePair(t)
	cfg := config.Default()
	msgs := []any{
		NewConfig(cfg),
		NewState(StateRunning, true, 2, 15230*time.Millisecond, "detail"),
		Stats{Type: "stats", OK: true, Connected: true, Drop: 1, Loss: 12, Retrans: 12, FrameErrors: 3, RTTMS: 1.03},
		NewCountersReset(),
		NewHello(42),
		NewHeartbeat(29.97, 33, 123456),
	}
	go func() {
		for _, m := range msgs {
			tx.Send(m)
		}
	}()
	for i, want := range msgs {
		got, err := rx.Recv()
		if err != nil {
			t.Fatalf("%d: %v", i, err)
		}
		switch w := want.(type) {
		case Config:
			g := got.(Config)
			if g.Video.Width != w.Video.Width || len(g.Overlay.Tiles) != len(w.Overlay.Tiles) {
				t.Errorf("config: %+v", g)
			}
		default:
			if !equalJSON(t, got, want) {
				t.Errorf("%d: want %+v got %+v", i, want, got)
			}
		}
	}
}

func equalJSON(t *testing.T, a, b any) bool {
	t.Helper()
	return strings.TrimSpace(mustJSON(a)) == strings.TrimSpace(mustJSON(b))
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func typeName(v any) string { return fmt.Sprintf("%T", v) }

func TestSpecExamples(t *testing.T) {
	cases := []struct {
		line string
		typ  string
	}{
		{`{"type":"state","state":"running","hw_fallback":false,"restart_count":2,"since_ms":15230,"detail":""}`, "ipc.State"},
		{`{"type":"stats","ok":true,"connected":true,"drop":0,"loss":12,"retrans":12,"frame_errors":0,"rtt_ms":1.03}`, "ipc.Stats"},
		{`{"type":"heartbeat","fps":29.97,"frame_age_ms":33,"frames_total":123456}`, "ipc.Heartbeat"},
	}
	for _, c := range cases {
		got, err := Decode([]byte(c.line))
		if err != nil {
			t.Fatalf("%s: %v", c.line, err)
		}
		if typeName(got) != c.typ {
			t.Errorf("%s: type %s", c.line, typeName(got))
		}
	}
	h, _ := Decode([]byte(cases[2].line))
	if hb := h.(Heartbeat); hb.FPS != 29.97 || hb.FrameAgeMS != 33 || hb.FramesTotal != 123456 {
		t.Errorf("%+v", hb)
	}
}

func TestUnknownTypeIsIgnoredNotFatal(t *testing.T) {
	tx, rx := pipePair(t)
	go func() {
		tx.Send(map[string]any{"type": "future_thing", "x": 1})
		tx.Send(NewHello(1))
	}()
	got, err := rx.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if u, ok := got.(Unknown); !ok || u.Type != "future_thing" {
		t.Fatalf("Unknown を期待: %#v", got)
	}
	got, _ = rx.Recv()
	if _, ok := got.(Hello); !ok {
		t.Errorf("次のメッセージを読める: %#v", got)
	}
}

func TestBrokenJSONIsNotFatal(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	rx := NewConn(b)
	go func() {
		a.Write([]byte("{not json\n"))
		NewConn(a).Send(NewHello(7))
	}()
	_, err := rx.Recv()
	if err == nil || IsFatal(err) {
		t.Fatalf("壊れた行は致命的でないエラー: %v", err)
	}
	got, err := rx.Recv()
	if err != nil || got.(Hello).PID != 7 {
		t.Errorf("続けて読める: %v %v", got, err)
	}
}

func TestOversizeLineDiscardedAndRecovers(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	rx := NewConn(b)
	go func() {
		big := bytes.Repeat([]byte("x"), MaxLine+5000)
		a.Write([]byte(`{"type":"state","detail":"`))
		a.Write(big)
		a.Write([]byte("\"}\n"))
		NewConn(a).Send(NewHello(9))
	}()
	_, err := rx.Recv()
	if !errors.Is(err, ErrTooLong) || IsFatal(err) {
		t.Fatalf("ErrTooLong を期待: %v", err)
	}
	got, err := rx.Recv()
	if err != nil || got.(Hello).PID != 9 {
		t.Errorf("上限超過の後も次の行を読める: %v %v", got, err)
	}
}

func TestExactlyMaxLineAccepted(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	rx := NewConn(b)
	prefix := `{"type":"state","detail":"`
	suffix := `"}`
	pad := strings.Repeat("y", MaxLine-1-len(prefix)-len(suffix)) // 改行込みでちょうど MaxLine
	line := prefix + pad + suffix + "\n"
	if len(line) != MaxLine {
		t.Fatalf("len=%d", len(line))
	}
	go a.Write([]byte(line))
	got, err := rx.Recv()
	if err != nil {
		t.Fatalf("上限ちょうどは受け付ける: %v", err)
	}
	if got.(State).Detail != pad {
		t.Error("内容が欠けた")
	}
}

func TestSendTooLong(t *testing.T) {
	tx, _ := pipePair(t)
	err := tx.Send(State{Type: "state", Detail: strings.Repeat("z", MaxLine)})
	if !errors.Is(err, ErrSendTooLong) {
		t.Errorf("ErrSendTooLong を期待: %v", err)
	}
}

func TestEOFIsFatal(t *testing.T) {
	a, b := net.Pipe()
	rx := NewConn(b)
	a.Close()
	_, err := rx.Recv()
	if !IsFatal(err) {
		t.Errorf("EOF は致命的: %v", err)
	}
}

func TestServerReplacesOldConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.sock")
	srv, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	accepted := make(chan *Conn, 2)
	go func() {
		for {
			c, err := srv.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()
	done := make(chan struct{})
	defer close(done)
	c1 := Dial(path, 10*time.Millisecond, done)
	<-accepted
	c2 := Dial(path, 10*time.Millisecond, done)
	<-accepted

	c1.c.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := c1.Recv(); !IsFatal(err) {
		t.Errorf("古い接続は閉じられる: %v", err)
	}
	if err := srv.Send(NewHello(5)); err != nil {
		t.Fatal(err)
	}
	got, err := c2.Recv()
	if err != nil || got.(Hello).PID != 5 {
		t.Errorf("新しい接続へ届く: %v %v", got, err)
	}
}

func TestDialRetriesUntilServerAppears(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.sock")
	res := make(chan *Conn, 1)
	done := make(chan struct{})
	go func() { res <- Dial(path, 20*time.Millisecond, done) }()
	time.Sleep(80 * time.Millisecond)
	srv, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	select {
	case c := <-res:
		if c == nil {
			t.Fatal("接続できなかった")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("再接続しない")
	}
}

func TestDialStopsOnDone(t *testing.T) {
	done := make(chan struct{})
	res := make(chan *Conn, 1)
	go func() { res <- Dial(filepath.Join(t.TempDir(), "none.sock"), 10*time.Millisecond, done) }()
	close(done)
	select {
	case c := <-res:
		if c != nil {
			t.Error("nil を期待")
		}
	case <-time.After(time.Second):
		t.Fatal("done で止まらない")
	}
}
