package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// MinMajor は必要な ffmpeg の最低メジャー版(6.0 以上)。
const MinMajor = 6

var versionRe = regexp.MustCompile(`(?m)^ffmpeg version n?(\d+)\.(\d+)(?:\.(\d+))?([^\s]*)`)

// ParseVersion は `ffmpeg -version` の出力から版を読む。読めなければ major=-1。
func ParseVersion(out string) (text string, major, minor int) {
	m := versionRe.FindStringSubmatch(out)
	if m == nil {
		return "", -1, 0
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	text = m[1] + "." + m[2]
	if m[3] != "" {
		text += "." + m[3]
	}
	text += m[4]
	return text, major, minor
}

// ParseProtocols は `ffmpeg -protocols` の出力から、Input と Output のプロトコル集合を返す。
func ParseProtocols(out string) (in, outp map[string]bool) {
	in, outp = map[string]bool{}, map[string]bool{}
	var cur map[string]bool
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case t == "Input:":
			cur = in
		case t == "Output:":
			cur = outp
		case t == "" || strings.HasSuffix(t, ":"):
			// 見出し行など
		default:
			if cur != nil {
				cur[t] = true
			}
		}
	}
	return
}

// DiagError は自己診断の失敗。Reason は画面とログに出す人向けの理由。
type DiagError struct{ Reason string }

func (e *DiagError) Error() string { return e.Reason }

// Diagnose は ffmpeg を調べる。見つからない・6.0 未満・srt が無い、のどれかなら *DiagError。
// bin が空なら PATH から "ffmpeg" を探す。
func Diagnose(ctx context.Context, bin string) (Caps, error) {
	if bin == "" {
		bin = "ffmpeg"
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return Caps{}, &DiagError{fmt.Sprintf("ffmpeg が見つからない(%s)。PATH を確認すること", bin)}
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	vout, err := exec.CommandContext(cctx, path, "-hide_banner", "-version").CombinedOutput()
	if err != nil {
		return Caps{}, &DiagError{fmt.Sprintf("ffmpeg -version に失敗: %v", err)}
	}
	text, major, minor := ParseVersion("ffmpeg version " + trimPrefixVersion(string(vout)))
	caps := Caps{Path: path, VersionText: text, Major: major, Minor: minor}
	if major >= 0 && major < MinMajor {
		return caps, &DiagError{fmt.Sprintf("ffmpeg の版が古い(%s)。6.0 以上が必要", text)}
	}
	if major < 0 {
		caps.VersionText = firstLine(string(vout))
	}
	pout, err := exec.CommandContext(cctx, path, "-hide_banner", "-protocols").CombinedOutput()
	if err != nil {
		return caps, &DiagError{fmt.Sprintf("ffmpeg -protocols に失敗: %v", err)}
	}
	in, outp := ParseProtocols(string(pout))
	caps.HasSRT = in["srt"]
	caps.HasUnix = outp["unix"]
	if !caps.HasSRT {
		return caps, &DiagError{"この ffmpeg は libsrt が無効(-protocols に srt が無い)。srt 対応のビルドが必要"}
	}
	return caps, nil
}

// trimPrefixVersion は、先頭行が "ffmpeg version ..." のとき、その先頭の語を重複させないため落とす。
func trimPrefixVersion(s string) string {
	s = strings.TrimSpace(s)
	return strings.TrimPrefix(s, "ffmpeg version ")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// IsDiagError は err が自己診断の失敗か。
func IsDiagError(err error) bool {
	var d *DiagError
	return errors.As(err, &d)
}
