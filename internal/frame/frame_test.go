package frame

import (
	"bytes"
	"context"
	"io"
	"net"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

const tw, th = 4, 2
const fsz = tw * th * 3

func mk(v byte) []byte { return bytes.Repeat([]byte{v}, fsz) }

func TestPartialFrameDiscarded(t *testing.T) {
	s := NewStore(tw, th, 0, 30)
	r := NewReceiver(s)
	var in bytes.Buffer
	in.Write(mk(1))
	in.Write(mk(2)[:fsz-5]) // ffmpeg が途中で落ちた半端なバイト列
	r.ReadFrames(&in)
	if s.Total() != 1 {
		t.Fatalf("完成したフレームだけ数える: %d", s.Total())
	}
	dst := make([]byte, fsz)
	_, ok := s.Take(dst, 0, time.Now())
	if !ok || dst[0] != 1 {
		t.Errorf("表示は最後の完全なフレーム(1): ok=%v dst[0]=%d", ok, dst[0])
	}
}

func TestOnlyLatestKept(t *testing.T) {
	s := NewStore(tw, th, 0, 30)
	r := NewReceiver(s)
	var in bytes.Buffer
	for i := byte(1); i <= 10; i++ {
		in.Write(mk(i))
	}
	r.ReadFrames(&in)
	if s.Total() != 10 {
		t.Fatalf("total=%d", s.Total())
	}
	dst := make([]byte, fsz)
	seq, ok := s.Take(dst, 0, time.Now())
	if !ok || dst[0] != 10 {
		t.Errorf("最新(10)だけ見える: %d", dst[0])
	}
	if _, ok := s.Take(dst, seq, time.Now()); ok {
		t.Error("同じフレームでは再描画しない")
	}
}

func TestTakeBeforeAnyFrame(t *testing.T) {
	s := NewStore(tw, th, 0, 30)
	if _, ok := s.Take(make([]byte, fsz), 0, time.Now()); ok || s.HasFrame() {
		t.Error("フレームが無ければ changed=false")
	}
	if !s.LastReceived().IsZero() {
		t.Error("まだ無ければゼロ時刻")
	}
}

func TestSocketReplacesConnectionAndKeepsLastFrame(t *testing.T) {
	s := NewStore(tw, th, 0, 30)
	r := NewReceiver(s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	path := filepath.Join(t.TempDir(), "frame.sock")
	if _, err := r.ListenUnix(ctx, path); err != nil {
		t.Fatal(err)
	}

	c1, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	c1.Write(mk(7))
	waitFor(t, func() bool { return s.Total() == 1 })

	c2, err := net.Dial("unix", path) // 新しい書き手が古い接続を置き換える
	if err != nil {
		t.Fatal(err)
	}
	c1.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := c1.Read(make([]byte, 1)); err == nil {
		t.Error("古い接続は閉じられる")
	}
	c2.Write(mk(8))
	waitFor(t, func() bool { return s.Total() == 2 })

	// 書き手が半端に書いて落ちる → 最後の完全なフレームが残る
	c2.Write(mk(9)[:10])
	c2.Close()
	time.Sleep(100 * time.Millisecond)
	dst := make([]byte, fsz)
	if _, ok := s.Take(dst, 0, time.Now()); !ok || dst[0] != 8 {
		t.Errorf("切断後も最後のフレーム(8)を保持: %d", dst[0])
	}
	if s.Total() != 2 {
		t.Errorf("半端なフレームは数えない: %d", s.Total())
	}

	// 受け口は残っていて、次のffmpegが繋がる
	c3, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("受け口が残っていない: %v", err)
	}
	c3.Write(mk(10))
	waitFor(t, func() bool { return s.Total() == 3 })
	c3.Close()
}

func TestSocketPermissions(t *testing.T) {
	r := NewReceiver(NewStore(tw, th, 0, 30))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	path := filepath.Join(t.TempDir(), "f.sock")
	r.ListenUnix(ctx, path)
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		t.Fatal(err)
	}
	if st.Mode&0o777 != 0o600 {
		t.Errorf("ソケットは0600: %o", st.Mode&0o777)
	}
}

