// Package mediamtx は、同梱した公式 MediaMTX バイナリの書き出し、設定の生成、起動を担う。
package mediamtx

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"srtrx/internal/config"
	"srtrx/internal/proc"
)

//go:embed assets/mediamtx_linux_amd64
var binary []byte

//go:embed VERSION
var versionFile string

//go:embed SHA256
var sha256File string

//go:embed LICENSE
var License string

// Version は同梱した MediaMTX の版。
func Version() string { return strings.TrimSpace(versionFile) }

// ExpectedSHA256 は固定した SHA-256(16進)。
func ExpectedSHA256() string { return strings.TrimSpace(sha256File) }

// StopGrace は SIGTERM から SIGKILL までの猶予(ffmpeg と同じ)。
const StopGrace = time.Second

// PortInUse は「ポート使用中」と判定したときの理由。
const PortInUse = "ポート使用中(外部のMediaMTXが動いている可能性)"

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// VerifyEmbedded は埋め込んだバイナリが固定した SHA-256 と一致するか確かめる。
func VerifyEmbedded() error {
	if got := sum(binary); got != ExpectedSHA256() {
		return fmt.Errorf("同梱の MediaMTX の SHA-256 が一致しない: got %s want %s", got, ExpectedSHA256())
	}
	return nil
}

// Extract は binDir/mediamtx-{SHA-256の先頭8桁} へ書き出し(モード0700)、パスを返す。
// 同じ名前のファイルがあり SHA-256 が一致すれば、書き直さない。
func Extract(binDir string) (string, error) {
	if err := VerifyEmbedded(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		return "", err
	}
	want := ExpectedSHA256()
	dst := filepath.Join(binDir, "mediamtx-"+want[:8])
	if b, err := os.ReadFile(dst); err == nil && sum(b) == want {
		if st, err := os.Stat(dst); err == nil && st.Mode().Perm() == 0o700 {
			return dst, nil
		}
		if err := os.Chmod(dst, 0o700); err == nil {
			return dst, nil
		}
	}
	tmp, err := os.CreateTemp(binDir, "mediamtx-tmp*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(binary); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp.Name(), 0o700); err != nil {
		return "", err
	}
	// 実行中のバイナリへの上書きは ETXTBSY になるので、rename で入れ替える
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return "", err
	}
	return dst, nil
}

// GenConfig はアプリの設定から mediamtx.yml を生成する(手で編集させない)。
func GenConfig(c config.MediaMTX) string {
	var b strings.Builder
	fmt.Fprintf(&b, "logLevel: %s\n", c.LogLevel)
	b.WriteString("rtsp: no\nrtmp: no\nhls: no\nwebrtc: no\napi: no\n")
	b.WriteString("srt: yes\n")
	fmt.Fprintf(&b, "srtAddress: %q\n", c.SRTListen)
	b.WriteString("metrics: yes\n")
	fmt.Fprintf(&b, "metricsAddress: %q\n", c.MetricsListen)
	b.WriteString("paths:\n  all_others:\n")
	return b.String()
}

// WriteConfig は GenConfig の結果を path へ書く(rename で入れ替え)。
func WriteConfig(path string, c config.MediaMTX) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(GenConfig(c)), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Process は起動した MediaMTX。
type Process struct {
	*proc.Proc
	portInUse chan struct{}
}

// PortConflict は標準エラーに `already in use` が出ると閉じる。
func (p *Process) PortConflict() <-chan struct{} { return p.portInUse }

// Start は MediaMTX を起動する。cfgPath は WriteConfig で書いたファイル。
func Start(binPath, cfgPath, pidFile string, onLine func(stream, line string)) (*Process, error) {
	p := &Process{portInUse: make(chan struct{})}
	var once sync.Once
	pr, err := proc.Start(proc.Spec{
		Name:    "mediamtx",
		Path:    binPath,
		Args:    []string{cfgPath},
		PidFile: pidFile,
		// MediaMTX は作業ディレクトリへ自己署名の証明書(auto.crt/auto.key)を作る。設定ファイルの場所に閉じ込める。
		Dir: filepath.Dir(cfgPath),
		OnLine: func(stream, line string) {
			if strings.Contains(strings.ToLower(line), "already in use") {
				once.Do(func() { close(p.portInUse) })
			}
			if onLine != nil {
				onLine(stream, line)
			}
		},
	})
	if err != nil {
		return nil, err
	}
	p.Proc = pr
	return p, nil
}

// Ping は metricsURL が 200 を返すか。
func Ping(ctx context.Context, client *http.Client, metricsURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metricsURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return errors.New(resp.Status)
	}
	return nil
}
