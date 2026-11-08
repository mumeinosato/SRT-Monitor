// Package frame は生フレーム(rgb24)の受信と、最新の1枚の保持を担う。
package frame

import (
	"sync"
	"time"
)

// Store は受信したフレームを保持する。
//
// player_buffer_frames == 0: 到着した最新を即表示(古いものは捨てる)。
// player_buffer_frames  = N: N枚たまるまで保持し、fps の一定間隔で1枚ずつ表示する。
// 空になったら最後のフレームを保持し、再びN枚たまるまで待つ。
type Store struct {
	mu sync.Mutex

	w, h    int
	bufN    int
	period  time.Duration
	latest  []byte // 表示中(Take が返す)フレーム。まだ無ければ nil
	hasLast bool
	seq     uint64 // latest を更新するたびに増える
	total   uint64 // 受信完了したフレームの総数(ハートビート用)
	lastRx  time.Time

	queue   [][]byte // N>0 のときの待ち行列
	primed  bool
	nextDue time.Time

	free [][]byte
}

// NewStore は幅・高さ・バッファ枚数・fps で作る。
func NewStore(w, h, bufFrames, fps int) *Store {
	s := &Store{}
	s.Reset(w, h, bufFrames, fps)
	return s
}

// Reset は設定の変更を反映する。サイズが変わったときは保持中のフレームを捨てる。
func (s *Store) Reset(w, h, bufFrames, fps int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if fps < 1 {
		fps = 30
	}
	s.bufN = bufFrames
	s.period = time.Second / time.Duration(fps)
	if w != s.w || h != s.h {
		s.w, s.h = w, h
		s.latest, s.hasLast, s.queue, s.free = nil, false, nil, nil
		s.primed = false
		s.seq++
	}
	if bufFrames == 0 {
		// 待ち行列を畳む: 最新の1枚だけ残す
		if n := len(s.queue); n > 0 {
			s.free = append(s.free, s.latest)
			s.latest = s.queue[n-1]
			s.hasLast = true
			s.seq++
			s.queue = nil
		}
		s.primed = false
	}
}

// FrameSize は1フレームのバイト数(幅×高さ×3)。
func (s *Store) FrameSize() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w * s.h * 3
}

// Size は現在の幅と高さ。
func (s *Store) Size() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w, s.h
}

// Buffer は読み込み用のバッファを返す(再利用品か新規)。size は FrameSize と一致させる。
func (s *Store) Buffer(size int) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	for n := len(s.free); n > 0; n = len(s.free) {
		b := s.free[n-1]
		s.free = s.free[:n-1]
		if len(b) == size {
			return b
		}
	}
	return make([]byte, size)
}

// Publish は完成したフレームを渡す。buf の所有権は Store に移る。
// size が変わっていた(Reset された)フレームは捨てる。
func (s *Store) Publish(buf []byte, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(buf) != s.w*s.h*3 {
		return
	}
	s.total++
	s.lastRx = now
	if s.bufN == 0 {
		if s.latest != nil && len(s.free) < 3 {
			s.free = append(s.free, s.latest)
		}
		s.latest = buf
		s.hasLast = true
		s.seq++
		return
	}
	s.queue = append(s.queue, buf)
	if len(s.queue) > s.bufN {
		// 遅れすぎ: 最古を捨てる(遅延を溜めない)
		if len(s.free) < 3 {
			s.free = append(s.free, s.queue[0])
		}
		s.queue = s.queue[1:]
	}
	if !s.primed && len(s.queue) >= s.bufN {
		s.primed = true
		s.nextDue = now
	}
}

// Take は表示すべきフレームを dst へコピーして返す。
// 前回の seq と同じ(変化なし)なら changed=false。dst は FrameSize 以上の長さが要る。
func (s *Store) Take(dst []byte, lastSeq uint64, now time.Time) (seq uint64, changed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bufN > 0 && s.primed {
		if !now.Before(s.nextDue) && len(s.queue) > 0 {
			f := s.queue[0]
			s.queue = s.queue[1:]
			if s.latest != nil && len(s.free) < 3 {
				s.free = append(s.free, s.latest)
			}
			s.latest = f
			s.hasLast = true
			s.seq++
			s.nextDue = s.nextDue.Add(s.period)
			if now.Sub(s.nextDue) > s.period*4 { // 長く止まっていたら基準を取り直す
				s.nextDue = now.Add(s.period)
			}
			if len(s.queue) == 0 {
				s.primed = false // 空になった: 最後のフレームを保持し、再びN枚待つ
			}
		}
	}
	if !s.hasLast || s.seq == lastSeq {
		return s.seq, false
	}
	copy(dst, s.latest)
	return s.seq, true
}

// Total は受信完了したフレームの総数。
func (s *Store) Total() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.total
}

// LastReceived は最後にフレームを受信した時刻(まだ無ければゼロ)。
func (s *Store) LastReceived() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastRx
}

// HasFrame は表示できるフレームがあるか。
func (s *Store) HasFrame() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hasLast
}

// Queued は待ち行列の長さ(テスト・診断用)。
func (s *Store) Queued() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}
