package main

// Disciplining the clock.
//
// A computer's clock drifts — cheap oscillators gain or lose seconds
// a day — and Kubernetes quietly assumes nobody's does: TLS
// certificates carry notBefore/notAfter instants, leases carry renew
// deadlines, and etcd (when it arrives) orders events by time. A
// machine whose clock is wrong enough can't even *join* a cluster,
// because every certificate the CA minted appears to be from the
// future. That's why time is a machine-plane concern and why the
// first correction happens before k3s starts: the thing that's
// broken is below the thing that would fix it.
//
// The protocol is SNTP, the stateless subset of NTP: one 48-byte
// request, one 48-byte reply, four timestamps between them. The
// client notes when it asked (t1) and when the answer arrived (t4);
// the server stamps when the request landed (t2) and when the reply
// left (t3). (t2-t1) is the outbound trip plus the clock error;
// (t3-t4) is the return trip minus it; averaging the two cancels the
// travel whenever the path is symmetric, leaving just the error.
// That algebra is the whole trick, and the vendored client
// (github.com/beevik/ntp — the same library Talos uses) implements
// it along with the protocol's sanity checks: leap-second flags,
// kiss-of-death codes, stratum bounds. Like the DHCP client, liken
// takes the blessed library for the wire format and keeps the
// *decisions* — who to ask, when to step, how hard to slew — in
// plain sight here.
//
// The hierarchy is liken's usual shape: explicit inputs, no
// discovery. Leaders ask the upstreams declared on the Cluster;
// followers ask the leaders themselves — resolved from the fleet's
// Machine manifests, with the endpoint's host as the fallback — and
// a leader answers from its own disciplined clock (responder.go's
// story). A cluster with no upstreams free-runs: internally
// consistent, correct only if the hardware clocks happen to be, and
// status says so honestly.
//
// Correction comes in two strengths, chosen by whether anyone is
// watching. At boot, before k3s, the clock simply *steps* to the
// measured time (clock_settime): nothing is running that could care,
// and a wrong clock must not be allowed to greet the cluster. After
// that, the clock only ever *slews* (adjtimex): the kernel trims the
// clock's rate so it drifts gently onto the right time, seconds
// always moving forward at nearly one per second. Stepping a running
// node would yank time out from under lease renewals and container
// logs; a slew is invisible to everything but the drift it removes.

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
// polite enough not to pester anyone's upstream.
const timePollInterval = 64 * time.Second

// stepThreshold is the offset below which a boot doesn't bother
// stepping: the running slew will absorb it faster than the step's
// disruption is worth. 128ms is ntpd's own line between "slew it"
// and "step it".
const stepThreshold = 128 * time.Millisecond

// The stratum vocabulary status reports. NTP counts distance from a
// reference clock: 1 is attached to one, each hop adds one. 10 is
// the widespread convention for "my own local clock, on purpose",
// and 16 means "unsynchronized" — a machine that wants time and
// hasn't gotten it yet.
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
// the Cluster's upstreams. Followers ask the leaders — every one of
// them, resolved from the Machine manifests the image already
// carries (one boot medium holds the whole fleet's), each leader
// identified by its declared address on the node network. The
// endpoint's host is appended as the fallback for leaders that
// couldn't be resolved (a DHCP-addressed leader declares no address
// to find), so "who has the time?" never needs an answer "where is
// my cluster?" didn't already give. nil means free-running: a
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
// covers it, and a time source list is a preference order, not a
// promise.
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

// timeStatus is the clock's story as status: the same facts the
// console prints, made queryable. A machine with sources that hasn't
// synced yet is Unsynchronized (stratum 16); a machine with no
// sources at all is FreeRunning on purpose (stratum 10); neither
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
// and dispersion, and combines survivors; SNTP's simpler manner —
// trust the first sane reply — is fine for a machine whose sources
// were each explicitly chosen by an operator rather than drawn from
// a pool of strangers.
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
// hasn't started, so no lease, log, or watch can be yanked out from
// under anything. The attempts are bounded because a machine's boot
// must not hinge on the internet being up — if no source answers,
// the boot proceeds on the hardware clock and the discipline loop
// keeps trying forever. It returns the first sync so status can
// carry it.
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
// The kernel's old-style singleshot adjustment is happiest within
// half a second; a larger error is corrected across several polls
// rather than one big ask. The clamp, not the caller, owns this
// limit.
func slewAmount(offset time.Duration) time.Duration {
	return min(max(offset, -500*time.Millisecond), 500*time.Millisecond)
}

// slewClock asks the kernel to gently absorb the offset: the
// old-style adjtime interface (ADJ_OFFSET_SINGLESHOT) trims the
// clock's tick rate by about 0.5ms per second until the requested
// offset has been eaten, then resumes normal ticking. Time never
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

// writeRTC copies the system clock into the hardware clock. Linux
// never does this on its own — on a traditional distro it's a
// shutdown script's job, so here it's init's. The RTC is what the
// machine wakes up with: writing it after a sync means even a
// power-cut machine boots with roughly right time, and writing it
// at clean shutdown carries the best final estimate into the next
// boot. Those are the only two moments; the RTC ticks on its own
// battery between them. The value written is UTC: storing local
// time in the RTC is a desktop-PC legacy, and a fleet spanning time
// zones needs the hardware to speak one.
func writeRTC() {
	f, err := os.OpenFile("/dev/rtc0", os.O_WRONLY, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: time: opening the RTC: %v\n", err)
		return
	}
	defer f.Close()
	now := time.Now().UTC()
	// The RTC speaks a broken-down calendar time, tm-struct style:
	// months count from zero and years from 1900, quirks the ioctl
	// inherits from four decades of C.
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
// slew, publish, sleep, forever. It owns facts.Time — and, from the
// moment it starts, the facts file — as the only writer, so no lock.
// It narrates transitions rather than every poll: a sync gained, a
// sync lost, and an offset only when it exceeds the step threshold,
// which means drift is outrunning the slew.
func disciplineClock(clk *clock, facts *machine.MachineStatus) func(context.Context) error {
	return func(ctx context.Context) error {
		lastGood := time.Time{}
		if facts.Time.LastSync != nil {
			lastGood = *facts.Time.LastSync
		}
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
				// A failed poll is only news when it changes the
				// story: past the staleness window, the machine
				// stops claiming a synchronized clock.
				if facts.Time.State == machine.TimeSynchronized && time.Since(lastGood) > syncStaleAfter {
					fmt.Fprintf(os.Stderr, "liken: time: lost every source (%v); the clock is on its own\n", err)
					facts.Time.State = machine.TimeUnsynchronized
					facts.Time.Stratum = stratumUnsynchronized
					publishTimeFacts(facts)
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
			facts.Time = timeStatus(sync, clk.sources)
			publishTimeFacts(facts)
		}
	}
}

// publishTimeFacts rewrites the facts file with the current time
// story. The write is the same atomic replace every facts write is,
// so the operator sees old facts or new, never torn ones.
func publishTimeFacts(facts *machine.MachineStatus) {
	if err := machine.WriteFacts(machine.FactsPath, facts); err != nil {
		fmt.Fprintf(os.Stderr, "liken: time: writing facts: %v\n", err)
	}
}
