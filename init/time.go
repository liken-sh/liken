package main

// Disciplining the clock.
//
// A computer's clock drifts: cheap oscillators gain or lose seconds
// a day. Kubernetes assumes nobody's does. TLS certificates carry
// notBefore/notAfter instants, leases carry renew deadlines, and
// etcd (when it arrives) orders events by time. A machine whose
// clock is wrong enough can't even *join* a cluster, because every
// certificate the CA minted appears to be from the future. That's
// why time is a machine-plane concern and why the first correction
// happens before k3s starts: the cluster cannot fix a clock that is
// keeping the machine out of the cluster.
//
// The protocol is SNTP, the stateless subset of NTP: one 48-byte
// request, one 48-byte reply, four timestamps between them. The
// client notes when it asked (t1) and when the answer arrived (t4);
// the server stamps when the request landed (t2) and when the reply
// left (t3). (t2-t1) is the outbound trip plus the clock error;
// (t3-t4) is the return trip minus it; averaging the two cancels the
// travel whenever the path is symmetric, leaving just the error.
// That algebra is the core of the protocol. The vendored client
// (github.com/beevik/ntp, the same library Talos uses) implements it
// along with the protocol's sanity checks: leap-second flags,
// kiss-of-death codes, stratum bounds. As with the DHCP client,
// liken uses the established library for the wire format and keeps
// the decisions (who to ask, when to step, how hard to slew) in
// plain sight here.
//
// The hierarchy follows liken's usual pattern: explicit inputs, no
// discovery. Leaders ask the upstreams declared on the Cluster.
// Followers ask the leaders themselves, resolved from the fleet's
// Machine manifests, with the endpoint's host as the fallback, and a
// leader answers from its own disciplined clock (responder.go). A
// cluster with no upstreams free-runs: it is internally consistent,
// correct only if the hardware clocks happen to be, and its status
// reports that state.
//
// Correction comes in two strengths, chosen by whether anything is
// running that could observe a change. At boot, before k3s, the
// clock simply *steps* to the measured time (clock_settime): nothing
// is running that could care, and a machine must not join the
// cluster with a wrong clock. After that, the clock only ever
// *slews* (adjtimex): the kernel trims the clock's rate so it drifts
// gently onto the right time, seconds always moving forward at
// nearly one per second. Stepping a running node would change the
// time underneath lease renewals and container logs; a slew removes
// the drift without any visible jump.

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

	"github.com/chrisguidry/liken/machine"
)

// timePollInterval is how often the discipline loop measures. 64
// seconds is NTP's classic starting cadence (minpoll 6, 2^6 s):
// frequent enough to catch drift measured in parts per million,
// infrequent enough not to burden anyone's upstream.
const timePollInterval = 64 * time.Second

// stepThreshold is the offset below which a boot doesn't bother
// stepping: the running slew will absorb it faster than the step's
// disruption is worth. 128ms is ntpd's own line between "slew it"
// and "step it".
const stepThreshold = 128 * time.Millisecond

// The stratum vocabulary status reports. NTP counts distance from a
// reference clock: 1 is attached to one, each hop adds one. 10 is
// the widespread convention for a deliberately local clock, and 16
// means "unsynchronized": the machine has time sources but hasn't
// reached one yet.
const (
	stratumFreeRunning    = 10
	stratumUnsynchronized = 16
)

// timeSync is one successful measurement: who answered, from what
// stratum, how far off this machine's clock was, and when.
type timeSync struct {
	source  string
	stratum int
	offset  time.Duration
	at      time.Time
}

// clock is the machine's account of its own timekeeping, shared by
// the two components that care: the discipline loop records each
// measurement, and (on a leader) the responder reads the latest to
// know what to advertise. This is what running the machine plane in
// one process buys: two daemons would need a socket between them;
// two goroutines need a mutex.
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

