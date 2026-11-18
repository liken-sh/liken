package main

// Every read of the card goes through cardGate, so that the operator's
// file never keeps DRM master from the compositor.
//
// The compositor needs DRM master on the card to show a frame. The
// kernel makes a file master when it opens a primary card node that has
// no master. A file that was never master can take it later only with
// SET_MASTER, which needs CAP_SYS_ADMIN, and the weston container drops
// every capability. So if the operator opens the card before the
// compositor does, the compositor never gets master, each of its atomic
// commits fails with EACCES, and the kernel console takes over the
// screen.
//
// The gate guards against that:
//
//  1. It opens the card only while the output watch has a connection to
//     the compositor. Weston opens the card before it listens on its
//     socket, so by then the compositor is already master.
//  2. Each open sends DROP_MASTER before any other ioctl, so a file that
//     became master gives it up at once. GETCONNECTOR with a mode count
//     of zero makes the kernel probe the connector again when the
//     caller is master, and the operator must never cause a probe.
//  3. When DROP_MASTER succeeds, the operator's file was master, so the
//     running compositor is not master. The gate reports the
//     compositor's pid, and restartMasterless restarts it.
//
// Weston 16.0.2 closes the card before it closes its client connections
// on exit (weston_compositor_destroy runs before wl_display_destroy in
// frontend/main.c). A read in that window becomes master and gives it
// up on the same open, before the next compositor starts. It also
// reports the exiting compositor, and restartMasterless skips it
// because the operator already requested that restart or the process has
// exited.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync/atomic"
	"syscall"

	"golang.org/x/sys/unix"
)

// DRM_IOCTL_DROP_MASTER, _IO('d', 0x1f) in <drm/drm.h>. The request
// takes no argument, so its number encodes no size.
const drmDropMaster = 0x641f

// errCompositorAbsent means the gate did not open the card, because the
// operator has no connection to the compositor.
var errCompositorAbsent = errors.New("the operator has no connection to a compositor")

type cardGate struct {
	path     string
	procRoot string
	// live reports whether the output watch has a connection to the
	// compositor. While it is nil the gate opens nothing.
	live func() bool
	// dropMaster is a field so that a test can simulate a file that
	// was master.
	dropMaster func(fd int) error
	// masterless sends restartMasterless the pid of a compositor that
	// has no DRM master. It buffers one pid, and a report that finds
	// the buffer full is dropped, because a second report about the
	// same compositor needs no second restart.
	masterless chan int
	// absent makes the gate log only the first read that finds no
	// connection, not every pass.
	absent atomic.Bool
}

func newCardGate(path, procRoot string) *cardGate {
	return &cardGate{
		path:     path,
		procRoot: procRoot,
		dropMaster: func(fd int) error {
			return drmIoctl(fd, drmDropMaster, nil)
		},
		masterless: make(chan int, 1),
	}
}

func (g *cardGate) currentModes() (map[string]string, error) {
	fd, err := g.open()
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	return readCurrentModes(fd)
}

func (g *cardGate) connectorModes() (map[string][]drmMode, error) {
	fd, err := g.open()
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	return readConnectorModes(fd)
}

// open returns a file descriptor for the card that is not DRM master.
// DROP_MASTER returns EACCES for a file that was never master, which is
// the normal case. After any error other than EACCES the file may still
// be master, so open closes it and fails.
//
// EACCES depends on the operator container not having CAP_SYS_ADMIN.
// With that capability the kernel skips its permission check and
// returns EINVAL for a file that was never master, so every read would
// fail.
func (g *cardGate) open() (int, error) {
	if g.live == nil || !g.live() {
		if !g.absent.Swap(true) {
			fmt.Printf("%s: the operator has no connection to a compositor, so it reads nothing from the card until it connects\n", g.path)
		}
		return -1, errCompositorAbsent
	}
	g.absent.Store(false)
	fd, err := unix.Open(g.path, unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("opening %s: %w", g.path, err)
	}
	switch err := g.dropMaster(fd); {
	case err == nil:
		g.reportMasterless()
	case errors.Is(err, unix.EACCES):
	default:
		_ = unix.Close(fd)
		return -1, fmt.Errorf("dropping DRM master on %s: %w", g.path, err)
	}
	return fd, nil
}

// reportMasterless does not block, because applyMode reads the card
// while it holds modeSwitches, and the restart waits for modeSwitches.
// When the pod has no compositor process, nothing is reported, and the
// next compositor to open the card becomes master.
func (g *cardGate) reportMasterless() {
	pids := compositorProcesses(g.procRoot)
	if len(pids) == 0 {
		return
	}
	select {
	case g.masterless <- pids[0]:
	default:
	}
}

// errNothingToRestart makes restart count no restart, and
// restartMasterless logs nothing for it.
var errNothingToRestart = errors.New("the reported compositor needs no restart")

// restartMasterless restarts each compositor that the gate reports,
// until ctx is done or the channel closes. It signals only the
// reported pid, so a late report cannot end a compositor that started
// after the report was sent.
func (p *draPlugin) restartMasterless(ctx context.Context, masterless <-chan int) {
	for {
		select {
		case <-ctx.Done():
			return
		case pid, ok := <-masterless:
			if !ok {
				return
			}
			err := p.restart("masterless", func() error { return p.endMasterless(pid) })
			switch {
			case errors.Is(err, errNothingToRestart):
			case err != nil:
				fmt.Fprintf(os.Stderr, "restarting the compositor with no DRM master on %s: %v\n", p.card, err)
			default:
				fmt.Printf("%s: the compositor at pid %d has no DRM master and cannot show a frame, so it restarts\n", p.card, pid)
			}
		}
	}
}

// endMasterless skips a pid that has already exited or that the
// operator has already requested to restart. It runs under modeSwitches,
// so no other restart runs between the check and the signal.
func (p *draPlugin) endMasterless(pid int) error {
	if p.restartRequests.exists(pid) || !slices.Contains(p.compositors(), pid) {
		return errNothingToRestart
	}
	err := p.endCompositor(pid, syscall.SIGTERM)
	if errors.Is(err, os.ErrProcessDone) {
		return errNothingToRestart
	}
	return err
}
