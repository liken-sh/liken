package main

// Disciplining the clock.
//
// A computer's clock drifts. Cheap oscillators gain or lose seconds
// each day. Kubernetes assumes that no clock drifts. TLS
// certificates carry notBefore and notAfter instants, leases carry
// renew deadlines, and, on multi-leader clusters, etcd orders events
// by time. If a machine's clock is wrong enough, the machine cannot
// even join a cluster, because every certificate that the CA issued
// appears to be from the future. This is why time is a machine-plane
// concern, and why the first correction happens before k3s starts:
// the cluster cannot fix a clock that is keeping the machine out of
// the cluster.
//
// The protocol is SNTP, the stateless subset of NTP. Each exchange
// sends one 48-byte request and receives one 48-byte reply, with
// four timestamps between them. The client records when it sent the
// request (t1) and when the reply arrived (t4). The server records
// when the request arrived (t2) and when the reply left (t3). The
// value (t2-t1) is the outbound trip time plus the clock error. The
// value (t3-t4) is the return trip time minus the clock error.
// Averaging the two values cancels the travel time when the path is
// symmetric, and leaves only the error. This calculation is the core
// of the protocol.
//
// The vendored client, github.com/beevik/ntp, is the same library
// that Talos uses. It implements this calculation and the protocol's
// checks: leap-second flags, kiss-of-death codes, and stratum
// bounds. As with the DHCP client, liken uses this established
// library for the wire format, and keeps the decisions in this file:
// which source to ask, when to step the clock, and how hard to slew
// it.
//
// The source hierarchy follows liken's usual pattern: explicit
// inputs, not discovery. Leaders ask the upstreams declared on the
// Cluster. Followers ask the leaders directly, resolved from the
// fleet's Machine manifests, with the endpoint's host as the
// fallback. A leader answers from its own disciplined clock (see
// responder.go). A cluster with no upstreams free-runs. A
// free-running cluster is internally consistent, but it is correct
// only if the hardware clocks happen to be correct, and the
// machine's status reports this free-running state.
//
// Correction comes in two strengths. The choice depends on whether
// anything is running that could notice a sudden change in time. At
// boot, before k3s starts, the clock steps to the measured time,
// using clock_settime. Nothing is running yet that would be
// affected, and a machine must not join the cluster with a wrong
// clock. After boot, the clock only slews, using adjtimex. The
// kernel trims the clock's rate so the clock drifts gradually onto
// the correct time, with seconds always moving forward at close to
// one per second. Stepping the clock on a running node would change
// the time underneath lease renewals and container logs. A slew
// removes the drift without any visible jump in time.

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

// timePollInterval sets how often the discipline loop measures the
// clock (machine/clock.go gives the reason for 64 seconds).
const timePollInterval = machine.ClockPollInterval

// stepThreshold is the offset below which a boot does not step the
// clock. Below this offset, the slew absorbs the error faster than
// the step's disruption is worth. 128ms is the same line that ntpd
// uses between slewing and stepping.
const stepThreshold = 128 * time.Millisecond

// The stratum values that status reports. NTP counts distance from a
// reference clock: stratum 1 is attached to a reference clock
// directly, and each hop away adds one. Stratum 10 is the common
// convention for a clock that is deliberately local. Stratum 16
// means unsynchronized: the machine has time sources, but has not
// reached one yet.
const (
	stratumFreeRunning    = 10
	stratumUnsynchronized = 16
)

// timeSync records one successful measurement: which source
// answered, its stratum, how far off this machine's clock was, how
// long the exchange's round trip took, and when the measurement
// happened. A boot measurement that stepped the clock is marked,
// because its offset describes the clock before the step.
type timeSync struct {
	source  string
	stratum int
	offset  time.Duration
	delay   time.Duration
	at      time.Time
	stepped bool
}

// clock holds the machine's timekeeping state. Two components share
// it: the discipline loop records each measurement, and, on a
// leader, the responder reads the latest measurement for what to
// advertise. Running the machine plane in one process makes this
// possible with a mutex. Two separate daemons would need a socket
// between them; two goroutines only need a mutex.
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

