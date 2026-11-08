package ffmpeg

import (
	"time"

	"srtrx/internal/proc"
)

// StopGrace は SIGTERM から SIGKILL までの猶予。
const StopGrace = time.Second

// Start は組み立てた引数で ffmpeg を起動する(独立プロセスグループ、stderr は slog へ)。
func Start(o Options, pidFile string, onLine func(stream, line string)) (*proc.Proc, error) {
	return proc.Start(proc.Spec{
		Name:    "ffmpeg",
		Path:    o.Caps.Path,
		Args:    BuildArgs(o),
		PidFile: pidFile,
		OnLine:  onLine,
	})
}
