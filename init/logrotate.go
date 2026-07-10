package main

// Log rotation, in two forms, both owned by init because init is the
// only program in a position to do them safely.
//
// The first form is rotate-at-boot: before k3s starts, the previous
// boot's k3s and containerd logs are shifted aside (k3s.log becomes
// k3s.log.1, and so on, keeping a few generations). Boot is the one
// moment nothing holds either file open, every process from the
// previous boot being gone, so a rename can't strand a writer. That
// constraint is containerd's whole story: k3s reopens containerd.log
// each time it starts containerd, but init doesn't own that file's
// descriptor, so renaming it mid-run would leave containerd writing
// to the renamed generation forever while the fresh path sat empty.
// Rotating only at boot also gives the files a useful shape: each
// generation is one boot, a small journald-style boot index, and a
// boot that died leaves its log on disk for reading after the fact.
//
// The second form is the in-boot size cap on k3s.log, which init can
// do because it owns that file's writer (supervisor.go tees k3s's
// output through it). These logs live on clusterState, the same
// filesystem as etcd's data, and a filesystem that fills up corrupts
// more than logs; a bounded worst case (the cap times the kept
// generations) is the difference between a chatty k3s and a machine
// that eats its own datastore. containerd.log has no equivalent cap,
// because init doesn't hold its descriptor; its volume is a small
// fraction of k3s's, and the residual risk is accepted and recorded
// here.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

const (
	// logGenerations is how many previous boots (or overflowed caps)
	// each log keeps on disk.
	logGenerations = 3

	// k3sLogCap bounds one boot's k3s.log. With rotation, the worst
	// case on disk per family is logGenerations+1 times this.
	k3sLogCap = 64 << 20
)

// rotateBootLogs shifts the previous boot's logs aside. It runs in
// the k3s branch of main, after storage has settled (so clusterState
// is mounted and these paths land on the persistent disk when the
// machine has one) and before k3s starts (so nothing holds the
// files). On a memory-backed machine the same paths sit on the root
// tmpfs and rotation works identically; there is just nothing left
// to rotate after a reboot.
func rotateBootLogs() {
	if err := os.MkdirAll(likenLogDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "liken: creating %s: %v\n", likenLogDir, err)
	}
	rotateGenerations(k3sLog, logGenerations)
	rotateGenerations(containerdLog, logGenerations)
}

// rotateGenerations does the numbered shift: the oldest generation
// is deleted, each survivor moves down one, and the live file
// becomes .1. Missing files are normal (a first boot has no logs at
// all, and containerd's directory doesn't exist until k3s has run
// once), so absence is silent; any other failure is worth a line but
// never worth stopping a boot over.
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

// cappedLogFile is the k3s.log writer: append-only, and when a
// boot's log passes the cap, it shifts the generations and reopens a
// fresh file. Two details are load-bearing for the log relay tailing
// this file. The rotation happens only at a line boundary, so the
// renamed generation always ends with a complete line (the tailer
// ships a trailing fragment as a whole line, which would garble a
// line split mid-write). And rotation is by rename, which is exactly
// the identity change the tailer's inode check watches for.
//
// Write never returns an error. This writer sits inside the
// io.MultiWriter carrying k3s's output to the console, and
// io.MultiWriter stops at the first failed writer: an error here
// (say, a full disk) would silence the console echo too, on a
// machine whose console is the last resort for reading anything. So
// a failure is reported once and file logging goes quiet while the
// console copy keeps flowing.
type cappedLogFile struct {
	path        string
	limit       int64
	f           *os.File
	size        int64
	atLineStart bool
	broken      bool
}

// openCappedLog opens (or continues) the log at path. Appending to
// an existing file matters within a boot: k3s restarts under the
// supervisor's backoff reopen this same log, and each restart must
// extend the boot's record, not truncate it.
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

// fail reports the failure once and stops file logging; the console
// copy of k3s's output is unaffected.
func (c *cappedLogFile) fail(reason string) {
	fmt.Fprintf(os.Stderr, "liken: %s; k3s file logging stops here (console continues)\n", reason)
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
