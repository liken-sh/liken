package plugins

// Sync pulls each operator's CLI out of the registry and writes it
// into the plugin directory, replacing whatever was there. It is the
// step behind `kubectl liken plugins sync`, and it is idempotent:
// re-running installs the same versions over the same files.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Sync installs one CLI per operator, then prints the one line to add
// the plugin directory to PATH when it is not already there. arch is
// the workstation's architecture, so the pull takes the workstation's
// binary regardless of the node's.
func Sync(operators []Operator, arch, binDir, pathEnv string, out io.Writer) error {
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	for _, op := range operators {
		ref, err := cliImageRef(op.Image)
		if err != nil {
			return fmt.Errorf("%s: %w", op.Domain, err)
		}
		data, err := pullBinary(ref, arch)
		if err != nil {
			return fmt.Errorf("%s: %w", op.Domain, err)
		}
		if err := installBinary(binDir, op.Domain, data); err != nil {
			return err
		}
		fmt.Fprintf(out, "installed %s from %s\n", Name(op.Domain), ref)
	}
	if !onPath(pathEnv, binDir) {
		fmt.Fprintf(out, "export PATH=\"%s:$PATH\"\n", binDir)
	}
	return nil
}

// installBinary writes one CLI to its path with mode 0755, through a
// temporary file and a rename. The rename replaces the old binary in
// one step, so a re-run never leaves a half-written file where a
// working one was.
func installBinary(binDir, domain string, data []byte) error {
	target := filepath.Join(binDir, Name(domain))
	tmp, err := os.CreateTemp(binDir, Name(domain)+".*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, target); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

// onPath reports whether dir is already one of the directories in a
// PATH value.
func onPath(pathEnv, dir string) bool {
	for _, entry := range filepath.SplitList(pathEnv) {
		if entry == dir {
			return true
		}
	}
	return false
}
