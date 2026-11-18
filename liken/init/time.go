package main

// Init keeps the machine's clock correct. Cheap oscillators gain or
// lose seconds each day, and Kubernetes depends on the time being
// right: TLS certificates have notBefore and notAfter instants, leases
// have renew deadlines, and etcd orders events by time. A machine whose
// clock is far enough behind cannot join a cluster, because every
// certificate from the cluster's CA looks like it was issued in the
// future. Nothing in the cluster can correct the clock of a machine
// that cannot join, so init corrects it before it starts k3s.
//
// The protocol is SNTP, the stateless subset of NTP. A client sends a
// 48-byte request and receives a 48-byte reply. It records when it
// sent the request (t1) and when the reply arrived (t4). The server
// records when the request arrived (t2) and when the reply left (t3).
// The value t2-t1 is the outbound trip time plus the clock error, and
// t3-t4 is the clock error minus the return trip time. When the path
// is symmetric, the average of the two is the clock error alone.
//
// github.com/beevik/ntp does this calculation and the protocol's
// checks: leap-second flags, kiss-of-death codes, and stratum bounds.
// This file decides which source to query, when to step the clock, and
// how fast to slew it.
//
// Sources come from the Cluster and the Machine manifests, not from
// discovery. Leaders query the upstreams in the Cluster's
// spec.time.upstreams. Followers query the leaders, and fall back to
// the host of the cluster endpoint. A leader serves its clock to them
// over NTP (responder.go). In a cluster with no upstreams, the machines
// agree with each other but are right only when the leaders' hardware
// clocks are right, and status reports FreeRunning.
//
// A correction is either a step or a slew. At boot, before k3s
// starts, init steps the clock to the measured time with
// clock_settime, because nothing is running yet that a jump could
// disturb. After that, init only slews with adjtimex: the kernel runs
// the clock slightly fast or slow until the error is gone, so the time
// never jumps or runs backward under lease renewals and container logs.

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/beevik/ntp"
	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

// timePollInterval is how often the discipline loop measures the clock.
// machine/clock.go gives the reason for the value.
const timePollInterval = machine.ClockPollInterval

// stepThreshold is the offset below which a boot slews instead of
// stepping the clock. ntpd draws the same line, at 128ms.
const stepThreshold = 128 * time.Millisecond

// The stratum that status reports for a machine that has no time
// source (free-running) and for one that has sources but has not yet
// reached any (unsynchronized). A stratum is a source's distance from
// a reference clock: 1 is attached to one, and each hop adds 1. 10 is
// the conventional value for a deliberately local clock, and 16 means
// unsynchronized.
const (
	stratumFreeRunning    = 10
	stratumUnsynchronized = 16
)

// timeSync records one successful measurement. A measurement that
// stepped the clock at boot is marked, because its offset is the
// error from before the step.
type timeSync struct {
	source  string
	stratum int
	offset  time.Duration
	delay   time.Duration
	at      time.Time
	stepped bool
}

// clock holds the machine's timekeeping state. The discipline loop
// records each measurement in it, and on a leader the responder reads
// the last measurement to decide what to advertise. Both run as
// goroutines of init, so a mutex protects it.
type clock struct {
	mu      sync.Mutex
	sources []string
	last    *timeSync
}

func newClock(sources []string) *clock {
	return &clock{sources: sources}
}

func (c *clock) record(measured *timeSync) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.last = measured
}

// lastSyncAt returns the time of the last measurement, or the zero time
// before the first one.
func (c *clock) lastSyncAt() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last == nil {
		return time.Time{}
	}
	return c.last.at
}

// timeSources returns the sources this machine queries for the time. A
// leader uses the Cluster's upstreams, and has no sources when none are
// declared. A follower uses the declared address of each leader, read
// from the fleet's Machine manifests in the image, and then the host of
// the cluster endpoint, which covers a leader with no declared address,
// such as a DHCP leader.
func timeSources(clusterDoc *cluster.Cluster, role api.Role, manifestDir string) []string {
	if clusterDoc == nil {
		return nil
	}
	if role == api.RoleLeader {
		return clusterDoc.Spec.Time.Upstreams
	}
	sources := leaderAddresses(clusterDoc, manifestDir)
	endpoint, err := url.Parse(clusterDoc.Spec.Endpoint)
	if err == nil && endpoint.Hostname() != "" && !slices.Contains(sources, endpoint.Hostname()) {
		sources = append(sources, endpoint.Hostname())
	}
	return sources
}

