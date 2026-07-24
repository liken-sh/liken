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

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/machine"
)

// timePollInterval sets how often the discipline loop measures the
// clock. 64 seconds is NTP's classic starting interval (minpoll 6,
// or 2^6 seconds). This interval is frequent enough to catch drift
// measured in parts per million, and infrequent enough that it does
// not burden an upstream time source.
const timePollInterval = 64 * time.Second

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
// answered, its stratum, how far off this machine's clock was, and
// when the measurement happened.
type timeSync struct {
	source  string
	stratum int
	offset  time.Duration
	at      time.Time
}

// clock holds the machine's timekeeping state. Two components share
// it: the discipline loop records each measurement, and, on a
// leader, the responder reads the latest measurement to know what to
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

// timeStatus reports the clock's state as machine status. It reports
// the same facts that the console prints, in a form other systems
// can query. A machine that has sources but has not synced yet is
// Unsynchronized, at stratum 16. A machine with no sources at all is
// deliberately FreeRunning, at stratum 10. Neither state is reported
// as Synchronized, because that word is reserved for a clock that is
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

// syncStaleAfter is how long the loop keeps reporting synchronized
// after its last good measurement. Three missed polls means the
// source is gone, not just busy, and status must stop reporting
// synchronized at that point.
const syncStaleAfter = 3 * timePollInterval

// worthRepublishing decides whether a fresh measurement changes what
// the published time facts report. A change in state, source, or
// stratum must always be reported. The offset must be reported only
// when it has moved past offsetPublishThreshold since the last
// publish. SNTP measurements wobble by microseconds on every poll,
// and each republished fact has a cost: the machine publishes a
// status update whenever the facts change, and each such write
// causes a raft round and an fsync on every one of the cluster's
// leaders. A fleet whose clocks are working correctly should cost
// etcd nothing extra. The freshness floor limits the one case where
// suppressing updates could mislead: lastSync must not go so stale
// that the status reports a silent sync loop, while init is actually
// still receiving answers from its sources.
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
// measures, slews, publishes, sleeps, and repeats. The clock loop is
// the only writer of the time/ subtree, so it keeps a local copy of the
// clock's state and needs no lock: no other component ever writes those
// files. disciplineClock prints transitions rather than every poll: a
// sync gained, a sync lost, and an offset only when the offset exceeds
// the step threshold, which means drift is outrunning the slew. The
// facts follow the same rule. The loop rewrites the time/ files only
// when worthRepublishing reports that the measurement matters, and never
// for microsecond wobble; between writes it holds the latest value in
// its local copy.
func disciplineClock(clk *clock, tree machine.FactsTree, initial machine.TimeStatus) func(context.Context) error {
	return func(ctx context.Context) error {
		// current holds the clock's state. This loop is the only writer
		// of the time/ subtree, so a local copy lets the loop read a
		// consistent value as it makes its decisions. It starts from the
		// seed the boot step published.
		current := initial

		lastGood := time.Time{}
		if current.LastSync != nil {
			lastGood = *current.LastSync
		}
		// published holds what the time/ subtree currently says. It is
		// the baseline that every worthRepublishing check compares
		// against. The boot step published this value, moments ago.
		published := current
		publishedOffset, _ := time.ParseDuration(current.Offset)
		publishedAt := time.Now()
		// The boot step, or the lack of one, decided whether the RTC
		// has been written yet. If the boot came up on a wrong
		// hardware clock, this loop corrects the RTC at the first
		// sync it achieves.
		rtcWritten := current.State == machine.TimeSynchronized
		for {
			if !sleepUnlessCancelled(ctx, timePollInterval) {
				// Clean shutdown. Leave the hardware clock holding
				// the best time estimate this machine ever had.
				if !lastGood.IsZero() {
					writeRTC()
				}
				return nil
			}
			sync, err := querySources(clk.sources)
			if err != nil {
				// A failed poll is worth reporting only when it
				// changes the machine's state. Past the staleness
				// window, the machine stops reporting a synchronized
				// clock.
				if current.State == machine.TimeSynchronized && time.Since(lastGood) > syncStaleAfter {
					fmt.Fprintf(os.Stderr, "liken: time: lost every source (%v); the clock is on its own\n", err)
					current.State = machine.TimeUnsynchronized
					current.Stratum = stratumUnsynchronized
					tree.WriteTime(current)
					published, publishedAt = current, time.Now()
				}
				continue
			}

			if err := slewClock(sync.offset); err != nil {
				fmt.Fprintf(os.Stderr, "liken: time: slewing the clock: %v\n", err)
			}
			if current.State != machine.TimeSynchronized {
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
			// This drift check compares against the offset that was
			// last published, not the offset from the last poll.
			// Small wobbles accumulate toward the threshold this
			// way, instead of resetting every 64 seconds.
			current = timeStatus(sync, clk.sources)
			if worthRepublishing(published, current, sync.offset-publishedOffset, time.Since(publishedAt)) {
				tree.WriteTime(current)
				published, publishedOffset, publishedAt = current, sync.offset, time.Now()
			}
		}
	}
}
