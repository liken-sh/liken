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
// discovery. Servers ask the upstreams declared on the Cluster;
// agents ask the cluster's endpoint (the same address they join k3s
// through), which a server answers from its own disciplined clock
// (responder.go's story). A cluster with no upstreams free-runs:
// internally consistent, correct only if the hardware clocks happen
// to be, and status says so honestly.
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
	"net/url"
	"os"
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

// timeSources derives where this machine gets its time, the same way
// it derives everything: from declared inputs, by role. Servers ask
// the Cluster's upstreams; agents ask the endpoint they join k3s
// through, so "who has the time?" and "where is my cluster?" are one
// answer. nil means free-running — for a server, because no
// upstreams were declared; for an agent, only when the endpoint is
// missing or unparseable, which an agent boot has already refused to
// run with (k3s.go).
func timeSources(cluster *machine.Cluster, role string) []string {
	if cluster == nil {
		return nil
	}
	if role == machine.RoleServer {
		return cluster.Spec.Time.Upstreams
	}
	endpoint, err := url.Parse(cluster.Spec.Endpoint)
	if err != nil || endpoint.Hostname() == "" {
		return nil
	}
	return []string{endpoint.Hostname()}
}

// timeStatus is the clock's story as status: the same facts the
// console prints, made queryable. A machine with sources that hasn't
// synced yet is unsynchronized (16); a machine with no sources at
// all is free-running on purpose (10); neither claims synchronized,
// because that word is reserved for a clock currently following a
// source that is itself synchronized.
func timeStatus(sync *timeSync, sources []string) machine.TimeStatus {
	if sync == nil {
		stratum := stratumUnsynchronized
		if len(sources) == 0 {
			stratum = stratumFreeRunning
		}
		return machine.TimeStatus{Stratum: stratum}
	}
	at := sync.at
	return machine.TimeStatus{
		Synchronized: true,
		Source:       sync.source,
		Stratum:      sync.stratum + 1,
		Offset:       sync.offset.Round(10 * time.Microsecond).String(),
		LastSync:     &at,
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

// disciplineClock builds the machine plane's time component: measure,
// slew, publish, sleep, forever. It owns facts.Time — and, from the
// moment it starts, the facts file — as the only writer, so no lock.
// It narrates transitions rather than every poll: a sync gained, a
// sync lost, and an offset only when it exceeds the step threshold,
// which means drift is outrunning the slew.
func disciplineClock(sources []string, facts *machine.MachineStatus) func(context.Context) error {
	return func(ctx context.Context) error {
		lastGood := time.Time{}
		if facts.Time.LastSync != nil {
			lastGood = *facts.Time.LastSync
		}
		for {
			if !sleepUnlessCancelled(ctx, timePollInterval) {
				return nil
			}
			sync, err := querySources(sources)
			if err != nil {
				// A failed poll is only news when it changes the
				// story: past the staleness window, the machine
				// stops claiming a synchronized clock.
				if facts.Time.Synchronized && time.Since(lastGood) > syncStaleAfter {
					fmt.Fprintf(os.Stderr, "liken: time: lost every source (%v); the clock is on its own\n", err)
					facts.Time.Synchronized = false
					facts.Time.Stratum = stratumUnsynchronized
					publishTimeFacts(facts)
				}
				continue
			}

			if err := slewClock(sync.offset); err != nil {
				fmt.Fprintf(os.Stderr, "liken: time: slewing the clock: %v\n", err)
			}
			if !facts.Time.Synchronized {
				fmt.Printf("liken: time: synchronized to %s (stratum %d), offset %s\n",
					sync.source, sync.stratum, sync.offset.Round(10*time.Microsecond))
			} else if sync.offset.Abs() >= stepThreshold {
				fmt.Printf("liken: time: offset %s from %s exceeds the slew's pace; correcting over several polls\n",
					sync.offset.Round(time.Millisecond), sync.source)
			}
			lastGood = sync.at
			facts.Time = timeStatus(sync, sources)
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