// leaderAddresses returns the declared node address of each machine in
// spec.leaders. It skips a leader whose address it cannot resolve,
// because the endpoint fallback in timeSources covers it.
func leaderAddresses(clusterDoc *cluster.Cluster, manifestDir string) []string {
	var addresses []string
	for _, name := range clusterDoc.Spec.Leaders {
		if addr := declaredNodeAddress(clusterDoc, manifestDir, name); addr != "" {
			addresses = append(addresses, addr)
		}
	}
	return addresses
}

// declaredNodeAddress returns the address that a machine's manifest
// declares inside spec.network.nodeCIDR. It returns "" when the
// network or the manifest is invalid or the machine declares no
// address there, for example a DHCP machine. Machines find each other
// this way before any of them has started.
func declaredNodeAddress(clusterDoc *cluster.Cluster, manifestDir, name string) string {
	_, subnet, err := net.ParseCIDR(clusterDoc.Spec.Network.NodeCIDR)
	if err != nil {
		return ""
	}
	m, err := machine.Load(filepath.Join(manifestDir, name+".yaml"))
	if err != nil {
		return ""
	}
	for _, iface := range m.Spec.Network.Interfaces {
		ip, _, err := net.ParseCIDR(iface.Address)
		if err == nil && subnet.Contains(ip) {
			return ip.String()
		}
	}
	return ""
}

// timeStatus returns the clock's state for the machine's status. A
// machine with sources that has not yet measured is Unsynchronized at
// stratum 16, and a machine with no sources is FreeRunning at stratum
// 10. Only a measurement makes the state Synchronized.
//
// The offset is a bound such as "±5ms" (clockbound.go). After a step at
// boot the offset is empty, because the measurement is the error from
// before the step. The first poll after it publishes a bound.
func timeStatus(sync *timeSync, sources []string) machine.TimeStatus {
	if sync == nil {
		if len(sources) == 0 {
			return machine.TimeStatus{State: machine.TimeFreeRunning, Stratum: stratumFreeRunning}
		}
		return machine.TimeStatus{State: machine.TimeUnsynchronized, Stratum: stratumUnsynchronized}
	}
	status := machine.TimeStatus{
		State:   machine.TimeSynchronized,
		Source:  sync.source,
		Stratum: sync.stratum + 1,
	}
	if !sync.stepped {
		status.Offset = machine.OffsetBoundOf(sync.offset).String()
	}
	return status
}

// querySources queries each source in turn and returns the first valid
// reply. A full NTP daemon polls every source and combines the ones
// that pass its checks. init does not, because a person declared each
// source, and none comes from a public pool.
func querySources(sources []string) (*timeSync, error) {
	var lastErr error
	for _, source := range sources {
		response, err := ntp.QueryWithOptions(source, ntp.QueryOptions{})
		if err == nil {
			err = response.Validate()
		}
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", source, err)
			continue
		}
		return &timeSync{
			source:  source,
			stratum: int(response.Stratum),
			offset:  response.ClockOffset,
			delay:   response.RTT,
			at:      time.Now(),
		}, nil
	}
	return nil, lastErr
}

// stepClockAtBoot is the only code that steps the clock. It tries the
// sources up to 3 times and waits 1, 2, and then 3 seconds after each
// failure, so a network that never answers delays boot by 6 seconds at
// most. Then boot continues on the hardware clock and the discipline
// loop keeps trying. It returns the measurement for status, or nil.
func stepClockAtBoot(sources []string) *timeSync {
	if len(sources) == 0 {
		fmt.Println("liken: time: no time sources declared, so the clock runs on the hardware clock alone")
		return nil
	}
	for attempt := range 3 {
		sync, err := queryTimeSources(sources)
		if err != nil {
			fmt.Fprintf(os.Stderr, "liken: time: querying time sources: %v\n", err)
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}
		if abs := sync.offset.Abs(); abs < stepThreshold {
			fmt.Printf("liken: time: clock is %s from %s (stratum %d), under the step threshold, so the discipline loop will slew it\n",
				sync.offset.Round(10*time.Microsecond), sync.source, sync.stratum)
			return sync
		}
		corrected := time.Now().Add(sync.offset)
		if err := setSystemClock(corrected); err != nil {
			fmt.Fprintf(os.Stderr, "liken: time: stepping the clock with clock_settime: %v\n", err)
			return sync
		}
		fmt.Printf("liken: time: stepped the clock by %s using %s (stratum %d), now %s\n",
			sync.offset.Round(time.Millisecond), sync.source, sync.stratum,
			corrected.Format(time.RFC3339))
		sync.at = time.Now()
		sync.stepped = true
		return sync
	}
	fmt.Fprintln(os.Stderr, "liken: time: no time source answered after 3 attempts, so boot continues on the hardware clock and the discipline loop keeps querying")
	return nil
}

