package main

// Serving time to the fleet.
//
// This file implements the other half of the time hierarchy: leaders
// answer the followers' SNTP queries from their own disciplined
// clocks. It is a responder, not a proxy, so it never forwards a
// query upstream. Each stratum serves the clock it keeps, and it
// advertises which reference that clock descends from. This is how
// the real NTP hierarchy works, and it makes a leader self-sufficient
// once synced: followers can boot, join, and stay disciplined with no
// internet access at all.
//
// The packet is 48 bytes long, with a fixed layout, in big-endian
// order. Like the GPT header, it is small enough to build by hand,
// and it is documented well enough (RFC 5905) to build correctly.
// The reply carries four timestamps: the client's own transmit time,
// echoed back (t1, so the client can match replies to requests); the
// time the request arrived here (t2); and the time the reply left
// (t3). The client supplies its own receipt time (t4) and does the
// offset algebra that time.go describes.
//
// Only leaders run this component, and only leaders ever receive
// queries: followers sync from the leaders. A responder on a
// follower would be a listener with no caller, and a shell-less OS
// should have no port open unless something needs to connect to it.
// This component is also the machine plane's named candidate for
// promotion to a separate process (see components.go), because it
// parses unauthenticated network input as PID 1. A fixed-size read
// in a memory-safe language is about the smallest attack surface
// that a network service can have, but it is still an attack
// surface.

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"time"
)

// The mode field is three bits of byte zero, and it states what kind
// of packet this is. A client asks with mode 3, and a server answers
// with mode 4. Every other mode belongs to NTP's symmetric and
// broadcast functions, which liken does not implement. Those packets
// get no reply.
const (
	modeClient = 3
	modeServer = 4
)

// The leap indicator is two bits of byte zero. Its main purpose is
// to announce leap seconds, but it can also carry the alarm value 3,
// "unsynchronized". This value tells a client not to trust anything
// else in the packet.
const (
	leapNone  = 0
	leapAlarm = 3
)

// ntpEpochOffset converts between the two epochs in use. NTP counts
// seconds from 1900-01-01, and Unix counts seconds from 1970-01-01.
// This constant is the number of seconds between the two epochs.
// (NTP's 32-bit seconds field wraps in 2036, the start of era 1. The
// protocol handles this rollover by convention.)
const ntpEpochOffset = 2_208_988_800

// ntpTimestamp encodes a moment in NTP's 64-bit format: 32 bits of
// seconds since 1900, then 32 bits of binary fraction. Each unit of
// the fraction is 1/2^32 of a second, about 233 picoseconds. This is
// far finer than anything this code measures.
func ntpTimestamp(t time.Time) uint64 {
	seconds := uint64(t.Unix() + ntpEpochOffset)
	fraction := uint64(t.Nanosecond()) << 32 / 1_000_000_000
	return seconds<<32 | fraction
}

// advertise derives what this machine reports on the wire about its
// clock, from the same state that status.time reports. The wire's
// vocabulary differs from status's vocabulary in one spot. A server
// that has not yet received time answers with stratum 0 and the
// "INIT" kiss code (the protocol's way of saying "do not use me, ask
// again later"), where status reports stratum 16 for the same state.
// Both values mean unsynchronized. One describes this machine, and
// the other instructs a client.
func (c *clock) advertise() (stratum, leap byte, lastSync *timeSync) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case len(c.sources) == 0:
		// This is deliberately free-running: the function serves at
		// the local-clock stratum without the alarm. A fleet with no
		// upstream time source still needs one shared reference, and
		// this machine's clock provides it.
		return stratumFreeRunning, leapNone, nil
	case c.last == nil:
		return 0, leapAlarm, nil
	default:
		return byte(c.last.stratum + 1), leapNone, c.last
	}
}

// respondTo builds the 48-byte answer to one query, or returns nil
// for anything that is not a plausible client request. Garbage input
// gets no reply, not an error. Port 123 on an open network receives
// many kinds of packets, and the cheapest response is no response.
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
	// The version is the client's own version, echoed back, so an
	// SNTPv3 client gets an SNTPv3 answer.
	response[0] = leap<<6 | version<<3 | modeServer
	response[1] = stratum
	response[2] = request[2] // the client's poll interval, echoed back by convention
	// Precision is a signed log2 value. -20 claims about 1
	// microsecond, a realistic claim for a clock reading taken from
	// the kernel.
	response[3] = byte(0x100 - 20)

	// Root delay and root dispersion ([4:8] and [8:12]) stay zero.
	// These fields accumulate path error across strata, and tracking
	// them correctly is full-NTP bookkeeping that this responder
	// does not do.

	// The reference ID names the clock that this answer descends
	// from. It holds the source's IPv4 address when the clock
	// follows a source, "LOCL" for the local clock, or, for stratum
	// 0, the kiss code: an ASCII instruction to the client.
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

	// The reference timestamp is the time when this clock last agreed
	// with its own source. A free-running clock is continuously its
	// own reference.
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

	// This is the four-timestamp exchange. The client's transmit time
	// comes back exactly as the originate timestamp (t1). The client
	// uses t1 to match replies to requests and to reject spoofed
	// packets. The timestamps t2 and t3 mark the start and end of
	// this function's handling. One clock reading serves both t2 and
	// t3, because the nanoseconds that this function takes are small
	// compared to the network's microseconds.
	copy(response[24:32], request[40:48])
	binary.BigEndian.PutUint64(response[32:40], ntpTimestamp(now))
	binary.BigEndian.PutUint64(response[40:48], ntpTimestamp(now))
	return response
}

// answerTimeQueries reads queries and writes answers until the
// context ends or the socket fails. A goroutine watches the context
// and closes the socket to unblock the read, because a blocked
// ReadFrom call has no other way to detect a cancellation.
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
			// If a reply is lost, the client must retry. UDP does not
			// guarantee delivery, and neither does this responder.
			_, _ = pc.WriteTo(response, addr)
		}
	}
}

// serveTime runs the responder as a machine-plane component: it
// binds the NTP port and answers queries without stopping. If it
// returns an error (the port is unavailable, or the socket fails),
// the machine plane's backoff logic handles the retry, instead of
// this function looping on its own.
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
