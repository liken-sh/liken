package main

// Serving time to the fleet.
//
// This is the other half of the time hierarchy: leaders answer the
// followers' SNTP queries from their own disciplined clocks. A
// *responder*, not a proxy — no query is ever forwarded upstream.
// Each stratum serves the clock it keeps and advertises where its
// pedigree came from, which is how the real NTP hierarchy works and
// what makes a leader self-sufficient once synced: followers can
// boot, join, and stay disciplined with no internet access at all.
//
// The packet is 48 bytes, fixed layout, big-endian — the same genre
// as the GPT header: small enough to build by hand, documented
// enough (RFC 5905) to build honestly. The reply carries four
// timestamps: the client's own transmit time echoed back (t1, so it
// can match replies to requests), when the request arrived here
// (t2), and when the reply left (t3); the client supplies its
// receipt (t4) and does the offset algebra time.go describes.
//
// Only leaders run this component, and only leaders are ever asked:
// followers sync from the leaders, so a responder on a follower
// would be a listener with no caller — and a shell-less OS should
// have no port open without someone who's supposed to knock. This
// is also the machine plane's named candidate for promotion to a
// separate process (components.go): it parses unauthenticated
// network input as PID 1, and although a fixed-size read in a
// memory-safe language is about the smallest attack surface a
// network service can have, "small" is not "none". The hardening
// pass owns that promotion.

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"time"
)

// The mode field, three bits of byte zero: who is speaking and why.
// A client asks with mode 3; a server answers with mode 4. Every
// other mode belongs to NTP's symmetric and broadcast machinery,
// which liken doesn't speak — those packets get silence.
const (
	modeClient = 3
	modeServer = 4
)

// The leap indicator, two bits of byte zero. Its day job is
// announcing leap seconds; its second job is the alarm value 3,
// "unsynchronized", which tells a client to distrust everything
// else in the packet.
const (
	leapNone  = 0
	leapAlarm = 3
)

// ntpEpochOffset converts between the two epochs in play: NTP counts
// seconds from 1900-01-01, Unix from 1970-01-01, and these are the
// seconds between them. (NTP's 32-bit seconds field wraps in 2036 —
// era 1 — which the protocol handles by convention and this code
// will outlive by being replaced long before it matters.)
const ntpEpochOffset = 2_208_988_800

// ntpTimestamp encodes a moment in NTP's 64-bit format: 32 bits of
// seconds since 1900, 32 bits of binary fraction — each unit is
// 1/2^32 of a second, about 233 picoseconds, absurdly finer than
// anything here measures.
func ntpTimestamp(t time.Time) uint64 {
	seconds := uint64(t.Unix() + ntpEpochOffset)
	fraction := uint64(t.Nanosecond()) << 32 / 1_000_000_000
	return seconds<<32 | fraction
}

// advertise is what this machine tells the wire about its clock,
// derived from the same state status.time reports — but in the
// wire's vocabulary, which differs from status's in one spot: a
// server that wants time and hasn't gotten it yet answers stratum 0
// with the "INIT" kiss code (the protocol's "don't use me, ask
// later"), where status says stratum 16. Both mean unsynchronized;
// one is a self-description, the other an instruction to a client.
func (c *clock) advertise() (stratum, leap byte, lastSync *timeSync) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case len(c.sources) == 0:
		// Free-running on purpose: serve confidently at the
		// local-clock stratum. A fleet with no upstreams still wants
		// one truth, and this machine's clock is it.
		return stratumFreeRunning, leapNone, nil
	case c.last == nil:
		return 0, leapAlarm, nil
	default:
		return byte(c.last.stratum + 1), leapNone, c.last
	}
}

// respondTo builds the 48-byte answer to one query, or nil for
// anything that isn't a plausible client request. Garbage gets
// silence rather than an error: port 123 on an open network hears
// all sorts of things, and the cheapest response is none.
func respondTo(request []byte, clk *clock, now time.Time) []byte {
	if len(request) < 48 {
		return nil
	}
	version := request[0] >> 3 & 0x07
	mode := request[0] & 0x07
	if mode != modeClient || version < 1 || version > 4 {
		return nil
	}

	stratum, leap, lastSync := clk.advertise()

	response := make([]byte, 48)
	// Byte zero packs three fields: leap(2) | version(3) | mode(3).
	// The version is the client's own, echoed back: an SNTPv3 client
	// deserves an SNTPv3 answer.
	response[0] = leap<<6 | version<<3 | modeServer
	response[1] = stratum
	response[2] = request[2] // the client's poll interval, echoed by convention
	// Precision is a signed log2: -20 claims ~1µs, honest for a
	// clock read from the kernel.
	response[3] = byte(0x100 - 20)

	// Root delay and root dispersion ([4:8] and [8:12]) stay zero:
	// they accumulate path error across strata, and tracking them
	// honestly is full-NTP bookkeeping this responder doesn't do.

	// The reference ID names the clock this answer descends from:
	// the source's IPv4 address when following one, "LOCL" for the
	// local clock, or — for stratum 0 — the kiss code, the ASCII
	// instruction to the client.
	switch {
	case leap == leapAlarm:
		copy(response[12:16], "INIT")
	case lastSync == nil:
		copy(response[12:16], "LOCL")
	default:
		if ip := net.ParseIP(lastSync.source); ip != nil && ip.To4() != nil {
			copy(response[12:16], ip.To4())
		}
	}

	// The reference timestamp is when this clock last agreed with
	// its own source; a free-running clock is continuously its own
	// reference.
	reference := now
	if lastSync != nil {
		reference = lastSync.at
	}
	if leap == leapAlarm {
		reference = time.Time{}
	}
	if !reference.IsZero() {
		binary.BigEndian.PutUint64(response[16:24], ntpTimestamp(reference))
	}

	// The four-timestamp exchange: the client's transmit time comes
	// back verbatim as the originate timestamp (t1 — how it matches
	// replies to requests and rejects spoofed strays), and t2/t3
	// bracket our handling. One clock reading serves both: the
	// nanoseconds this function takes are noise next to the
	// network's microseconds.
	copy(response[24:32], request[40:48])
	binary.BigEndian.PutUint64(response[32:40], ntpTimestamp(now))
	binary.BigEndian.PutUint64(response[40:48], ntpTimestamp(now))
	return response
}

// answerTimeQueries reads queries and writes answers until the
// context ends or the socket dies. The context's goroutine closes
// the socket to unblock the read — a blocked ReadFrom can't hear a
// cancellation any other way.
func answerTimeQueries(ctx context.Context, pc net.PacketConn, clk *clock) error {
	go func() {
		<-ctx.Done()
		pc.Close()
	}()
	buffer := make([]byte, 64)
	for {
		n, addr, err := pc.ReadFrom(buffer)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("reading a time query: %w", err)
		}
		if response := respondTo(buffer[:n], clk, time.Now()); response != nil {
			// A lost reply is the client's problem to retry; UDP
			// makes no promises and neither do we.
			_, _ = pc.WriteTo(response, addr)
		}
	}
}

// serveTime is the responder as a machine-plane component: bind the
// NTP port, answer forever. An error return (the port unavailable, a
// dead socket) hands the retry to the plane's backoff rather than
// looping here.
func serveTime(clk *clock) func(context.Context) error {
	return func(ctx context.Context) error {
		pc, err := net.ListenPacket("udp", ":123")
		if err != nil {
			return fmt.Errorf("binding the NTP port: %w", err)
		}
		fmt.Println("liken: time: serving SNTP on port 123")
		return answerTimeQueries(ctx, pc, clk)
	}
}
