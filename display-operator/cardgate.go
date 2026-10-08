package main

// The gate on every read of the card node, and the check for DRM
// master that each read makes.
//
// The compositor must hold DRM master on the card to show a frame.
// The kernel makes a file master when it opens a primary card node
// and no master exists. The compositor runs with no capability, so it
// can take master only at its own open. If the operator's file holds
// master when the compositor opens the card, the compositor never gets
// master, and every atomic commit it makes fails with EACCES. The
// kernel's console then takes the screen.
//
// So each read has three guards:
//
//  1. The card opens only while the output watch holds a connection
//     to a compositor. Weston opens the card before it listens on its
//     socket, so a compositor that accepts a connection has already
//     opened the card, and an open after that cannot take master from
//     it.
//  2. Each open drops master before any other ioctl, so a file that
//     became master gives it back at once. A master that sends
//     GETCONNECTOR with a mode count of zero makes the kernel probe the
//     connector again, and the operator must never do that.
//  3. A drop that succeeds means that no other file held master, so
//     the compositor that serves holds none. The gate reports the
//     compositor's pid, and the operator restarts that compositor.
//
// The gate runs no timer. The watch opens it, and every pass that
// reads the card runs the check.
//
// One window stays open. Weston 14.0.2 closes the card before it
// closes its client connections on its way out
// (weston_compositor_destroy runs before wl_display_destroy in
// frontend/main.c), so the watch stays live for a short time after
// the card has no master. A read in that time takes master, and the
// drop gives it back before a new compositor can open the card,
// because the new compositor's container starts only after the old
// one exits. The drop also reports the exiting compositor's pid. That
// report restarts nothing: the operator ended that compositor itself,
// so its pid is in the record that restartMasterless skips, and a pid
// that has exited answers os.ErrProcessDone.

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
// carries no argument, so its number carries no size.
const drmDropMaster = 0x641f

// errCompositorAbsent is the answer of a read while the operator
// holds no connection to a compositor. Each caller treats it as a
// failed read, which costs the fields that the read fills, and nothing
// opened the card.
var errCompositorAbsent = errors.New("the operator holds no connection to a compositor")

// cardGate opens the card for the two reads in currentmode.go.
type cardGate struct {
	path     string
	procRoot string
	// Live answers whether the output watch holds a connection to a
	// compositor. It is nil until the operator wires the watch, and a
	// nil seam answers no, so the gate opens nothing before then.
	live func() bool
	// DropMaster sends DROP_MASTER on one open file. It is a field so a
	// test can make an open report that it was master. Only a card
	// node with no master gives that answer.
	dropMaster func(fd int) error
	// Masterless carries the pid of a compositor that serves with no
	// DRM master to the one goroutine that restarts it. One slot is
	// enough: a second report about the same compositor asks for
	// nothing more.
	masterless chan int
	// Absent is set while reads find no connection to a compositor,
	// so the gate logs the start of that time once and not on every
	// pass.
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

// Open answers a file on the card that holds no DRM master.
//
// The kernel answers DROP_MASTER in one of three ways. Success means
// the file was master and is not now, so the read goes on and reports
// the compositor. EACCES means the file was never master, which is
// the normal answer. Any other error leaves the file possibly master,
// so the read closes it and reads nothing. The gate never sends
// SET_MASTER.
//
// The EACCES answer depends on the operator container having no
// CAP_SYS_ADMIN. With that capability, the kernel skips the
// permission check, and DROP_MASTER on a file that was never master
// answers EINVAL, so every read fails.
func (g *cardGate) open() (int, error) {
	if g.live == nil || !g.live() {
		if !g.absent.Swap(true) {
			fmt.Printf("%s: the operator holds no connection to a compositor, so it reads nothing from the card until it connects\n", g.path)
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

// ReportMasterless sends the compositor's pid to the restart. The
// send never waits, because applyMode reads the card while it holds
// the lock that the restart takes. A pod with no compositor process
// has nothing to restart, and the next compositor's open takes the
// master that this read gave back.
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

// errNothingToRestart is the answer of a masterless restart that
// finds no process to end. It counts nothing and logs nothing.
var errNothingToRestart = errors.New("the reported compositor needs no restart")

// restartMasterless restarts each compositor that the card gate
// reports with no DRM master, through the lock that every restart
// holds.
//
// It ends only the pid in the report, and only while that pid runs
// and this operator has not ended it before, for any reason. Weston
// 14.0.2 closes the card before it closes its client connections
// (weston_compositor_destroy runs before wl_display_destroy in
// frontend/main.c), so a compositor on its way out still holds a
// live connection after the card has no master. A read in that time
// reports the exiting compositor. The operator ended that compositor
// itself, for a heal, a hung compositor, a mode, or an earlier
// masterless report, so its restart order is still in place, and the
// restart skips it. Ending only the reported pid, and never every
// compositor process, keeps a report from reaching the compositor
// that starts next.
//
// The check and the signal both run under the lock, so no other
// restart runs between them.
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

// EndMasterless ends one reported compositor. The caller holds
// modeSwitches.
func (p *draPlugin) endMasterless(pid int) error {
	if p.orders.placed(pid) || !slices.Contains(p.compositors(), pid) {
		return errNothingToRestart
	}
	err := p.endCompositor(pid, syscall.SIGTERM)
	if errors.Is(err, os.ErrProcessDone) {
		return errNothingToRestart
	}
	return err
}
