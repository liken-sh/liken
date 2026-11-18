package main

// init rotates the k3s and containerd logs at boot, and caps the size
// of k3s.log while k3s runs.
//
// At boot, before k3s starts, init renames the previous boot's logs to
// numbered generations: k3s.log becomes k3s.log.1, and so on. No
// process of the previous boot is running, so no process is left
// writing to a renamed file. That matters for containerd.log, which k3s
// opens each time it starts containerd. A rename while containerd ran
// would leave containerd writing to the renamed file. Rotating only at
// boot also makes each generation the log of one boot, so the log of a
// boot that crashed is kept.
//
// init writes k3s.log itself, from startK3s in supervisor.go, so it can
// cap that file. The logs are on clusterState, the filesystem that
// holds etcd's data, and a full filesystem corrupts the datastore.
// containerd.log has no cap because init does not write it. containerd
// writes much less than k3s, and this file accepts the risk that its
// log fills the disk.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

const (
	// logGenerations is how many rotated files of each log stay on disk.
	logGenerations = 3

	// k3sLogCap is the size at which k3s.log rotates. The k3s log can
	// use at most (logGenerations+1) times this on disk, plus the line
	// being written when it passes the cap.
	k3sLogCap = 64 << 20
)

// rotateBootLogs renames the previous boot's k3s and containerd logs
// to numbered generations. main calls it after storage is mounted, so
// the paths are on the persistent disk when the machine has one, and
// before k3s starts, so no process has the files open. On a machine
// with no persistent disk the paths are on the root tmpfs, and the
// reboot has already cleared them.
func rotateBootLogs() {
	if err := os.MkdirAll(likenLogDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "liken: creating %s: %v\n", likenLogDir, err)
	}
	rotateGenerations(k3sLog, logGenerations)
	rotateGenerations(containerdLog, logGenerations)
}

// rotateGenerations deletes the oldest generation of the log at path,
// renames each remaining generation to the next number, and renames the
// log itself to .1. A missing file is not reported, because a first
// boot has no logs and containerd's directory does not exist until k3s
// has run once. Any other error is printed to the console and does not
// stop the boot.
func rotateGenerations(path string, keep int) {
	if err := os.Remove(fmt.Sprintf("%s.%d", path, keep)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "liken: rotating %s: %v\n", path, err)
	}
	for n := keep - 1; n >= 1; n-- {
		shiftLog(fmt.Sprintf("%s.%d", path, n), fmt.Sprintf("%s.%d", path, n+1))
	}
	shiftLog(path, path+".1")
}

func shiftLog(from, to string) {
	if err := os.Rename(from, to); err != nil && !errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "liken: rotating %s: %v\n", from, err)
	}
}

// cappedLogFile is the writer for k3s.log. It appends, and once the
// file has passed limit, it rotates the generations at the next line
// boundary and opens a new file. The log relay that tails the file
// (logs/tail.go) depends on both parts of that. The relay sends a
// trailing fragment as a whole line, so a rotated file must end with a
// whole line. The relay reopens the path when its inode changes, so the
// rotation renames the file and does not truncate it.
//
// Write never returns an error. teeOutput puts this writer in an
// io.MultiWriter with the console, and io.MultiWriter stops at the
// first writer that fails, so an error such as a full disk would also
// stop the console copy. A machine has no shell, so the console is the
// last place to read k3s's output. After a failure the writer prints
// the reason once and drops later writes.
type cappedLogFile struct {
	path        string
	limit       int64
	f           *os.File
	size        int64
	atLineStart bool
	broken      bool
}

// openCappedLog opens the log at path for appending, creating it when
// it is missing. It does not truncate, because the supervisor reopens
// the same log each time it restarts k3s, and the log of one boot must
// include every run.
func openCappedLog(path string, limit int64) (*cappedLogFile, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return &cappedLogFile{
		path:        path,
		limit:       limit,
		f:           f,
		size:        st.Size(),
		atLineStart: true,
	}, nil
}

func (c *cappedLogFile) Write(p []byte) (int, error) {
	if c.broken {
		return len(p), nil
	}
	if c.size >= c.limit && c.atLineStart {
		c.rotate()
		if c.broken {
			return len(p), nil
		}
	}
	n, err := c.f.Write(p)
	c.size += int64(n)
	if n > 0 {
		c.atLineStart = p[n-1] == '\n'
	}
	if err != nil {
		c.fail(fmt.Sprintf("writing %s: %v", c.path, err))
	}
	return len(p), nil
}

func (c *cappedLogFile) rotate() {
	if err := c.f.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "liken: closing %s to rotate: %v\n", c.path, err)
	}
	rotateGenerations(c.path, logGenerations)
	f, err := os.OpenFile(c.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		c.fail(fmt.Sprintf("reopening %s after rotation: %v", c.path, err))
		return
	}
	c.f = f
	c.size = 0
	c.atLineStart = true
}

// fail prints the reason and stops writing the file.
func (c *cappedLogFile) fail(reason string) {
	fmt.Fprintf(os.Stderr, "liken: %s. k3s stops logging to the file, and its console output continues\n", reason)
	c.broken = true
	if c.f != nil {
		c.f.Close()
		c.f = nil
	}
}

func (c *cappedLogFile) Close() error {
	if c.f == nil {
		return nil
	}
	err := c.f.Close()
	c.f = nil
	return err
}
