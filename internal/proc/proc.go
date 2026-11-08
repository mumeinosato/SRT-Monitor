// Package proc は子プロセス(ffmpeg、MediaMTX)を、独立したプロセスグループで起動・停止する。
package proc

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// MaxLine は標準エラー/出力1行の上限(超えた分は切り捨てる)。
const MaxLine = 4096

// TailLines はメモリに残す直近の行数。
const TailLines = 500

// Spec は起動の指定。
type Spec struct {
	Name    string // ログ用("ffmpeg" など)
	Path    string // 実行ファイル
	Args    []string
	Env     []string // nil なら親の環境
	Dir     string   // 作業ディレクトリ(空なら親と同じ)
	PidFile string   // 空でなければ、プロセスグループIDを書く
	// OnLine は stdout/stderr の1行ごとに呼ばれる(任意)。
	OnLine func(stream, line string)
}

// ExitInfo は終了の理由。
type ExitInfo struct {
	Code   int    // 終了コード。シグナル終了なら -1
	Signal string // シグナル名。通常終了なら空
	Err    error
}

func (e ExitInfo) String() string {
	switch {
	case e.Signal != "":
		return "シグナル " + e.Signal + " で終了"
	case e.Err != nil:
		return e.Err.Error()
	default:
		return fmt.Sprintf("終了コード %d", e.Code)
	}
}

// Proc は起動した1つの子プロセス。
type Proc struct {
	spec  Spec
	cmd   *exec.Cmd
	pgid  int
	done  chan struct{}
	exit  ExitInfo
	start time.Time

	mu   sync.Mutex
	tail []string
}

// ---- fork 専用スレッド ----
//
// Linux の Pdeathsig は「子を fork したスレッド」が終了したときに発火する。
// Go の goroutine は任意のスレッドで動き、スレッドは増減するので、普通に Start すると、
// そのスレッドが畳まれた瞬間に子が殺される。そこで、fork は専用のgoroutineに
// LockOSThread した上で、必ずそこから行う(プロセスの寿命と同じだけ生きる)。

type spawnReq struct {
	cmd  *exec.Cmd
	resp chan error
}

var (
	spawnOnce sync.Once
	spawnCh   chan spawnReq
)

func spawner() {
	spawnCh = make(chan spawnReq)
	go func() {
		runtime.LockOSThread() // 解除しない: このスレッドが生きている間は子が死なない
		for r := range spawnCh {
			r.resp <- r.cmd.Start()
		}
	}()
}

func startOnSpawnThread(cmd *exec.Cmd) error {
	spawnOnce.Do(spawner)
	resp := make(chan error, 1)
	spawnCh <- spawnReq{cmd, resp}
	return <-resp
}

// Start は子プロセスを起動する。
//   - Setpgid: 独立したプロセスグループ(停止はグループごと)
//   - Pdeathsig=SIGKILL: 制御が死んだら道連れ
//   - 標準入力は /dev/null
func Start(spec Spec) (*Proc, error) {
	cmd := exec.Command(spec.Path, spec.Args...)
	if spec.Env != nil {
		cmd.Env = spec.Env
	}
	cmd.Dir = spec.Dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		return nil, err
	}
	defer devnull.Close()
	cmd.Stdin = devnull
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	if err := startOnSpawnThread(cmd); err != nil {
		return nil, fmt.Errorf("%s を起動できない: %w", spec.Name, err)
	}
	p := &Proc{spec: spec, cmd: cmd, pgid: cmd.Process.Pid, done: make(chan struct{}), start: time.Now()}
	if spec.PidFile != "" {
		if err := os.WriteFile(spec.PidFile, []byte(strconv.Itoa(p.pgid)+"\n"), 0o600); err != nil {
			slog.Warn("pid ファイルを書けない", "file", spec.PidFile, "err", err)
		}
	}

	var rd sync.WaitGroup
	rd.Add(2)
	go func() { defer rd.Done(); p.readLines("stderr", stderr) }()
	go func() { defer rd.Done(); p.readLines("stdout", stdout) }()
	go func() {
		rd.Wait() // パイプを読み切ってから Wait(Wait はパイプを閉じるため)
		err := cmd.Wait()
		p.exit = exitInfo(err)
		if spec.PidFile != "" {
			removePidFileIf(spec.PidFile, p.pgid)
		}
		close(p.done)
	}()
	return p, nil
}

func exitInfo(err error) ExitInfo {
	if err == nil {
		return ExitInfo{Code: 0}
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return ExitInfo{Code: -1, Signal: ws.Signal().String()}
		}
		return ExitInfo{Code: ee.ExitCode()}
	}
	return ExitInfo{Code: -1, Err: err}
}

