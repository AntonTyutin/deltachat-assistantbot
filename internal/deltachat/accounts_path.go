package deltachat

import (
	"fmt"
	"io"
	"os"

	"github.com/mattn/go-isatty"
)

func validateAccountsDir(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("DeltaChat accounts directory does not exist: %s (mount a volume at /data or run setup-account)", path)
		}
		return fmt.Errorf("DeltaChat accounts directory %s: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("DeltaChat accounts path is not a directory: %s", path)
	}
	probe, err := os.MkdirTemp(path, ".write-check-*")
	if err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("cannot write to DeltaChat accounts directory %s: permission denied (mount /data with write access for the container user)", path)
		}
		return fmt.Errorf("cannot write to DeltaChat accounts directory %s: %w", path, err)
	}
	_ = os.RemoveAll(probe)
	return nil
}

func rpcServerStderr() io.Writer {
	if isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsTerminal(os.Stdout.Fd()) {
		return io.Discard
	}
	return os.Stderr
}
