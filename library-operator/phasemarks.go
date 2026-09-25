package main

// phasemarks.go is how the containers of one library Job learn that a phase
// has ended. Every phase is a regular container, and all of them start
// together, so a phase cannot wait on the kubelet for the phase before it.
// Each phase writes a mark on the Job's phases volume when it ends, and a
// phase that depends on it reads the mark. The volume is an emptyDir, so the
// marks are local files that every container of the pod shares, and a
// retried pod starts with none.
//
// A container that the kernel kills writes no mark. So each phase holds an
// exclusive flock on its own running file for its whole life, and the kernel
// releases the lock when the process ends, however it ends. A running file
// whose lock is free and that has no mark beside it is a phase that died.
//
// The wait is an inotify watch on the directory. A mark arrives by a rename,
// and a process that ends closes its running file, which the watch reports as
// a close after a write. So a phase wakes on both events and polls nothing.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// The environment of a phase container: the directory the phases volume is
// mounted at, and the phases this container waits for, separated by commas.
const (
	libraryPhasesVariable     = "LIBRARY_PHASES"
	libraryPhaseNeedsVariable = "LIBRARY_PHASE_NEEDS"
)

// The name of the phases volume, where the operator mounts it, and the
// directory under it that holds the .nfo locks. The locks are in a directory
// of their own, because the watch reads the top directory only, and a lock
// taken on every .nfo edit would wake every waiter for nothing.
const (
	phasesVolumeName = "phases"
	phasesMountPath  = "/phases"
	nfoLocksDir      = "nfo"
)

// The phase the scan container runs. A phase is named by its container, so
// kubectl lists the phases of a Job in its pod.
const scanPhase = scannerContainer

// The suffixes of the files one phase writes: the running file it locks, the
// mark of a phase that finished its work, and the mark of a phase that failed.
const (
	phaseRunningSuffix = ".running"
	phaseDoneSuffix    = ".done"
	phaseFailedSuffix  = ".failed"
)

// The phases volume of one pod, and the running file this container holds.
type phaseBoard struct {
	dir     string
	running *os.File
}

func newPhaseBoard(dir string) *phaseBoard {
	return &phaseBoard{dir: dir}
}

// The board a container reads from its environment, and nil where no phases
// volume is mounted.
func boardOf(dir string) *phaseBoard {
	if dir == "" {
		return nil
	}
	return newPhaseBoard(dir)
}

