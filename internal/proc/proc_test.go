package proc

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func sh(t *testing.T, script string, onLine func(string, string)) *Proc {
	t.Helper()
	p, err := Start(Spec{Name: "sh", Path: "/bin/sh", Args: []string{"-c", script}, OnLine: onLine})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Kill() })
	return p
}

func waitDone(t *testing.T, p *Proc, d time.Duration) {
	t.Helper()
	select {
	case <-p.Done():
	case <-time.After(d):
		t.Fatal("終了しない")
	}
}

func TestExitCodeAndSignal(t *testing.T) {
	p := sh(t, "exit 3", nil)
	waitDone(t, p, 3*time.Second)
	if p.Exit().Code != 3 {
		t.Errorf("exit: %+v", p.Exit())
	}
	p = sh(t, "kill -9 $$", nil)
	waitDone(t, p, 3*time.Second)
	if p.Exit().Signal != "killed" {
		t.Errorf("signal: %+v (%s)", p.Exit(), p.Exit())
	}
}

func TestStderrLinesAndTail(t *testing.T) {
	var mu sync.Mutex
	var got []string
	p := sh(t, "echo one >&2; echo two >&2; echo out", func(stream, line string) {
		mu.Lock()
		got = append(got, stream+":"+line)
		mu.Unlock()
	})
	waitDone(t, p, 3*time.Second)
	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(got, ",")
	for _, w := range []string{"stderr:one", "stderr:two", "stdout:out"} {
		if !strings.Contains(joined, w) {
			t.Errorf("%s が無い: %s", w, joined)
		}
	}
	if len(p.Tail(2)) != 2 {
		t.Errorf("Tail: %v", p.Tail(2))
	}
}

func TestLongLineTruncatedTo4KiB(t *testing.T) {
	var mu sync.Mutex
	var lens []int
	p := sh(t, `head -c 20000 /dev/zero | tr '\0' 'a' >&2; echo >&2; echo after >&2`, func(_, line string) {
		mu.Lock()
		lens = append(lens, len(line))
		mu.Unlock()
	})
	waitDone(t, p, 3*time.Second)
	mu.Lock()
	defer mu.Unlock()
	if len(lens) != 2 || lens[0] != MaxLine || lens[1] != 5 {
		t.Errorf("長い行は4KiBで切り、次の行は無事: %v", lens)
	}
}

func TestTailLimit(t *testing.T) {
	p := sh(t, `i=0; while [ $i -lt 700 ]; do echo l$i >&2; i=$((i+1)); done`, nil)
	waitDone(t, p, 5*time.Second)
	tail := p.Tail(10000)
	if len(tail) != TailLines || tail[len(tail)-1] != "l699" {
		t.Errorf("直近500行: len=%d last=%s", len(tail), tail[len(tail)-1])
	}
}

func TestStopTermThenKillGroup(t *testing.T) {
	// SIGTERM を無視する子(と孫)。グループごと SIGKILL されること。
	p := sh(t, `trap '' TERM; (trap '' TERM; sleep 60) & sleep 60; wait`, nil)
	time.Sleep(200 * time.Millisecond)
	start := time.Now()
	p.Stop(300 * time.Millisecond)
	if !p.Exited() {
		t.Fatal("Stop の後は終了している")
	}
	if d := time.Since(start); d < 250*time.Millisecond || d > 3*time.Second {
		t.Errorf("TERM を無視したら grace の後に KILL: %v", d)
	}
	time.Sleep(100 * time.Millisecond)
	// 孫も残っていない(刈り取り待ちのゾンビは数えない)
	if n := liveInGroup(p.PID()); n != 0 {
		t.Errorf("プロセスグループに生きた残りが %d 個ある", n)
	}
}

func TestStopWhileSIGSTOPped(t *testing.T) {
	p := sh(t, `sleep 60`, nil)
	time.Sleep(100 * time.Millisecond)
	syscall.Kill(p.PID(), syscall.SIGSTOP) // T5: ffmpeg が固まる
	start := time.Now()
	p.Stop(time.Second)
	if !p.Exited() {
		t.Fatal("SIGSTOP 中でも止められる")
	}
	if time.Since(start) > 2*time.Second {
		t.Error("遅すぎる")
	}
}