// lastSyncAt answers when the last good measurement was taken, or the
// zero time before the first one.
func (c *clock) lastSyncAt() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last == nil {
		return time.Time{}
	}
	return c.last.at
}

// timeSources works out where this machine gets its time. It uses
// declared inputs by role, the same way liken works out other
// machine state. Leaders ask the Cluster's upstreams. Followers ask
// every leader. timeSources resolves each leader's address from the
// Machine manifests that the image already carries; one boot medium
// carries manifests for the whole fleet. Each leader is identified
// by its declared address on the node network. timeSources appends
// the endpoint's host as a fallback for any leader it could not
// resolve, for example a DHCP-addressed leader that declares no
// address to find. Every source therefore comes from inputs the
// machine already needed to find its cluster, so no new information
// is required. timeSources returns nil for a leader with no
// upstreams declared, which means the leader free-runs.
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

// leaderAddresses resolves each machine named in spec.leaders to its
// static address on the node network. It reads each machine's
// manifest from the image. leaderAddresses skips a leader it cannot
// resolve, for example one with no manifest or no address inside
// nodeCIDR. The endpoint fallback covers a skipped leader, and the
// source list is a preference order in which missing entries are
// acceptable.
func leaderAddresses(clusterDoc *cluster.Cluster, manifestDir string) []string {
	var addresses []string
	for _, name := range clusterDoc.Spec.Leaders {
		if addr := declaredNodeAddress(clusterDoc, manifestDir, name); addr != "" {
			addresses = append(addresses, addr)
		}
	}
	return addresses
}

// declaredNodeAddress resolves one machine's declared address on the
// node network from its manifest in the image. It returns "" when
// the machine declares no address, for example a DHCP machine, which
// has no address to find. This is how machines find each other
// before any of them has started: through the fleet's declared
// inputs, not through discovery.
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

// timeStatus reports the clock's standing as machine status. It
// reports the same facts that the console prints, in a form other
// systems can query. A machine that has sources but has not synced yet
// is Unsynchronized, at stratum 16. A machine with no sources at all
// is deliberately FreeRunning, at stratum 10. Neither state is
// reported as Synchronized, because that word is reserved for a clock
// that is currently following a source that is itself synchronized.
//
// The offset is a bound, such as "±5ms" (clockbound.go). A boot that
// stepped the clock publishes no bound, because its measurement
// describes the clock before the step, and the first poll after it
// publishes one.
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

// querySources asks each source in turn and uses the first valid
// answer. A full NTP daemon polls every source, scores each one by
// delay and dispersion, and combines the results that pass its
// checks. SNTP's simpler approach, trusting the first valid reply,
// works for a machine whose sources were each chosen directly by an
// operator, rather than drawn from a public pool.
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

// stepClockAtBoot is the one moment when liken jumps the clock. k3s
// has not started yet, so no lease, log, or watch depends on the
// current time. The number of attempts is limited, because a
// machine's boot must not depend on the internet being available. If
// no source answers, the boot continues on the hardware clock, and
// the discipline loop keeps trying without limit. stepClockAtBoot
// returns the first successful sync so that status can report it.
func stepClockAtBoot(sources []string) *timeSync {
	if len(sources) == 0 {
		fmt.Println("liken: time: no sources declared; free-running on the hardware clock")
		return nil
	}
	for attempt := range 3 {
		sync, err := queryTimeSources(sources)
		if err != nil {
			fmt.Fprintf(os.Stderr, "liken: time: measuring: %v\n", err)
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}
		if abs := sync.offset.Abs(); abs < stepThreshold {
			fmt.Printf("liken: time: clock is %s from %s (stratum %d); close enough to slew\n",
				sync.offset.Round(10*time.Microsecond), sync.source, sync.stratum)
			return sync
		}
		corrected := time.Now().Add(sync.offset)
		if err := setSystemClock(corrected); err != nil {
			fmt.Fprintf(os.Stderr, "liken: time: stepping the clock: %v\n", err)
			return sync
		}
		fmt.Printf("liken: time: stepped the clock %s from %s (stratum %d); it is %s\n",
			sync.offset.Round(time.Millisecond), sync.source, sync.stratum,
			corrected.Format(time.RFC3339))
		sync.at = time.Now()
		sync.stepped = true
		return sync
	}
	fmt.Fprintln(os.Stderr, "liken: time: no source answered; booting on the hardware clock (the discipline loop keeps trying)")
	return nil
}

