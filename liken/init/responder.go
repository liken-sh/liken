package main

// Each leader runs an SNTP server, so followers sync their clocks from
// the leaders. A leader replies from its own clock and
// never forwards a query upstream. So once a leader has synced,
// followers can boot, join, and keep their clocks correct with no
// internet access.
//
// An NTP packet is 48 bytes in a fixed big-endian layout (RFC 5905),
// so the responder builds it by hand. The reply has three of the four
// timestamps of an exchange: the client's transmit time echoed back as
// t1, the time the request arrived as t2, and the time the reply left
// as t3. The client records t4 when the reply arrives, and computes
// the offset as time.go describes.
//
// The responder parses unauthenticated UDP as PID 1, which is a reason
// that components.go gives for moving a component into a child
// process. It still runs as a goroutine.

import (
	"context"
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"net"
	"time"
)

// The mode is the low three bits of byte zero. A client sends mode 3
// and a server replies with mode 4. The responder does not reply to
// the other modes, which are NTP's symmetric and broadcast modes.
const (
	modeClient = 3
	modeServer = 4
)

// The leap indicator is the top two bits of byte zero. The value 3
// means the server is unsynchronized, and a client must not use its
// time.
const (
	leapNone  = 0
	leapAlarm = 3
)

// ntpEpochOffset is the number of seconds from the NTP epoch,
// 1900-01-01, to the Unix epoch, 1970-01-01. NTP's 32-bit seconds
// field wraps in 2036, and clients handle the wrap by convention.
const ntpEpochOffset = 2_208_988_800

// ntpTimestamp encodes a time in NTP's 64-bit format: 32 bits of
// seconds since 1900, then the fraction of a second in units of 1/2^32.
func ntpTimestamp(t time.Time) uint64 {
	seconds := uint64(t.Unix() + ntpEpochOffset)
	fraction := uint64(t.Nanosecond()) << 32 / 1_000_000_000
	return seconds<<32 | fraction
}

// advertise returns the stratum, leap indicator, and last sync to put
// in a reply. Before the first sync from a source, the reply has
// stratum 0 and the kiss code "INIT", which tells a client to ask
// again later. status.time reports stratum 16 for the same state.
func (c *clock) advertise() (stratum, leap byte, lastSync *timeSync) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case len(c.sources) == 0:
		// With no sources, the leader serves its own clock as a valid
		// time, so the machines of the fleet still agree with each
		// other.
		return stratumFreeRunning, leapNone, nil
	case c.last == nil:
		return 0, leapAlarm, nil
	default:
		return byte(c.last.stratum + 1), leapNone, c.last
	}
}

// respondTo builds the reply to one query. It returns nil for a packet
// that is not a client request of NTP version 1 to 4, so the responder
// sends nothing back.
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
	// Byte zero is leap(2) | version(3) | mode(3). The reply echoes the
	// client's version, so an SNTPv3 client gets an SNTPv3 reply.
	response[0] = leap<<6 | version<<3 | modeServer
	response[1] = stratum
	response[2] = request[2] // the client's poll interval, echoed by convention
	// Precision is a signed log2 of seconds. -20 is about 1
	// microsecond.
	response[3] = byte(0x100 - 20)

	// Root delay and root dispersion ([4:8] and [8:12]) stay zero,
	// because the responder does not track the error that they add up
	// across strata.

	// The reference ID is the kiss code at stratum 0, and "LOCL" for a
	// free-running clock. For a synced clock, RFC 5905 section 7.3 sets
	// it to the IPv4 address that answered, or to the first four bytes
	// of the MD5 hash of the IPv6 address, so a client can detect a
	// timing loop. A measurement with no answering address falls back
	// to the source as configured, and a hostname there leaves zero.
	switch {
	case leap == leapAlarm:
		copy(response[12:16], "INIT")
	case lastSync == nil:
		copy(response[12:16], "LOCL")
	default:
		ip := lastSync.address
		if ip == nil {
			ip = net.ParseIP(lastSync.source)
		}
		switch {
		case ip == nil:
		case ip.To4() != nil:
			copy(response[12:16], ip.To4())
		default:
			hash := md5.Sum(ip.To16())
			copy(response[12:16], hash[:4])
		}
	}

	// The reference timestamp is the time of the last sync. A
	// free-running clock uses the current time, and an unsynchronized
	// one leaves it zero.
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

	// The client's transmit time goes back unchanged as t1, so the
	// client can match the reply to its request and reject a spoofed
	// one. One clock reading is both t2 and t3, because building the
	// reply takes much less time than the network round trip.
	copy(response[24:32], request[40:48])
	binary.BigEndian.PutUint64(response[32:40], ntpTimestamp(now))
	binary.BigEndian.PutUint64(response[40:48], ntpTimestamp(now))
	return response
}

// answerTimeQueries replies to queries until the context ends or the
// socket fails. A goroutine closes the socket when the context ends,
// because closing it is what returns a blocked ReadFrom.
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
			// A client retries when a reply is lost, so a failed write
			// is ignored.
			_, _ = pc.WriteTo(response, addr)
		}
	}
}

// serveTime returns the responder as a machine-plane component. When
// the port cannot be bound or the socket fails, it returns the error,
// and the machine plane restarts it.
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
