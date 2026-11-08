// Package metrics は MediaMTX の /metrics を取得・解析し、リセットからの累計を数える。
package metrics

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

// 解析する指標名。
const (
	mSRTConns    = "srt_conns"
	mDrop        = "srt_conns_packets_received_drop"
	mLoss        = "srt_conns_packets_received_loss"
	mRetrans     = "srt_conns_packets_received_retrans"
	mRTT         = "srt_conns_ms_rtt"
	mFrameErrors = "paths_inbound_frames_in_error"
	statePublish = "publish"
	maxBodyBytes = 8 << 20
)

// Conn は publish 接続(OBS→MediaMTX)1本の、ある時点のカウンター。
type Conn struct {
	ID      string
	Drop    float64
	Loss    float64
	Retrans float64
	RTT     float64
}

// Snapshot は1回の取得結果から取り出した値。
type Snapshot struct {
	Conns       []Conn             // state="publish" の接続(id 順)
	FrameErrors map[string]float64 // paths_inbound_frames_in_error(ラベルの組をキーに)
}

// Parse は /metrics のテキストから Snapshot を取り出す。
// 接続が無いとき MediaMTX は `srt_conns 0` のようなラベル無しの行を出すが、それは接続としては数えない。
func Parse(r io.Reader) (*Snapshot, error) {
	parser := expfmt.NewTextParser(model.UTF8Validation)
	fams, err := parser.TextToMetricFamilies(io.LimitReader(r, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("/metrics を解析できない: %w", err)
	}
	conns := map[string]*Conn{}
	get := func(id string) *Conn {
		c := conns[id]
		if c == nil {
			c = &Conn{ID: id}
			conns[id] = c
		}
		return c
	}
	snap := &Snapshot{FrameErrors: map[string]float64{}}

	for name, fam := range fams {
		for _, m := range fam.GetMetric() {
			labels := map[string]string{}
			for _, lp := range m.GetLabel() {
				labels[lp.GetName()] = lp.GetValue()
			}
			v, ok := metricValue(m.GetGauge().GetValue(), m.GetCounter().GetValue(), m.GetUntyped().GetValue(),
				m.Gauge != nil, m.Counter != nil, m.Untyped != nil)
			if !ok {
				continue
			}
			switch name {
			case mSRTConns:
				if labels["state"] == statePublish && labels["id"] != "" && v > 0 {
					get(labels["id"])
				}
			case mDrop, mLoss, mRetrans, mRTT:
				if labels["state"] != statePublish || labels["id"] == "" {
					continue
				}
				c := get(labels["id"])
				switch name {
				case mDrop:
					c.Drop = v
				case mLoss:
					c.Loss = v
				case mRetrans:
					c.Retrans = v
				case mRTT:
					c.RTT = v
				}
			case mFrameErrors:
				if len(labels) == 0 {
					continue // ラベル無しの0は「パスが無い」の意味
				}
				snap.FrameErrors[labelKey(labels)] = v
			}
		}
	}
	for _, c := range conns {
		snap.Conns = append(snap.Conns, *c)
	}
	sort.Slice(snap.Conns, func(i, j int) bool { return snap.Conns[i].ID < snap.Conns[j].ID })
	return snap, nil
}

func metricValue(g, c, u float64, hasG, hasC, hasU bool) (float64, bool) {
	switch {
	case hasG:
		return g, true
	case hasC:
		return c, true
	case hasU:
		return u, true
	}
	return 0, false
}

func labelKey(l map[string]string) string {
	keys := make([]string, 0, len(l))
	for k := range l {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + "=" + l[k] + ",")
	}
	return b.String()
}

// Fetch は url を取得して解析する。
func Fetch(ctx context.Context, client *http.Client, url string) (*Snapshot, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return nil, fmt.Errorf("/metrics が %s を返した", resp.Status)
	}
	return Parse(resp.Body)
}
