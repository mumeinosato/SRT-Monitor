// Package ffmpeg は ffmpeg の引数の組み立て、自己診断、起動を担う。
//
// 単位の変換(GUIはミリ秒、ffmpegのSRT URLの latency/timeout はマイクロ秒、
// connect_timeout だけミリ秒)は、このパッケージの BuildURL の1か所に集める。
package ffmpeg

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"srtrx/internal/config"
)

// Caps は自己診断で分かった ffmpeg の能力。
type Caps struct {
	Path         string
	VersionText  string // "6.1.1-3ubuntu5" など
	Major, Minor int    // 版が読めなかったとき(gitビルド等)は Major = -1
	HasSRT       bool   // Input プロトコルに srt がある
	HasUnix      bool   // Output プロトコルに unix がある
}

// VersionKnown は版を数字として読めたか。
func (c Caps) VersionKnown() bool { return c.Major >= 0 }

// NeedsVsync0 は `-fps_mode` が使えない版(5.1 より古い)か。
func (c Caps) NeedsVsync0() bool {
	if !c.VersionKnown() {
		return false
	}
	return c.Major < 5 || (c.Major == 5 && c.Minor < 1)
}

// Options は引数組み立ての入力。
type Options struct {
	Config config.Config // Effective() 済みのもの
	Caps   Caps
	// HWAccel は実際に使うハードウェアデコード(自動フォールバック中は "none")。
	HWAccel string
	// Output は出力先(unix ソケットのパス、または FIFO のパス)。
	// Caps.HasUnix が偽のときは FIFO として書く。
	Output string
}

// BuildURL は入力のSRT URLを組み立てる。
//
//	srt://{host}:{port}?streamid=read:{path}(callerのみ)&mode=..&latency=(ms×1000)
//	&timeout=(ms×1000)&connect_timeout=(ms)&tlpktdrop=1&recv_buffer_size=N
func BuildURL(in config.Input) string {
	q := []string{}
	if in.Mode == "caller" {
		q = append(q, "streamid=read:"+in.Path)
	}
	q = append(q,
		"mode="+in.Mode,
		"latency="+strconv.Itoa(in.LatencyMS*1000),
		"timeout="+strconv.Itoa(in.TimeoutMS*1000),
		"connect_timeout="+strconv.Itoa(in.ConnectTimeoutMS), // ここだけミリ秒
	)
	if in.TLPktDrop {
		q = append(q, "tlpktdrop=1")
	}
	if in.RecvBufferBytes > 0 {
		q = append(q, "recv_buffer_size="+strconv.Itoa(in.RecvBufferBytes))
	}
	return "srt://" + net.JoinHostPort(in.Host, strconv.Itoa(in.Port)) + "?" + strings.Join(q, "&")
}

// VideoFilter は -vf の中身。
func VideoFilter(hwaccel string, w, h int) string {
	switch hwaccel {
	case "vaapi", "qsv":
		return fmt.Sprintf("hwdownload,format=nv12,scale=%d:%d,format=rgb24", w, h)
	default:
		return fmt.Sprintf("scale=%d:%d,format=rgb24", w, h)
	}
}

// BuildArgs は ffmpeg の引数(実行ファイル名を除く)を返す。
func BuildArgs(o Options) []string {
	c := o.Config
	a := []string{
		"-hide_banner", "-loglevel", "warning",
		"-fflags", "nobuffer", "-flags", "low_delay",
		"-probesize", strconv.Itoa(c.Input.ProbeSizeBytes), "-analyzeduration", strconv.Itoa(c.Input.AnalyzeDurationMS * 1000), // µs
	}
	switch o.HWAccel {
	case "vaapi":
		a = append(a, "-hwaccel", "vaapi", "-hwaccel_device", "/dev/dri/renderD128", "-hwaccel_output_format", "vaapi")
	case "qsv":
		a = append(a, "-hwaccel", "qsv", "-hwaccel_output_format", "qsv")
	}
	a = append(a, "-i", BuildURL(c.Input))
	// 映像を明示して選ぶ。自動の選択は、入力解析で寸法が分からなかったストリーム(キーフレームの前から繋いだ)を
	// 選ばず、ffmpeg が `Output file does not contain any stream` で終了してしまう。明示すれば、
	// デコーダが次のキーフレームを待って映像を出す。MPEG-TS では解析は常に analyzeduration まで続くので、
	// 解析の上限を大きくしてキーフレームを待たせる方法は、起動のたびに遅れる。
	a = append(a, "-map", "0:v:0")
	a = append(a, "-vf", VideoFilter(o.HWAccel, c.Video.Width, c.Video.Height))
	a = append(a, c.Video.ExtraArgs...) // 出力の直前
	a = append(a, "-an", "-sn", "-dn")
	if o.Caps.NeedsVsync0() {
		a = append(a, "-vsync", "0")
	} else {
		a = append(a, "-fps_mode", "passthrough")
	}
	a = append(a, "-f", "rawvideo", OutputURL(o.Caps, o.Output))
	return a
}

// OutputURL は出力先の指定。unix が使えれば unix://、無ければFIFOのパスをそのまま使う。
func OutputURL(caps Caps, output string) string {
	if caps.HasUnix {
		return "unix://" + output
	}
	return output
}