// These variables let a test replace the network query and the clock
// syscalls. A real query uses a socket, which stops a synctest
// bubble's clock, and the syscalls need PID 1's privileges. Only tests
// assign to them.
var (
	queryTimeSources  = querySources
	setSystemClock    = clockSettime
	slewSystemClock   = slewClock
	pendingSlew       = readPendingSlew
	saveHardwareClock = writeRTC
)

func clockSettime(t time.Time) error {
	ts := unix.NsecToTimespec(t.UnixNano())
	return unix.ClockSettime(unix.CLOCK_REALTIME, &ts)
}

// slewAmount clamps the offset to +/-500ms, so a larger error is
// corrected over several polls. The clamp is there because the old
// adjtime interface is said to work best within half a second, a claim
// that nothing in this repository checks.
func slewAmount(offset time.Duration) time.Duration {
	return min(max(offset, -500*time.Millisecond), 500*time.Millisecond)
}

// slewClock uses ADJ_OFFSET_SINGLESHOT, the old adjtime interface: the
// kernel runs the clock about 0.5ms per second fast or slow until it
// has applied the offset. The clock never jumps and never runs
// backward, which is the reason to slew it and not step it.
func slewClock(offset time.Duration) error {
	tx := &unix.Timex{
		Modes:  unix.ADJ_OFFSET_SINGLESHOT,
		Offset: slewAmount(offset).Microseconds(),
	}
	_, err := unix.Adjtimex(tx)
	return err
}

// readPendingSlew returns the part of the last slew that the kernel has
// not applied yet. ADJ_OFFSET_SS_READ reads it without changing it.
// When the read fails it returns 0, which overstates the applied slew
// by at most one slew.
func readPendingSlew() time.Duration {
	tx := &unix.Timex{Modes: unix.ADJ_OFFSET_SS_READ}
	if _, err := unix.Adjtimex(tx); err != nil {
		return 0
	}
	return time.Duration(tx.Offset) * time.Microsecond
}

// syncStaleAfter is how long after its last measurement the loop keeps
// reporting Synchronized. machine/clock.go gives the reason.
const syncStaleAfter = machine.ClockStaleAfter

// writeRTC copies the system clock into the hardware clock (RTC), which
// the next boot starts from. Linux does not do this by itself, and
// there is no shutdown script, so init writes it after the first
// measurement of a boot and at a clean shutdown. The first write lets a
// machine that loses power boot with close to the right time. The RTC
// holds UTC, so every machine of a fleet uses one convention.
func writeRTC() {
	f, err := os.OpenFile("/dev/rtc0", os.O_WRONLY, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: time: opening the RTC: %v\n", err)
		return
	}
	defer f.Close()
	now := time.Now().UTC()
	// RTC_SET_TIME takes a struct tm: months count from 0 and years
	// from 1900.
	rt := unix.RTCTime{
		Sec:  int32(now.Second()),
		Min:  int32(now.Minute()),
		Hour: int32(now.Hour()),
		Mday: int32(now.Day()),
		Mon:  int32(now.Month() - 1),
		Year: int32(now.Year() - 1900),
	}
	if err := unix.IoctlSetRTCTime(int(f.Fd()), &rt); err != nil {
		fmt.Fprintf(os.Stderr, "liken: time: writing the hardware clock: %v\n", err)
		return
	}
	fmt.Printf("liken: time: hardware clock set to %s\n", now.Format(time.RFC3339))
}