func TestStopGracefulIsFast(t *testing.T) {
	p := sh(t, `sleep 60`, nil)
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	p.Stop(time.Second)
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Errorf("SIGTERM で終わるなら待たない: %v", d)
	}
}

func TestIndependentProcessGroup(t *testing.T) {
	p := sh(t, `sleep 5`, nil)
	pg, err := syscall.Getpgid(p.PID())
	if err != nil {
		t.Fatal(err)
	}
	if pg != p.PID() || pg == syscall.Getpgrp() {
		t.Errorf("独立したプロセスグループ: pgid=%d pid=%d self=%d", pg, p.PID(), syscall.Getpgrp())
	}
	// stdin は /dev/null
	stdin, _ := os.Readlink(fmt.Sprintf("/proc/%d/fd/0", p.PID()))
	if stdin != os.DevNull {
		t.Errorf("stdin=%s", stdin)
	}
}

// 子を起こしたスレッドが畳まれても、子は生き残ること(Go の落とし穴)。
func TestChildSurvivesThreadChurn(t *testing.T) {
	p := sh(t, `sleep 30`, nil)
	for i := 0; i < 50; i++ {
		done := make(chan struct{})
		go func() {
			runtime.LockOSThread() // ロックしたまま goroutine を終えるとスレッドが終了する
			close(done)
		}()
		<-done
	}
	time.Sleep(300 * time.Millisecond)
	if p.Exited() {
		t.Fatalf("スレッドの入れ替わりで子が死んだ: %s", p.Exit())
	}
}

func TestPidFileWrittenAndRemoved(t *testing.T) {
	pf := filepath.Join(t.TempDir(), "x.pid")
	p, err := Start(Spec{Name: "sh", Path: "/bin/sh", Args: []string{"-c", "sleep 30"}, PidFile: pf})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(pf)
	if err != nil || strings.TrimSpace(string(b)) != strconv.Itoa(p.PID()) {
		t.Fatalf("pid ファイル: %q %v", b, err)
	}
	p.Stop(time.Second)
	waitDone(t, p, 2*time.Second)
	if _, err := os.Stat(pf); err == nil {
		t.Error("終了したら pid ファイルを消す")
	}
}

func TestCleanupStale(t *testing.T) {
	dir := t.TempDir()
	pf := filepath.Join(dir, "stale.pid")

	// 前回の制御が残した子(Pdeathsig 無しで起動して、孤児を模す)
	orphan := sh(t, `exec sleep 301`, nil)
	os.WriteFile(pf, []byte(strconv.Itoa(orphan.PID())+"\n"), 0o600)

	// 別物なら触らない
	if killed, _ := CleanupStale(pf, "not-this-name"); killed {
		t.Fatal("名前が違うプロセスは殺さない")
	}
	if orphan.Exited() {
		t.Fatal("殺された")
	}
	os.WriteFile(pf, []byte(strconv.Itoa(orphan.PID())+"\n"), 0o600)
	killed, err := CleanupStale(pf, "sleep")
	if err != nil || !killed {
		t.Fatalf("killed=%v err=%v", killed, err)
	}
	waitDone(t, orphan, 3*time.Second)
	if _, err := os.Stat(pf); err == nil {
		t.Error("pid ファイルを消す")
	}
	// ファイルが無ければ何もしない
	if killed, err := CleanupStale(pf, "sleep"); killed || err != nil {
		t.Errorf("%v %v", killed, err)
	}
	// 不正な中身
	os.WriteFile(pf, []byte("garbage"), 0o600)
	if _, err := CleanupStale(pf, "sleep"); err == nil {
		t.Error("不正な pid ファイルはエラー")
	}
}

func TestStartMissingBinary(t *testing.T) {
	if _, err := Start(Spec{Name: "x", Path: "/nonexistent/bin"}); err == nil {
		t.Error("起動できなければエラー")
	}
}

// liveInGroup はプロセスグループ pg に属する、ゾンビでないプロセスの数。
func liveInGroup(pg int) int {
	ents, _ := os.ReadDir("/proc")
	n := 0
	for _, e := range ents {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		s := string(b)
		i := strings.LastIndex(s, ")")
		if i < 0 {
			continue
		}
		f := strings.Fields(s[i+2:]) // state ppid pgrp ...
		if len(f) > 2 && f[0] != "Z" && f[2] == strconv.Itoa(pg) {
			n++
		}
	}
	return n
}