// timeSources derives where this machine gets its time, the same way
// it derives everything: from declared inputs, by role. Leaders ask
// the Cluster's upstreams. Followers ask every leader, resolved from
// the Machine manifests the image already carries (one boot medium
// holds the whole fleet's), each leader identified by its declared
// address on the node network. The endpoint's host is appended as
// the fallback for leaders that couldn't be resolved (a
// DHCP-addressed leader declares no address to find). Every source
// therefore comes from inputs the machine already needed to find its
// cluster; no new information is required. nil means free-running: a
// leader with no upstreams declared.
func timeSources(cluster *machine.Cluster, role machine.Role, manifestDir string) []string {
	if cluster == nil {
		return nil
	}
	if role == machine.RoleLeader {
		return cluster.Spec.Time.Upstreams
	}
	sources := leaderAddresses(cluster, manifestDir)
	endpoint, err := url.Parse(cluster.Spec.Endpoint)
	if err == nil && endpoint.Hostname() != "" && !slices.Contains(sources, endpoint.Hostname()) {
		sources = append(sources, endpoint.Hostname())
	}
	return sources
}

// leaderAddresses resolves each machine named in spec.leaders to its
// static address on the node network, by reading its manifest from
// the image. A leader that can't be resolved (no manifest, no
// address inside nodeCIDR) is simply skipped: the endpoint fallback
// covers it, and the source list is a preference order in which
// missing entries are acceptable.
func leaderAddresses(cluster *machine.Cluster, manifestDir string) []string {
	var addresses []string
	for _, name := range cluster.Spec.Leaders {
		if addr := declaredNodeAddress(cluster, manifestDir, name); addr != "" {
			addresses = append(addresses, addr)
		}
	}
	return addresses
}

// declaredNodeAddress resolves one machine's declared address on the
// node network from its manifest in the image, "" when the machine
// declares none (a DHCP machine has no address to find). This is how
// machines find each other before any of them is up: the fleet's
// declared inputs, not discovery.
func declaredNodeAddress(cluster *machine.Cluster, manifestDir, name string) string {
	_, subnet, err := net.ParseCIDR(cluster.Spec.Network.NodeCIDR)
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

// timeStatus reports the clock's state as status: the same facts the
// console prints, made queryable. A machine with sources that hasn't
// synced yet is Unsynchronized (stratum 16); a machine with no
// sources at all is deliberately FreeRunning (stratum 10); neither
// claims Synchronized, because that word is reserved for a clock
// currently following a source that is itself synchronized.
func timeStatus(sync *timeSync, sources []string) machine.TimeStatus {
	if sync == nil {
		if len(sources) == 0 {
			return machine.TimeStatus{State: machine.TimeFreeRunning, Stratum: stratumFreeRunning}
		}
		return machine.TimeStatus{State: machine.TimeUnsynchronized, Stratum: stratumUnsynchronized}
	}
	at := sync.at
	return machine.TimeStatus{
		State:    machine.TimeSynchronized,
		Source:   sync.source,
		Stratum:  sync.stratum + 1,
		Offset:   sync.offset.Round(10 * time.Microsecond).String(),
		LastSync: &at,
	}
}

// querySources asks each source in turn and takes the first valid
// answer. A full NTP daemon polls every source, scores them by delay
// and dispersion, and combines survivors. SNTP's simpler approach,
// trusting the first sane reply, is fine for a machine whose sources
// were each explicitly chosen by an operator rather than drawn from
// a public pool.
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
			at:      time.Now(),
		}, nil
	}
	return nil, lastErr
}