// The clock's actions are package variables so that a test can run
// the decisions above and below against fakes. A real query needs a
// socket, which stops a synctest bubble's clock, and the real clock
// syscalls need PID 1's privileges. Production code never replaces
// them.
var (
	queryTimeSources  = querySources
	setSystemClock    = clockSettime
	slewSystemClock   = slewClock
	pendingSlew       = readPendingSlew
	saveHardwareClock = writeRTC
)

// clockSettime steps the system clock to t with clock_settime.
func clockSettime(t time.Time) error {
	ts := unix.NsecToTimespec(t.UnixNano())
	return unix.ClockSettime(unix.CLOCK_REALTIME, &ts)
}

// slewAmount limits how much correction one adjtimex call requests.
// The kernel's old-style singleshot adjustment works best within
// half a second. slewAmount corrects a larger error across several
// polls, instead of requesting it all at once. This clamp owns the
// limit, not the code that calls slewAmount.
func slewAmount(offset time.Duration) time.Duration {
	return min(max(offset, -500*time.Millisecond), 500*time.Millisecond)
}

// slewClock asks the kernel to absorb the offset gradually. The
// old-style adjtime interface, ADJ_OFFSET_SINGLESHOT, trims the
// clock's tick rate by about 0.5ms per second until it has absorbed
// the requested offset, then resumes normal ticking. Time never
// jumps and never runs backward. This is the reason to slew the
// clock instead of stepping it.
func slewClock(offset time.Duration) error {
	tx := &unix.Timex{
		Modes:  unix.ADJ_OFFSET_SINGLESHOT,
		Offset: slewAmount(offset).Microseconds(),
	}
	_, err := unix.Adjtimex(tx)
	return err
}

// readPendingSlew answers the part of the last slew that the kernel
// has not applied yet. ADJ_OFFSET_SS_READ reads the old-style adjtime
// offset without changing it. A clock whose read fails reports nothing
// pending, which overstates what was applied by at most one slew.
func readPendingSlew() time.Duration {
	tx := &unix.Timex{Modes: unix.ADJ_OFFSET_SS_READ}
	if _, err := unix.Adjtimex(tx); err != nil {
		return 0
	}
	return time.Duration(tx.Offset) * time.Microsecond
}

// syncStaleAfter is how long the loop keeps reporting synchronized
// after its last good measurement (machine.ClockStaleAfter).
const syncStaleAfter = machine.ClockStaleAfter

// writeRTC copies the system clock into the hardware clock. Linux
// never does this by itself. On a traditional distribution, a
// shutdown script does this job; here, init does it. The RTC is the
// clock that the machine starts its next boot from. Writing the RTC
// after a sync means that even a machine that loses power boots with
// roughly correct time. Writing the RTC at a clean shutdown carries
// the best final time estimate into the next boot. These are the
// only two moments when init writes the RTC; between them, the RTC
// keeps time on its own battery. The value written is UTC. Storing
// local time in the RTC is a desktop-PC convention from the past,
// and a fleet spanning time zones needs its hardware clocks to share
// one convention.
func writeRTC() {
	f, err := os.OpenFile("/dev/rtc0", os.O_WRONLY, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: time: opening the RTC: %v\n", err)
		return
	}
	defer f.Close()
	now := time.Now().UTC()
	// The RTC interface takes a broken-down calendar time, in the
	// style of C's struct tm. Months count from zero and years count
	// from 1900. The ioctl inherits these conventions from four
	// decades of C programming.
	rt := unix.RTCTime{
		Sec:  int32(now.Second()),
		Min:  int32(now.Minute()),
		Hour: int32(now.Hour()),
		Mday: int32(now.Day()),
		Mon:  int32(now.Month() - 1),
		Year: int32(now.Year() - 1900),
	}
	if err := unix.IoctlSetRTCTime(int(f.Fd()), &rt); err != nil {
		fmt.Fprintf(os.Stderr, "liken: time: writing the RTC: %v\n", err)
		return
	}
	fmt.Printf("liken: time: hardware clock set to %s\n", now.Format(time.RFC3339))
}