// Start takes the lock on this phase's running file and holds it until the
// process ends. The file is locked before it takes its name, so a waiter
// that finds the name always finds the lock taken.
func (b *phaseBoard) start(phase string) error {
	staged := filepath.Join(b.dir, "."+phase+phaseRunningSuffix)
	file, err := os.OpenFile(staged, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("creating the running file of %s: %w", phase, err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return fmt.Errorf("locking the running file of %s: %w", phase, err)
	}
	if err := os.Rename(staged, b.path(phase, phaseRunningSuffix)); err != nil {
		_ = file.Close()
		return fmt.Errorf("naming the running file of %s: %w", phase, err)
	}
	b.running = file
	return nil
}

// Finish writes this phase's mark: done with no failure, and failed with the
// failure's text, which the closing container copies into the runs row. The
// mark is written before the lock is released, so a waiter never reads a
// free lock with no mark for a phase that ended well.
func (b *phaseBoard) finish(phase string, failure error) error {
	suffix, body := phaseDoneSuffix, ""
	if failure != nil {
		suffix, body = phaseFailedSuffix, failure.Error()
	}
	staged := filepath.Join(b.dir, "."+phase+suffix)
	if err := os.WriteFile(staged, []byte(body), 0o644); err != nil {
		return fmt.Errorf("writing the mark of %s: %w", phase, err)
	}
	if err := os.Rename(staged, b.path(phase, suffix)); err != nil {
		return fmt.Errorf("naming the mark of %s: %w", phase, err)
	}
	if b.running != nil {
		err := b.running.Close()
		b.running = nil
		return err
	}
	return nil
}

func (b *phaseBoard) path(phase, suffix string) string {
	return filepath.Join(b.dir, phase+suffix)
}

// What one phase left: whether it has ended, and the failure it ended with,
// empty for a phase that finished its work.
type phaseEnd struct {
	ended   bool
	failure string
}

// Ended reads one phase's state. A phase that never started has no running
// file and has not ended. A phase whose process ended with no mark has
// ended with a failure, because nothing else releases its lock.
func (b *phaseBoard) ended(phase string) phaseEnd {
	if end, marked := b.mark(phase); marked {
		return end
	}
	running, err := os.Open(b.path(phase, phaseRunningSuffix))
	if err != nil {
		return phaseEnd{}
	}
	defer running.Close()
	if err := syscall.Flock(int(running.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		return phaseEnd{}
	}
	_ = syscall.Flock(int(running.Fd()), syscall.LOCK_UN)
	// The phase can write its mark and exit between the first read and the
	// lock, so the marks are read once more before the phase counts as dead.
	if end, marked := b.mark(phase); marked {
		return end
	}
	return phaseEnd{ended: true, failure: "the " + phase + " container ended and wrote no mark"}
}

// The mark one phase wrote, if it wrote one.
func (b *phaseBoard) mark(phase string) (phaseEnd, bool) {
	if _, err := os.Stat(b.path(phase, phaseDoneSuffix)); err == nil {
		return phaseEnd{ended: true}, true
	}
	failure, err := os.ReadFile(b.path(phase, phaseFailedSuffix))
	if err != nil {
		return phaseEnd{}, false
	}
	text := strings.TrimSpace(string(failure))
	if text == "" {
		text = "the " + phase + " phase failed"
	}
	return phaseEnd{ended: true, failure: text}, true
}

// The events the watch wakes on: a file created or renamed into the
// directory, and a file closed after a write, which is how the end of a
// process that holds a running file reaches the watch.
const phaseWatchEvents = syscall.IN_CREATE | syscall.IN_MOVED_TO | syscall.IN_CLOSE_WRITE

// Watch sends on the channel after every change to the directory, until the
// context ends. The channel holds one pending change and drops the rest,
// because a waiter reads every mark again when it wakes.
func (b *phaseBoard) watch(ctx context.Context) (<-chan struct{}, error) {
	fd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC | syscall.IN_NONBLOCK)
	if err != nil {
		return nil, fmt.Errorf("watching %s: %w", b.dir, err)
	}
	if _, err := syscall.InotifyAddWatch(fd, b.dir, phaseWatchEvents); err != nil {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("watching %s: %w", b.dir, err)
	}
	// The descriptor is non-blocking, so the file below reads through the
	// runtime's poller, and a close from the other goroutine ends the read.
	events := os.NewFile(uintptr(fd), "inotify")
	changed := make(chan struct{}, 1)
	go func() {
		<-ctx.Done()
		_ = events.Close()
	}()
	go func() {
		buffer := make([]byte, 4096)
		for {
			if _, err := events.Read(buffer); err != nil {
				return
			}
			markChanged(changed)
		}
	}()
	return changed, nil
}

// The phases one container waits for, from its environment.
func phaseNeeds(list string) []string {
	return commaNames(list)
}

// Whether every phase in the list has ended, and the failures of those that
// failed, in the list's order.
func (b *phaseBoard) allEnded(phases []string) (bool, []string) {
	all := true
	var failures []string
	for _, phase := range phases {
		end := b.ended(phase)
		if !end.ended {
			all = false
			continue
		}
		if end.failure != "" {
			failures = append(failures, end.failure)
		}
	}
	return all, failures
}

// The lock of one .nfo file. Three phases edit the .nfo file of a title, and
// each edit reads the file, changes its own elements, and writes it again
// with a rename. Two edits at once would lose one of them, so each takes an
// exclusive flock on a file named by a hash of the .nfo file's path. The lock
// file is on the phases volume, which is local to the node, because flock on
// a network volume does not reach the other containers.
func lockNFO(locks, nfoPath string) (func(), error) {
	if locks == "" {
		return func() {}, nil
	}
	directory := filepath.Join(locks, nfoLocksDir)
	if err := os.MkdirAll(directory, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf("creating the .nfo locks: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(directory, nfoLockName(nfoPath)), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening the lock of %s: %w", nfoPath, err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("locking %s: %w", nfoPath, err)
	}
	return func() { _ = file.Close() }, nil
}

// The lock file of one .nfo file: a hash of its path, because a path holds
// characters and a length that a file name does not accept.
func nfoLockName(nfoPath string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(nfoPath)))
	return hex.EncodeToString(sum[:16]) + ".lock"
}
