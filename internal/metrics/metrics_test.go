package metrics

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func readTestdata(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseRealPublishAndRead(t *testing.T) {
	snap, err := Parse(strings.NewReader(readTestdata(t, "publish_and_read.txt")))
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Conns) != 1 {
		t.Fatalf("publish 接続だけを数える(read は除く): %+v", snap.Conns)
	}
	c := snap.Conns[0]
	if c.ID == "" || c.RTT <= 0 || c.Drop != 0 || c.Loss != 0 {
		t.Errorf("%+v", c)
	}
	if len(snap.FrameErrors) != 1 {
		t.Errorf("frames_in_error: %+v", snap.FrameErrors)
	}
}

func TestParseRealNoPublisher(t *testing.T) {
	snap, err := Parse(strings.NewReader(readTestdata(t, "no_publisher.txt")))
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Conns) != 0 || len(snap.FrameErrors) != 0 {
		t.Errorf("ラベル無しのゼロ行は接続・パスではない: %+v", snap)
	}
}

func TestParseValuesFromText(t *testing.T) {
	text := `srt_conns{id="a",path="nc",remoteAddr="1.2.3.4:5",state="publish"} 1
srt_conns{id="r",path="nc",remoteAddr="1.2.3.4:6",state="read"} 1
srt_conns_packets_received_drop{id="a",path="nc",remoteAddr="x",state="publish"} 3
srt_conns_packets_received_loss{id="a",path="nc",remoteAddr="x",state="publish"} 12
srt_conns_packets_received_retrans{id="a",path="nc",remoteAddr="x",state="publish"} 11
srt_conns_ms_rtt{id="a",path="nc",remoteAddr="x",state="publish"} 1.03
srt_conns_packets_received_loss{id="r",path="nc",remoteAddr="x",state="read"} 999
paths_inbound_frames_in_error{name="nc",state="ready"} 5
`
	snap, err := Parse(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Conns) != 1 {
		t.Fatalf("%+v", snap.Conns)
	}
	c := snap.Conns[0]
	if c.ID != "a" || c.Drop != 3 || c.Loss != 12 || c.Retrans != 11 || c.RTT != 1.03 {
		t.Errorf("%+v", c)
	}
	for _, v := range snap.FrameErrors {
		if v != 5 {
			t.Errorf("frame errors: %v", v)
		}
	}
}

func TestParseGarbage(t *testing.T) {
	if _, err := Parse(strings.NewReader("this is {{ not metrics")); err == nil {
		t.Error("解析できなければエラー")
	}
}

func snap(conns ...Conn) *Snapshot {
	return &Snapshot{Conns: conns, FrameErrors: map[string]float64{}}
}

func TestTrackerAccumulatesAcrossReconnect(t *testing.T) {
	tr := NewTracker()
	// 起動時から既にあった接続 A(loss=100)。基準にする
	st := tr.Update(snap(Conn{ID: "A", Loss: 100, Retrans: 50, Drop: 1}))
	if st.Loss != 0 || st.Retrans != 0 || st.Drop != 0 {
		t.Fatalf("起動時の値は基準(累計0): %+v", st)
	}
	st = tr.Update(snap(Conn{ID: "A", Loss: 112, Retrans: 60, Drop: 1}))
	if st.Loss != 12 || st.Retrans != 10 || st.Drop != 0 {
		t.Fatalf("差分を足す: %+v", st)
	}
	// T12: 再接続で id が変わり、カウンターが0から始まる
	st = tr.Update(snap(Conn{ID: "B", Loss: 3, Retrans: 2, Drop: 4}))
	if st.Loss != 15 || st.Retrans != 12 || st.Drop != 4 {
		t.Fatalf("新しい id は 0 から数えて足す(0に戻らない): %+v", st)
	}
	st = tr.Update(snap(Conn{ID: "B", Loss: 5, Retrans: 2, Drop: 4}))
	if st.Loss != 17 {
		t.Fatalf("%+v", st)
	}
	// 接続が無い間も累計は保つ
	st = tr.Update(snap())
	if st.Connected || st.Loss != 17 || !st.OK {
		t.Fatalf("%+v", st)
	}
}

func TestTrackerResetKeepsBaseline(t *testing.T) {
	tr := NewTracker()
	tr.Update(snap(Conn{ID: "A", Loss: 10}))
	tr.Update(snap(Conn{ID: "A", Loss: 20}))
	st := tr.Reset()
	if st.Loss != 0 {
		t.Fatalf("リセットで 0: %+v", st)
	}
	st = tr.Update(snap(Conn{ID: "A", Loss: 20}))
	if st.Loss != 0 {
		t.Errorf("リセット直後に過去分が戻ってはいけない: %+v", st)
	}
	st = tr.Update(snap(Conn{ID: "A", Loss: 25}))
	if st.Loss != 5 {
		t.Errorf("リセット後の増分だけ: %+v", st)
	}
}

