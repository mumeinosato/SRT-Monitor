package metrics

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"srtrx/internal/logx"
)

// Poller は /metrics を一定間隔で取りに行き、Tracker へ流して OnStats を呼ぶ。
// 映像の経路とは独立している(失敗は OK=false になるだけ)。
type Poller struct {
	Tracker *Tracker
	OnStats func(Stats)

	mu       sync.Mutex
	url      string
	interval time.Duration
	timeout  time.Duration
	lastOK   time.Time
	wake     chan struct{}
	failing  bool
}

// NewPoller は設定値で作る。
func NewPoller(url string, interval, timeout time.Duration, onStats func(Stats)) *Poller {
	return &Poller{Tracker: NewTracker(), OnStats: onStats, url: url, interval: interval, timeout: timeout,
		wake: make(chan struct{}, 1)}
}

// SetConfig は設定変更を反映する(即時)。
func (p *Poller) SetConfig(url string, interval, timeout time.Duration) {
	p.mu.Lock()
	p.url, p.interval, p.timeout = url, interval, timeout
	p.mu.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// LastOK は最後に取得できた時刻。
func (p *Poller) LastOK() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastOK
}

// PollOnce は1回取得して Stats を返す(OnStats も呼ぶ)。
func (p *Poller) PollOnce(ctx context.Context) Stats {
	p.mu.Lock()
	url, timeout := p.url, p.timeout
	p.mu.Unlock()

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := &http.Client{Timeout: timeout}
	snap, err := Fetch(cctx, client, url)
	var st Stats
	if err != nil {
		st = p.Tracker.Fail()
		p.mu.Lock()
		first := !p.failing
		p.failing = true
		p.mu.Unlock()
		if first { // 失敗の連続でログを埋めない
			slog.Warn("統計の取得に失敗", "url", url, "err", err)
		}
	} else {
		st = p.Tracker.Update(snap)
		p.mu.Lock()
		p.lastOK = time.Now()
		was := p.failing
		p.failing = false
		p.mu.Unlock()
		if was {
			slog.Info("統計の取得が回復した", "url", url)
		}
	}
	if p.OnStats != nil {
		p.OnStats(st)
	}
	return st
}

// Run は ctx が終わるまで取得を続ける。
func (p *Poller) Run(ctx context.Context) {
	defer logx.Recover("metrics.Poller")
	for {
		p.PollOnce(ctx)
		p.mu.Lock()
		iv := p.interval
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-p.wake:
		case <-time.After(iv):
		}
	}
}