// disciplineClock returns the loop that polls the time sources every
// timePollInterval until the context ends.
//
// Each answer goes through the clock filter (clockfilter.go), which
// selects a sample and corrects it for the slews applied since it was
// measured. The loop slews the clock by the sample's offset, and writes
// the exact offset to a file in readingDir on every poll, where
// machine-operator reads it for Prometheus. It writes the time status
// (state, source, stratum, and offset bound) to the facts tree only
// when the status changes, so a steady clock causes no status writes.
//
// The loop is the only writer of the time status, so it keeps a copy of
// what it last published and needs no lock. It prints a line when sync
// is gained or lost, when the bound changes, and when the offset passes
// stepThreshold, which means the clock drifts faster than the slew
// corrects it.
func disciplineClock(clk *clock, tree machine.FactsTree, readingDir string, initial machine.TimeStatus) func(context.Context) error {
	return func(ctx context.Context) error {
		// initial is the time status that the boot published.
		published := initial
		lastGood := clk.lastSyncAt()
		filter := &clockFilter{}
		streak := &betterBoundStreak{}
		publish := func(next machine.TimeStatus) {
			tree.WriteTime(next)
			published = next
		}
		// When the boot step measured the time, main.go has already
		// written the RTC. Otherwise the loop writes it at its first
		// measurement.
		rtcWritten := published.State == machine.TimeSynchronized
		for {
			if !sleepUnlessCancelled(ctx, timePollInterval) {
				// A clean shutdown writes the clock to the RTC, unless
				// the clock was never measured during this boot.
				if !lastGood.IsZero() {
					saveHardwareClock()
				}
				return nil
			}
			sync, err := queryTimeSources(clk.sources)
			if err != nil {
				// A failed poll changes the published state only after
				// syncStaleAfter. Then the state becomes Unsynchronized
				// and the offset bound is dropped, because the
				// measurement it describes is too old.
				if published.State == machine.TimeSynchronized && time.Since(lastGood) > syncStaleAfter {
					fmt.Fprintf(os.Stderr, "liken: time: lost every source (%v); the clock is on its own\n", err)
					lost := published
					lost.State = machine.TimeUnsynchronized
					lost.Stratum = stratumUnsynchronized
					lost.Offset = ""
					publish(lost)
					streak.reset()
				}
				continue
			}

			// Each slew replaces the part of the last one that is still
			// pending, so every poll requests the clock's whole
			// current error.
			applied := filter.requested - pendingSlew()
			selected := filter.observe(*sync, applied)
			if err := slewSystemClock(selected.offset); err != nil {
				fmt.Fprintf(os.Stderr, "liken: time: slewing the clock with adjtimex: %v\n", err)
			} else {
				filter.slewed(applied, slewAmount(selected.offset))
			}
			if published.State != machine.TimeSynchronized {
				fmt.Printf("liken: time: synchronized to %s (stratum %d), offset %s\n",
					selected.source, selected.stratum, selected.offset.Round(10*time.Microsecond))
			} else if selected.offset.Abs() >= stepThreshold {
				fmt.Printf("liken: time: offset %s from %s is more than one slew can correct, so the correction takes several polls\n",
					selected.offset.Round(time.Millisecond), selected.source)
			}
			lastGood = sync.at
			clk.record(sync)
			if !rtcWritten {
				saveHardwareClock()
				rtcWritten = true
			}
			if err := machine.WriteClockReading(readingDir, machine.ClockReading{
				Offset: selected.offset, Delay: selected.delay, Source: selected.source, At: sync.at,
			}); err != nil {
				fmt.Fprintf(os.Stderr, "liken: time: writing the clock reading: %v\n", err)
			}

			next := timeStatus(&selected, clk.sources)
			if next.State != published.State || next.Source != published.Source || next.Stratum != published.Stratum {
				publish(next)
				streak.reset()
				continue
			}
			if bound, changed := streak.next(published.Offset, machine.OffsetBoundOf(selected.offset)); changed {
				fmt.Printf("liken: time: offset is within %s of %s (was within %s), last measured %s\n",
					consoleBound(bound), selected.source, consoleBound(published.Offset), selected.offset.Round(10*time.Microsecond))
				next.Offset = bound
				publish(next)
			}
		}
	}
}