func TestFIFO(t *testing.T) {
	s := NewStore(tw, th, 0, 30)
	r := NewReceiver(s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	path := filepath.Join(t.TempDir(), "frame.fifo")
	if err := r.RunFIFO(ctx, path); err != nil {
		t.Fatal(err)
	}
	// 1人目の書き手: 1枚+半端
	w1, err := openFIFOWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	w1.Write(mk(5))
	w1.Write(mk(6)[:7])
	w1.Close()
	waitFor(t, func() bool { return s.Total() == 1 })
	time.Sleep(50 * time.Millisecond)
	// 2人目: 半端が混ざらず、フレーム境界が合っている
	w2, err := openFIFOWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	w2.Write(mk(11))
	waitFor(t, func() bool { return s.Total() == 2 })
	w2.Close()
	dst := make([]byte, fsz)
	s.Take(dst, 0, time.Now())
	if !bytes.Equal(dst, mk(11)) {
		t.Errorf("境界がずれた: %v", dst)
	}
}

func openFIFOWriter(path string) (io.WriteCloser, error) {
	var err error
	for i := 0; i < 50; i++ {
		var f io.WriteCloser
		f, err = openWrite(path)
		if err == nil {
			return f, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil, err
}

func TestBufferedModePacesAtFPS(t *testing.T) {
	s := NewStore(tw, th, 3, 10) // 100ms間隔
	t0 := time.Unix(1000, 0)
	dst := make([]byte, fsz)

	s.Publish(mk(1), t0)
	s.Publish(mk(2), t0)
	if _, ok := s.Take(dst, 0, t0); ok {
		t.Fatal("N枚たまるまで表示しない")
	}
	s.Publish(mk(3), t0)
	seq, ok := s.Take(dst, 0, t0)
	if !ok || dst[0] != 1 {
		t.Fatalf("N枚たまったら先頭から: %d", dst[0])
	}
	if _, ok := s.Take(dst, seq, t0.Add(50*time.Millisecond)); ok {
		t.Error("間隔(100ms)が来るまで次へ進まない")
	}
	seq, ok = s.Take(dst, seq, t0.Add(100*time.Millisecond))
	if !ok || dst[0] != 2 {
		t.Errorf("100ms後に2枚目: %d", dst[0])
	}
	seq, ok = s.Take(dst, seq, t0.Add(200*time.Millisecond))
	if !ok || dst[0] != 3 {
		t.Errorf("200ms後に3枚目: %d", dst[0])
	}
	// 空になった: 最後のフレームを保持
	if _, ok := s.Take(dst, seq, t0.Add(300*time.Millisecond)); ok {
		t.Error("空になったら変化なし(最後のフレームを保持)")
	}
	if !s.HasFrame() {
		t.Error("最後のフレームは保持")
	}
	// 再びN枚たまるまで待つ
	s.Publish(mk(4), t0.Add(400*time.Millisecond))
	if _, ok := s.Take(dst, seq, t0.Add(400*time.Millisecond)); ok {
		t.Error("再びN枚たまるまで表示しない")
	}
}

func TestBufferedOverflowDropsOldest(t *testing.T) {
	s := NewStore(tw, th, 2, 30)
	t0 := time.Unix(1000, 0)
	for i := byte(1); i <= 5; i++ {
		s.Publish(mk(i), t0)
	}
	if s.Queued() != 2 {
		t.Fatalf("待ち行列は N 枚まで: %d", s.Queued())
	}
	dst := make([]byte, fsz)
	s.Take(dst, 0, t0)
	if dst[0] != 4 {
		t.Errorf("古いものを捨てて 4,5 が残る: %d", dst[0])
	}
}

func TestResetSizeDropsFrames(t *testing.T) {
	s := NewStore(tw, th, 0, 30)
	s.Publish(mk(1), time.Now())
	s.Reset(6, 4, 0, 30)
	if s.HasFrame() {
		t.Error("サイズ変更でバッファを確保し直す")
	}
	if s.FrameSize() != 6*4*3 {
		t.Error("フレームサイズが更新される")
	}
	before := s.Total()          // frames_total は単調増加(制御のフレーム判定に使う)
	s.Publish(mk(2), time.Now()) // 旧サイズは捨てる
	if s.Total() != before {
		t.Error("旧サイズのフレームは捨てる")
	}
}

func TestResetToZeroBufferCollapsesQueue(t *testing.T) {
	s := NewStore(tw, th, 3, 30)
	t0 := time.Unix(1, 0)
	s.Publish(mk(1), t0)
	s.Publish(mk(2), t0)
	s.Reset(tw, th, 0, 30)
	dst := make([]byte, fsz)
	if _, ok := s.Take(dst, 0, t0); !ok || dst[0] != 2 {
		t.Errorf("最新優先へ戻したら最新が出る: %d", dst[0])
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("条件が満たされない")
}
