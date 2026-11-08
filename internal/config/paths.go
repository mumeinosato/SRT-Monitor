package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// Paths は実行時に使うファイルの場所。
type Paths struct {
	RuntimeDir  string // $XDG_RUNTIME_DIR/srtrx (0700)
	FrameSock   string
	FrameFIFO   string
	ControlSock string
	FFmpegPID   string
	MediaMTXPID string

	ConfigFile  string
	StateDir    string // $XDG_STATE_HOME/srtrx
	LogFile     string
	BinDir      string
	MediaMTXYML string
}

// DefaultPaths は環境変数から場所を決める。
func DefaultPaths() Paths {
	home, _ := os.UserHomeDir()
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	if runtime == "" {
		runtime = filepath.Join(os.TempDir(), fmt.Sprintf("srtrx-run-%d", os.Getuid()))
	}
	cfgHome := os.Getenv("XDG_CONFIG_HOME")
	if cfgHome == "" {
		cfgHome = filepath.Join(home, ".config")
	}
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		stateHome = filepath.Join(home, ".local", "state")
	}
	return PathsFrom(runtime, filepath.Join(cfgHome, "srtrx"), filepath.Join(stateHome, "srtrx"))
}

// PathsFrom は3つの基点から全パスを組み立てる(テスト用にも使う)。
func PathsFrom(runtimeBase, configDir, stateDir string) Paths {
	rd := filepath.Join(runtimeBase, "srtrx")
	return Paths{
		RuntimeDir:  rd,
		FrameSock:   filepath.Join(rd, "frame.sock"),
		FrameFIFO:   filepath.Join(rd, "frame.fifo"),
		ControlSock: filepath.Join(rd, "control.sock"),
		FFmpegPID:   filepath.Join(rd, "ffmpeg.pid"),
		MediaMTXPID: filepath.Join(rd, "mediamtx.pid"),
		ConfigFile:  filepath.Join(configDir, "config.toml"),
		StateDir:    stateDir,
		LogFile:     filepath.Join(stateDir, "srtrx.log"),
		BinDir:      filepath.Join(stateDir, "bin"),
		MediaMTXYML: filepath.Join(stateDir, "mediamtx.yml"),
	}
}

// EnsureDirs はソケットのディレクトリ(0700)と状態ディレクトリを作る。
func (p Paths) EnsureDirs() error {
	if err := os.MkdirAll(p.RuntimeDir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(p.RuntimeDir, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(p.BinDir, 0o700); err != nil {
		return err
	}
	return os.MkdirAll(filepath.Dir(p.ConfigFile), 0o755)
}
