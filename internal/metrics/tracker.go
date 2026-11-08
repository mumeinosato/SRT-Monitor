package metrics

import (
	"math"
	"sync"
)

// Totals はリセットからの累計。
type Totals struct {
	Drop        int64
	Loss        int64
	Retrans     int64
	FrameErrors int64
}

// Stats はプレイヤーへ送る値(ipc.Stats の元)。
type Stats struct {
	OK        bool    // 直近の取得の成否
	Connected bool    // publish 接続があるか
	RTTMS     float64 // 接続があるときのRTT(複数なら最大)
	Totals
}

type connPrev struct{ drop, loss, retrans float64 }

// Tracker は、接続の id ごとに前回の値を覚えて差分を足していく(Prometheus の increase と同じ考え方)。
//
//   - 起動後の最初の取得成功時にあった接続は、その時点の値を基準にする(0から数えない)。
//   - それ以降に現れた接続(再接続で id が変わる)は、0から数える。
//   - 取得に失敗したときは累計を保持し、OK=false を返す。
//   - 値が前回より減ったら、カウンターのリセットとして、今の値を差分とする。
type Tracker struct {
	mu      sync.Mutex
	started bool
	conns   map[string]connPrev
	paths   map[string]float64
	drop    float64
	loss    float64
	retrans float64
	frameEr float64
	last    Stats
}

// NewTracker は空のトラッカー。
func NewTracker() *Tracker {
	return &Tracker{conns: map[string]connPrev{}, paths: map[string]float64{}}
}

func delta(cur, prev float64) float64 {
	if cur >= prev {
		return cur - prev
	}
	return cur
}

func (t *Tracker) totals() Totals {
	r := func(f float64) int64 { return int64(math.Round(f)) }
	return Totals{Drop: r(t.drop), Loss: r(t.loss), Retrans: r(t.retrans), FrameErrors: r(t.frameEr)}
}

// Update は取得に成功した Snapshot を取り込み、現在の Stats を返す。
func (t *Tracker) Update(s *Snapshot) Stats {
	t.mu.Lock()
	defer t.mu.Unlock()

	seen := map[string]connPrev{}
	var rtt float64
	for _, c := range s.Conns {
		cur := connPrev{c.Drop, c.Loss, c.Retrans}
		if prev, known := t.conns[c.ID]; known {
			t.drop += delta(cur.drop, prev.drop)
			t.loss += delta(cur.loss, prev.loss)
			t.retrans += delta(cur.retrans, prev.retrans)
		} else if t.started {
			// 起動後に現れた接続: 0 から数える
			t.drop += cur.drop
			t.loss += cur.loss
			t.retrans += cur.retrans
		} // else: 起動時にあった接続は、いまの値が基準
		seen[c.ID] = cur
		if c.RTT > rtt {
			rtt = c.RTT
		}
	}
	t.conns = seen // 消えた接続は忘れる

	paths := map[string]float64{}
	for k, v := range s.FrameErrors {
		if prev, known := t.paths[k]; known {
			t.frameEr += delta(v, prev)
		} else if t.started {
			t.frameEr += v
		}
		paths[k] = v
	}
	t.paths = paths
	t.started = true

	t.last = Stats{OK: true, Connected: len(s.Conns) > 0, RTTMS: rtt, Totals: t.totals()}
	return t.last
}

// Fail は取得に失敗したとき。累計は保持して OK=false を返す。
func (t *Tracker) Fail() Stats {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.last = Stats{OK: false, Totals: t.totals()}
	return t.last
}

// Reset は累計を0に戻す(基準の値は保つので、リセット直後に増えない)。
func (t *Tracker) Reset() Stats {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.drop, t.loss, t.retrans, t.frameEr = 0, 0, 0, 0
	t.last.Totals = Totals{}
	return t.last
}

// Last は直近の Stats。
func (t *Tracker) Last() Stats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.last
}
