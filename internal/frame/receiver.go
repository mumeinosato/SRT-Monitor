package frame

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"srtrx/internal/logx"
)

// Receiver はフレームソケット(必要ならFIFO)を待ち受け、読み切ったフレームを Store へ渡す。
// 接続は同時に1つだけで、新しい接続が来たら古い接続を閉じて置き換える。
// 接続が切れたとき、読みかけのフレームは捨てる。
type Receiver struct {
	store *Store

	mu   sync.Mutex
	conn net.Conn // 現在の接続
	gen  uint64   // 接続の世代

	nowFunc func() time.Time
}

// NewReceiver は Store へ書き込む Receiver を作る。
func NewReceiver(s *Store) *Receiver {
	return &Receiver{store: s, nowFunc: time.Now}
}

// ListenUnix は path のUnixソケットを待ち受ける(古いファイルは消し、モード0600)。
// ctx が終わるまで、あるいは Close まで動く。
func (r *Receiver) ListenUnix(ctx context.Context, path string) (net.Listener, error) {
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	go r.acceptLoop(ln)
	return ln, nil
}

func (r *Receiver) acceptLoop(ln net.Listener) {
	defer logx.Recover("frame.acceptLoop")
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			slog.Error("frame: accept に失敗", "err", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		r.mu.Lock()
		if r.conn != nil {
			r.conn.Close() // 古い接続を閉じて置き換える
		}
		r.conn = c
		r.gen++
		gen := r.gen
		r.mu.Unlock()
		slog.Info("frame: 書き手が接続した")
		go r.readLoop(c, gen)
	}
}

func (r *Receiver) readLoop(rd io.ReadCloser, gen uint64) {
	defer logx.Recover("frame.readLoop")
	defer func() {
		rd.Close()
		r.mu.Lock()
		if r.gen == gen {
			r.conn = nil
		}
		r.mu.Unlock()
		slog.Info("frame: 書き手が切断した")
	}()
	r.ReadFrames(rd)
}

// ReadFrames は rd から EOF/エラーまで、1フレームずつ読み切って Store へ渡す。
// 途中で切れたフレームは捨てる。テストやFIFOからも使う。
func (r *Receiver) ReadFrames(rd io.Reader) {
	for {
		size := r.store.FrameSize()
		buf := r.store.Buffer(size)
		if _, err := io.ReadFull(rd, buf); err != nil {
			// 半端なバイト列(ErrUnexpectedEOF)も、きれいなEOFも、ここで破棄する。
			if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, net.ErrClosed) {
				slog.Debug("frame: 読み取りエラー", "err", err)
			}
			return
		}
		r.store.Publish(buf, r.nowFunc())
	}
}

// RunFIFO は FIFO(名前付きパイプ)から読み続ける。ffmpeg が入れ替わっても読み手が残る。
// 書き手がいなくなる(EOF)たびに、読みかけのフレームを捨てて開き直す。
func (r *Receiver) RunFIFO(ctx context.Context, path string) error {
	_ = os.Remove(path)
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		return err
	}
	go func() {
		defer logx.Recover("frame.RunFIFO")
		for ctx.Err() == nil {
			// O_RDONLY の open は書き手が現れるまでブロックする。
			f, err := os.OpenFile(path, os.O_RDONLY, 0)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				slog.Error("frame: FIFO を開けない", "err", err)
				time.Sleep(200 * time.Millisecond)
				continue
			}
			slog.Info("frame: FIFO の書き手が接続した")
			stop := make(chan struct{})
			go func() {
				select {
				case <-ctx.Done():
					f.Close()
				case <-stop:
				}
			}()
			r.ReadFrames(f)
			close(stop)
			f.Close()
			slog.Info("frame: FIFO の書き手が切断した")
		}
	}()
	return nil
}

// Connected は書き手が接続中か(ソケット接続のみ)。
func (r *Receiver) Connected() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.conn != nil
}