func TestTrackerFailKeepsTotals(t *testing.T) {
	tr := NewTracker()
	tr.Update(snap(Conn{ID: "A", Loss: 0}))
	tr.Update(snap(Conn{ID: "A", Loss: 7, RTT: 2}))
	st := tr.Fail()
	if st.OK || st.Loss != 7 {
		t.Fatalf("失敗: OK=false、累計は保持: %+v", st)
	}
	// 回復: 失敗の間に増えた分も取りこぼさない
	st = tr.Update(snap(Conn{ID: "A", Loss: 9, RTT: 2}))
	if !st.OK || st.Loss != 9 {
		t.Errorf("%+v", st)
	}
}

func TestTrackerFirstPollFailureThenConnection(t *testing.T) {
	tr := NewTracker()
	tr.Fail()
	st := tr.Update(snap(Conn{ID: "A", Loss: 40}))
	if st.Loss != 0 {
		t.Errorf("最初の取得成功が基準: %+v", st)
	}
}

func TestTrackerCounterGoingBackwardsIsReset(t *testing.T) {
	tr := NewTracker()
	tr.Update(snap(Conn{ID: "A", Loss: 100}))
	st := tr.Update(snap(Conn{ID: "A", Loss: 4})) // 同じ id で減った
	if st.Loss != 4 {
		t.Errorf("減ったら、いまの値を差分とする: %+v", st)
	}
}

func TestTrackerRTTAndConnected(t *testing.T) {
	tr := NewTracker()
	st := tr.Update(snap())
	if st.Connected || st.RTTMS != 0 {
		t.Errorf("%+v", st)
	}
	st = tr.Update(snap(Conn{ID: "A", RTT: 1.5}, Conn{ID: "B", RTT: 3.5}))
	if !st.Connected || st.RTTMS != 3.5 {
		t.Errorf("%+v", st)
	}
}

func TestTrackerFrameErrors(t *testing.T) {
	tr := NewTracker()
	mk := func(v float64) *Snapshot {
		return &Snapshot{FrameErrors: map[string]float64{"name=nc,state=ready,": v}}
	}
	tr.Update(mk(10)) // 基準
	if st := tr.Update(mk(13)); st.FrameErrors != 3 {
		t.Fatalf("%+v", st)
	}
	// パスが消え(送信切断)、また現れて 0 から始まる
	tr.Update(&Snapshot{FrameErrors: map[string]float64{}})
	if st := tr.Update(mk(2)); st.FrameErrors != 5 {
		t.Fatalf("再出現は 0 から数える: %+v", st)
	}
}

func TestPollerOKThenFailThenRecover(t *testing.T) {
	var mode atomic.Int32 // 0=OK 1=500 2=遅延
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode.Load() {
		case 1:
			http.Error(w, "boom", 500)
		case 2:
			time.Sleep(500 * time.Millisecond)
		default:
			fmt.Fprint(w, `srt_conns{id="a",path="nc",remoteAddr="x",state="publish"} 1
srt_conns_packets_received_loss{id="a",path="nc",remoteAddr="x",state="publish"} 3
`)
		}
	}))
	defer srv.Close()

	var last atomic.Value
	p := NewPoller(srv.URL, time.Second, 150*time.Millisecond, func(s Stats) { last.Store(s) })
	ctx := context.Background()

	if st := p.PollOnce(ctx); !st.OK || !st.Connected {
		t.Fatalf("OK: %+v", st)
	}
	if p.LastOK().IsZero() {
		t.Error("LastOK")
	}
	mode.Store(1)
	if st := p.PollOnce(ctx); st.OK {
		t.Errorf("HTTPエラーは OK=false: %+v", st)
	}
	mode.Store(2)
	start := time.Now()
	if st := p.PollOnce(ctx); st.OK || time.Since(start) > 400*time.Millisecond {
		t.Errorf("タイムアウトは OK=false(%v): %+v", time.Since(start), st)
	}
	mode.Store(0)
	if st := p.PollOnce(ctx); !st.OK {
		t.Errorf("回復: %+v", st)
	}
	if s := last.Load().(Stats); !s.OK {
		t.Error("OnStats が呼ばれる")
	}
}

func TestPollerUnreachable(t *testing.T) {
	p := NewPoller("http://127.0.0.1:1/metrics", time.Second, 200*time.Millisecond, nil)
	if st := p.PollOnce(context.Background()); st.OK {
		t.Error("到達しない先は OK=false")
	}
}

func TestPollerRunStopsAndSetConfigWakes(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	p := NewPoller(srv.URL, time.Hour, time.Second, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	time.Sleep(100 * time.Millisecond)
	if hits.Load() != 1 {
		t.Fatalf("起動直後に1回取る: %d", hits.Load())
	}
	p.SetConfig(srv.URL, time.Hour, time.Second) // 設定変更で即取り直す
	time.Sleep(100 * time.Millisecond)
	if hits.Load() != 2 {
		t.Errorf("設定変更で即時に取り直す: %d", hits.Load())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ctx で止まらない")
	}
}