// stepClockAtBoot is the one moment liken ever jumps the clock: k3s
// hasn't started, so no lease, log, or watch depends on the current
// time yet. The attempts are bounded because a machine's boot must
// not hinge on the internet being up. If no source answers, the boot
// proceeds on the hardware clock and the discipline loop keeps
// trying forever. It returns the first sync so status can carry it.
func stepClockAtBoot(sources []string) *timeSync {
	if len(sources) == 0 {
		fmt.Println("liken: time: no sources declared; free-running on the hardware clock")
		return nil
	}
	for attempt := range 3 {
		sync, err := querySources(sources)
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
		ts := unix.NsecToTimespec(corrected.UnixNano())
		if err := unix.ClockSettime(unix.CLOCK_REALTIME, &ts); err != nil {
			fmt.Fprintf(os.Stderr, "liken: time: stepping the clock: %v\n", err)
			return sync
		}
		fmt.Printf("liken: time: stepped the clock %s from %s (stratum %d); it is %s\n",
			sync.offset.Round(time.Millisecond), sync.source, sync.stratum,
			corrected.Format(time.RFC3339))
		sync.at = time.Now()
		return sync
	}
	fmt.Fprintln(os.Stderr, "liken: time: no source answered; booting on the hardware clock (the discipline loop keeps trying)")
	return nil
}

// slewAmount bounds how much correction one adjtimex call requests.
// The kernel's old-style singleshot adjustment works best within
// half a second; a larger error is corrected across several polls
// rather than one large request. The clamp, not the caller, owns
// this limit.
func slewAmount(offset time.Duration) time.Duration {
	return min(max(offset, -500*time.Millisecond), 500*time.Millisecond)
}

// slewClock asks the kernel to gently absorb the offset: the
// old-style adjtime interface (ADJ_OFFSET_SINGLESHOT) trims the
// clock's tick rate by about 0.5ms per second until the requested
// offset has been absorbed, then resumes normal ticking. Time never
// jumps and never runs backward, which is the entire point of
// slewing over stepping.
func slewClock(offset time.Duration) error {
	tx := &unix.Timex{
		Modes:  unix.ADJ_OFFSET_SINGLESHOT,
		Offset: slewAmount(offset).Microseconds(),
	}
	_, err := unix.Adjtimex(tx)
	return err
}

// syncStaleAfter is how long the loop keeps claiming synchronized
// after its last good measurement: three missed polls means the
// source is gone, not just busy, and status must stop saying
// otherwise.
const syncStaleAfter = 3 * timePollInterval

// worthRepublishing decides whether a fresh measurement changes the
// story the published time facts tell. A change of state, source, or
// stratum is always news. The offset is only news when it has moved
// past offsetPublishThreshold since the last publish, because SNTP
// measurements wobble by microseconds on every poll, and every
// republished fact ripples outward: the operator publishes a status
// whenever the facts change, and each of those writes is a raft
// round and an fsync on every one of the cluster's leaders. A fleet
// whose clocks are fine should cost etcd nothing. The freshness
// floor bounds the one lie suppression could tell: lastSync must not
// drift so stale that the status claims a silent sync loop while
// init is happily hearing its sources.
const (
	offsetPublishThreshold = 25 * time.Millisecond
	timePublishFloor       = 10 * time.Minute
)

func worthRepublishing(published, fresh machine.TimeStatus, drift, sincePublished time.Duration) bool {
	if published.State != fresh.State || published.Source != fresh.Source || published.Stratum != fresh.Stratum {
		return true
	}
	return drift.Abs() >= offsetPublishThreshold || sincePublished >= timePublishFloor
}