func removePidFileIf(path string, pgid int) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if n, _ := strconv.Atoi(strings.TrimSpace(string(b))); n == pgid {
		os.Remove(path)
	}
}

// readLines は1行ずつ読み、4KiBで切って、ログとリングバッファへ送る。
func (p *Proc) readLines(stream string, r io.Reader) {
	br := bufio.NewReaderSize(r, MaxLine)
	for {
		chunk, err := br.ReadSlice('\n')
		if len(chunk) > 0 {
			line := strings.TrimRight(string(chunk), "\r\n")
			if errors.Is(err, bufio.ErrBufferFull) {
				// 長すぎる行: 4KiBで切り、残りは行末まで読み捨てる
				for {
					_, e2 := br.ReadSlice('\n')
					if !errors.Is(e2, bufio.ErrBufferFull) {
						break
					}
				}
			}
			if line != "" {
				p.emit(stream, line)
			}
		}
		if err != nil && !errors.Is(err, bufio.ErrBufferFull) {
			return
		}
	}
}

func (p *Proc) emit(stream, line string) {
	p.mu.Lock()
	p.tail = append(p.tail, line)
	if len(p.tail) > TailLines {
		p.tail = p.tail[len(p.tail)-TailLines:]
	}
	p.mu.Unlock()
	slog.Info(p.spec.Name, "stream", stream, "line", line)
	if p.spec.OnLine != nil {
		p.spec.OnLine(stream, line)
	}
}

// Tail は直近 n 行(古い順)。
func (p *Proc) Tail(n int) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	t := p.tail
	if len(t) > n {
		t = t[len(t)-n:]
	}
	return append([]string(nil), t...)
}

// PID はプロセスグループID(= リーダーのPID)。
func (p *Proc) PID() int { return p.pgid }

// Started は起動時刻。
func (p *Proc) Started() time.Time { return p.start }

// Done は終了すると閉じる。
func (p *Proc) Done() <-chan struct{} { return p.done }

// Exited は終了済みか。
func (p *Proc) Exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// Exit は終了の理由。Done が閉じた後に呼ぶ。
func (p *Proc) Exit() ExitInfo { return p.exit }

// Stop は SIGTERM を送り、grace 待っても残っていれば、プロセスグループへ SIGKILL を送る。
// SIGSTOP されたプロセスも、SIGKILL で確実に終わる。終了を確認してから戻る。
func (p *Proc) Stop(grace time.Duration) {
	if p.Exited() {
		return
	}
	_ = syscall.Kill(-p.pgid, syscall.SIGTERM)
	// SIGSTOP 中は SIGTERM が配送されないので、続けて SIGCONT を送って届かせる。
	_ = syscall.Kill(-p.pgid, syscall.SIGCONT)
	select {
	case <-p.done:
		return
	case <-time.After(grace):
	}
	p.Kill()
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		slog.Error("SIGKILL しても終了しない", "name", p.spec.Name, "pgid", p.pgid)
	}
}

// Kill はプロセスグループへ SIGKILL を送る。
func (p *Proc) Kill() { _ = syscall.Kill(-p.pgid, syscall.SIGKILL) }

// CleanupStale は、前回の制御が残した子プロセスを pid ファイルから探して止める。
// pid が再利用された別のプロセスを殺さないよう、/proc の cmdline に wantName が含まれるか確かめる。
func CleanupStale(pidFile, wantName string) (killed bool, err error) {
	b, err := os.ReadFile(pidFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer os.Remove(pidFile)
	pgid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pgid <= 1 {
		return false, fmt.Errorf("pid ファイルが不正: %q", strings.TrimSpace(string(b)))
	}
	cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pgid))
	if err != nil {
		return false, nil // もういない
	}
	if !strings.Contains(string(cmdline), wantName) {
		slog.Warn("pid ファイルのプロセスが別物なので触らない", "pid", pgid, "cmdline", string(cmdline))
		return false, nil
	}
	slog.Warn("前回の子プロセスが残っていたので止める", "name", wantName, "pgid", pgid)
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	_ = syscall.Kill(-pgid, syscall.SIGCONT)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !alive(pgid) {
			return true, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	for i := 0; i < 100 && alive(pgid); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	return true, nil
}

func alive(pid int) bool {
	// ゾンビは「生きている」とみなさない
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	s := string(b)
	if i := strings.LastIndex(s, ")"); i >= 0 && i+2 < len(s) {
		return s[i+2] != 'Z'
	}
	return true
}