// disciplineClock builds the machine plane's time component. It
// measures, filters, slews, records, publishes, sleeps, and repeats.
//
// Each answered poll goes through the clock filter (clockfilter.go),
// and the selected sample, corrected for the slews applied since it was
// measured, drives three outputs. The clock slews by it. The reading, the exact offset, goes to the file under
// readingDir on every answered poll, where machine-operator reads it
// for Prometheus. The standing goes to the facts tree, and from there
// to status, only when it changes: a new state, source, or stratum,
// or a new bound on the offset (clockbound.go). A healthy clock
// rewrites the facts tree never, so it costs the API server nothing.
//
// The loop is the only writer of the time standing, so it keeps the
// published standing in a local copy and needs no lock. It prints
// transitions rather than every poll: a sync gained, a sync lost, a
// new bound, and an offset past the step threshold, which means drift
// is outrunning the slew.
func disciplineClock(clk *clock, tree machine.FactsTree, readingDir string, initial machine.TimeStatus) func(context.Context) error {
	return func(ctx context.Context) error {
		// published holds what the time standing currently says. The
		// boot step published it, moments ago.
		published := initial
		lastGood := clk.lastSyncAt()
		filter := &clockFilter{}
		hold := &boundHold{}
		publish := func(next machine.TimeStatus) {
			tree.WriteTime(next)
			published = next
		}
		// The boot step, or the lack of one, determined whether the RTC
		// has been written yet. If the boot came up on a wrong
		// hardware clock, this loop corrects the RTC at the first
		// sync it achieves.
		rtcWritten := published.State == machine.TimeSynchronized
		for {
			if !sleepUnlessCancelled(ctx, timePollInterval) {
				// Clean shutdown. Leave the hardware clock holding
				// the best time estimate this machine ever had.
				if !lastGood.IsZero() {
					saveHardwareClock()
				}
				return nil
			}
			sync, err := queryTimeSources(clk.sources)
			if err != nil {
				// A failed poll is worth reporting only when it
				// changes the machine's state. Past the staleness
				// window, the machine stops reporting a synchronized
				// clock, and it drops the bound, because the bound
				// is a claim about a measurement that is now too old.
				if published.State == machine.TimeSynchronized && time.Since(lastGood) > syncStaleAfter {
					fmt.Fprintf(os.Stderr, "liken: time: lost every source (%v); the clock is on its own\n", err)
					lost := published
					lost.State = machine.TimeUnsynchronized
					lost.Stratum = stratumUnsynchronized
					lost.Offset = ""
					publish(lost)
					hold.reset()
				}
				continue
			}

			// The slew replaces whatever part of the last slew is still
			// pending, so the loop slews by the clock's whole error now
			// on every poll, and records it as the new request.
			applied := filter.requested - pendingSlew()
			selected := filter.observe(*sync, applied)
			if err := slewSystemClock(selected.offset); err != nil {
				fmt.Fprintf(os.Stderr, "liken: time: slewing the clock: %v\n", err)
			} else {
				filter.slewed(applied, slewAmount(selected.offset))
			}
			if published.State != machine.TimeSynchronized {
				fmt.Printf("liken: time: synchronized to %s (stratum %d), offset %s\n",
					selected.source, selected.stratum, selected.offset.Round(10*time.Microsecond))
			} else if selected.offset.Abs() >= stepThreshold {
				fmt.Printf("liken: time: offset %s from %s exceeds the slew's pace; correcting over several polls\n",
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
				fmt.Fprintf(os.Stderr, "liken: time: writing the reading: %v\n", err)
			}

			next := timeStatus(&selected, clk.sources)
			if next.State != published.State || next.Source != published.Source || next.Stratum != published.Stratum {
				publish(next)
				hold.reset()
				continue
			}
			if bound, changed := hold.next(published.Offset, machine.OffsetBoundOf(selected.offset)); changed {
				fmt.Printf("liken: time: offset within %s of %s (was %s); last measured %s\n",
					consoleBound(bound), selected.source, consoleBound(published.Offset), selected.offset.Round(10*time.Microsecond))
				next.Offset = bound
				publish(next)
			}
		}
	}
}