// writeRTC copies the system clock into the hardware clock. Linux
// never does this on its own: on a traditional distro it's a
// shutdown script's job, so here it's init's. The RTC is the clock
// the machine starts its next boot from. Writing it after a sync
// means even a power-cut machine boots with roughly right time, and
// writing it at clean shutdown carries the best final estimate into
// the next boot. Those are the only two moments; the RTC ticks on
// its own battery between them. The value written is UTC: storing
// local time in the RTC is a desktop-PC legacy, and a fleet spanning
// time zones needs its hardware clocks to share one convention.
func writeRTC() {
	f, err := os.OpenFile("/dev/rtc0", os.O_WRONLY, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: time: opening the RTC: %v\n", err)
		return
	}
	defer f.Close()
	now := time.Now().UTC()
	// The RTC interface takes a broken-down calendar time in the
	// style of C's struct tm: months count from zero and years from
	// 1900, quirks the ioctl inherits from four decades of C.
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

// disciplineClock builds the machine plane's time component: measure,
// slew, publish, sleep, forever. It is the only writer of facts.Time
// and, from the moment it starts, of the facts file, so no lock is
// needed. It prints transitions rather than every poll: a sync
// gained, a sync lost, and an offset only when it exceeds the step
// threshold, which means drift is outrunning the slew. The facts get
// the same restraint: a poll's measurement replaces facts.Time in
// memory every time, but the file is only rewritten when the
// measurement is news (worthRepublishing), never for microsecond
// wobble.
func disciplineClock(clk *clock, facts *machine.MachineStatus) func(context.Context) error {
	return func(ctx context.Context) error {
		lastGood := time.Time{}
		if facts.Time.LastSync != nil {
			lastGood = *facts.Time.LastSync
		}
		// What the facts file currently says, the baseline every
		// "is this news?" judgment compares against. The boot step
		// published facts.Time as it stands now, moments ago.
		published := facts.Time
		publishedOffset, _ := time.ParseDuration(facts.Time.Offset)
		publishedAt := time.Now()
		// The boot step (or its absence) decided whether the RTC has
		// been written yet; a boot that came up on a wrong hardware
		// clock gets its RTC corrected at the first sync this loop
		// achieves instead.
		rtcWritten := facts.Time.State == machine.TimeSynchronized
		for {
			if !sleepUnlessCancelled(ctx, timePollInterval) {
				// Clean shutdown: leave the hardware clock holding
				// the best estimate this machine ever had.
				if !lastGood.IsZero() {
					writeRTC()
				}
				return nil
			}
			sync, err := querySources(clk.sources)
			if err != nil {
				// A failed poll is only worth reporting when it
				// changes the machine's state: past the staleness
				// window, the machine stops claiming a synchronized
				// clock.
				if facts.Time.State == machine.TimeSynchronized && time.Since(lastGood) > syncStaleAfter {
					fmt.Fprintf(os.Stderr, "liken: time: lost every source (%v); the clock is on its own\n", err)
					facts.Time.State = machine.TimeUnsynchronized
					facts.Time.Stratum = stratumUnsynchronized
					publishTimeFacts(facts)
					published, publishedAt = facts.Time, time.Now()
				}
				continue
			}

			if err := slewClock(sync.offset); err != nil {
				fmt.Fprintf(os.Stderr, "liken: time: slewing the clock: %v\n", err)
			}
			if facts.Time.State != machine.TimeSynchronized {
				fmt.Printf("liken: time: synchronized to %s (stratum %d), offset %s\n",
					sync.source, sync.stratum, sync.offset.Round(10*time.Microsecond))
			} else if sync.offset.Abs() >= stepThreshold {
				fmt.Printf("liken: time: offset %s from %s exceeds the slew's pace; correcting over several polls\n",
					sync.offset.Round(time.Millisecond), sync.source)
			}
			lastGood = sync.at
			clk.record(sync)
			if !rtcWritten {
				writeRTC()
				rtcWritten = true
			}
			// The drift judged here is against the offset last
			// *published*, not the last poll's, so small wobbles
			// accumulate toward the threshold instead of resetting it
			// every 64 seconds.
			facts.Time = timeStatus(sync, clk.sources)
			if worthRepublishing(published, facts.Time, sync.offset-publishedOffset, time.Since(publishedAt)) {
				publishTimeFacts(facts)
				published, publishedOffset, publishedAt = facts.Time, sync.offset, time.Now()
			}
		}
	}
}

// publishTimeFacts rewrites the facts file with the current time
// status. The write is the same atomic replace every facts write is,
// so the operator sees old facts or new, never torn ones.
func publishTimeFacts(facts *machine.MachineStatus) {
	if err := machine.WriteFacts(machine.FactsPath, facts); err != nil {
		fmt.Fprintf(os.Stderr, "liken: time: writing facts: %v\n", err)
	}
}
