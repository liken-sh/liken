// The discovery parsers and the merge, plus the SSDP and mDNS send and
// read paths against loopback responders. Multicast needs a real LAN
// and a real device, so the tests point the group addresses at a
// loopback UDP socket and have a responder answer with canned packets.

package wiim

import (
	"context"
	"net"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// useShortReads holds the per-attempt read windows short, so a test
// that waits on a silent loopback socket finishes fast.
func useShortReads(t *testing.T) {
	t.Helper()
	origMDNS, origSSDP := mdnsReadTimeout, ssdpReadTimeout
	mdnsReadTimeout, ssdpReadTimeout = 100*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() { mdnsReadTimeout, ssdpReadTimeout = origMDNS, origSSDP })
}

// mdnsDevicePacket builds one mDNS response for an instance named
// "WiiM Amp". includeA decides whether the A record joins the PTR, SRV,
// and TXT records, so a test can split the address into its own packet.
func mdnsDevicePacket(t *testing.T, includeA bool) []byte {
	t.Helper()
	builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true})
	if err := builder.StartAnswers(); err != nil {
		t.Fatal(err)
	}
	header := func(name string) dnsmessage.ResourceHeader {
		return dnsmessage.ResourceHeader{
			Name:  dnsmessage.MustNewName(name),
			Class: dnsmessage.ClassINET,
			TTL:   120,
		}
	}
	if err := builder.PTRResource(
		header(linkplayService),
		dnsmessage.PTRResource{PTR: dnsmessage.MustNewName("WiiM Amp._linkplay._tcp.local.")},
	); err != nil {
		t.Fatal(err)
	}
	if err := builder.SRVResource(
		header("WiiM Amp._linkplay._tcp.local."),
		dnsmessage.SRVResource{Port: 49152, Target: dnsmessage.MustNewName("WiiMAmp.local.")},
	); err != nil {
		t.Fatal(err)
	}
	if err := builder.TXTResource(
		header("WiiM Amp._linkplay._tcp.local."),
		dnsmessage.TXTResource{TXT: []string{"uuid=ff98f2f7-aabb-ccdd-eeff-0011ff98f2f7"}},
	); err != nil {
		t.Fatal(err)
	}
	if includeA {
		if err := builder.AResource(
			header("WiiMAmp.local."),
			dnsmessage.AResource{A: [4]byte{192, 0, 2, 10}},
		); err != nil {
			t.Fatal(err)
		}
	}
	packet, err := builder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return packet
}

// mdnsARecordPacket is a second packet that carries only the A record
// for the same host, the shape a responder uses when it splits the
// answer across several packets.
func mdnsARecordPacket(t *testing.T) []byte {
	t.Helper()
	builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true})
	if err := builder.StartAnswers(); err != nil {
		t.Fatal(err)
	}
	if err := builder.AResource(
		dnsmessage.ResourceHeader{
			Name:  dnsmessage.MustNewName("WiiMAmp.local."),
			Class: dnsmessage.ClassINET,
			TTL:   120,
		},
		dnsmessage.AResource{A: [4]byte{192, 0, 2, 10}},
	); err != nil {
		t.Fatal(err)
	}
	packet, err := builder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return packet
}

// The mDNS parser folds a packet with the PTR, SRV, TXT, and A records
// into one device.
func TestParseMDNSBuildsADevice(t *testing.T) {
	instances := map[string]*mdnsInstance{}
	parseMDNS(mdnsDevicePacket(t, true), instances)
	devices := assembleMDNS(instances)

	mustMatch(t, len(devices), 1)
	mustMatch(t, devices[0].UUID, "FF98F2F7AABBCCDDEEFF0011")
	mustMatch(t, devices[0].Name, "WiiM Amp")
	mustMatch(t, devices[0].Address, "192.0.2.10")
}

// The mDNS parser assembles an instance whose records arrive in two
// packets: the identity and the name first, the address later.
func TestParseMDNSAssemblesAcrossPackets(t *testing.T) {
	instances := map[string]*mdnsInstance{}
	parseMDNS(mdnsDevicePacket(t, false), instances)
	mustMatch(t, assembleMDNS(instances), []Device{})

	parseMDNS(mdnsARecordPacket(t), instances)
	devices := assembleMDNS(instances)
	mustMatch(t, len(devices), 1)
	mustMatch(t, devices[0].UUID, "FF98F2F7AABBCCDDEEFF0011")
	mustMatch(t, devices[0].Address, "192.0.2.10")
}

// A packet that is not a DNS message folds nothing and cannot panic.
func TestParseMDNSIgnoresGarbage(t *testing.T) {
	instances := map[string]*mdnsInstance{}
	parseMDNS([]byte("not a dns packet"), instances)
	mustMatch(t, assembleMDNS(instances), []Device{})
}

// The SSDP parser reads the identity and the address from a response
// whose header casing and LOCATION path vary. The name is not in the
// response, so it stays empty.
func TestParseSSDPReadsEitherHeaderCasing(t *testing.T) {
	response := []byte("HTTP/1.1 200 OK\r\n" +
		"location: http://192.0.2.10:49152/device.xml\r\n" +
		"usn: uuid:FF98F2F7-AABB-CCDD-EEFF-0011FF98F2F7::urn:schemas-upnp-org:device:MediaRenderer:1\r\n\r\n")
	device, ok := parseSSDP(response)

	mustMatch(t, ok, true)
	mustMatch(t, device.UUID, "FF98F2F7AABBCCDDEEFF0011")
	mustMatch(t, device.Address, "192.0.2.10")
	mustMatch(t, device.Name, "")
}

// A response without an identity or without an address is not a device.
func TestParseSSDPRejectsWithoutIdentityOrAddress(t *testing.T) {
	if _, ok := parseSSDP([]byte("HTTP/1.1 200 OK\r\nLOCATION: http://192.0.2.10/x\r\n\r\n")); ok {
		t.Fatal("a response without a USN parsed")
	}
	if _, ok := parseSSDP([]byte("HTTP/1.1 200 OK\r\nUSN: uuid:FF98F2F7AABBCCDDEEFF0011\r\n\r\n")); ok {
		t.Fatal("a response without a LOCATION parsed")
	}
}

// The merge fills a missing name or address from whichever source
// carries it, and keeps one device per identity.
func TestMergeFillsMissingFields(t *testing.T) {
	devices := map[string]Device{}
	// SSDP finds the identity and the address, but no name.
	mergeDevice(devices, Device{UUID: "FF98F2F7AABBCCDDEEFF0011", Address: "192.0.2.11"})
	// mDNS finds the same identity with a name.
	mergeDevice(devices, Device{UUID: "FF98F2F7AABBCCDDEEFF0011", Name: "Amp", Address: "192.0.2.10"})

	got := sorted(devices)
	mustMatch(t, len(got), 1)
	mustMatch(t, got[0].Name, "Amp")
	mustMatch(t, got[0].Address, "192.0.2.11")
}

// A record with no identity never enters the set.
func TestMergeIgnoresAnEmptyIdentity(t *testing.T) {
	devices := map[string]Device{}
	mergeDevice(devices, Device{Name: "Amp", Address: "192.0.2.10"})
	mustMatch(t, devices, map[string]Device{})
}

// The sort orders devices by identity, not by arrival.
func TestSortedOrdersByUUID(t *testing.T) {
	got := sorted(map[string]Device{
		"BBB": {UUID: "BBB"},
		"AAA": {UUID: "AAA"},
		"CCC": {UUID: "CCC"},
	})
	mustMatch(t, []string{got[0].UUID, got[1].UUID, got[2].UUID}, []string{"AAA", "BBB", "CCC"})
}

// The SSDP send and read path works against a loopback responder: the
// responder learns its own address, the test points the SSDP group at
// it, and it answers the M-SEARCH with a canned response.
func TestSearchSSDPReadsALoopbackResponder(t *testing.T) {
	useShortReads(t)
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	orig := ssdpGroup
	ssdpGroup = conn.LocalAddr().(*net.UDPAddr)
	t.Cleanup(func() { ssdpGroup = orig })

	response := []byte("HTTP/1.1 200 OK\r\n" +
		"LOCATION: http://192.0.2.10:49152/device.xml\r\n" +
		"USN: uuid:ff98f2f7-aabb-ccdd-eeff-0011ff98f2f7::urn:schemas-upnp-org:device:MediaRenderer:1\r\n\r\n")
	go func() {
		buffer := make([]byte, 2048)
		_, addr, _ := conn.ReadFrom(buffer)
		_, _ = conn.WriteTo(response, addr)
	}()

	devices := map[string]Device{}
	searchSSDP(context.Background(), devices)
	got := sorted(devices)
	mustMatch(t, len(got), 1)
	mustMatch(t, got[0].UUID, "FF98F2F7AABBCCDDEEFF0011")
	mustMatch(t, got[0].Address, "192.0.2.10")
}

// The mDNS send and read path works against a loopback responder the
// same way, and the query it sends is a valid DNS message.
func TestBrowseMDNSReadsALoopbackResponder(t *testing.T) {
	useShortReads(t)
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	orig := mdnsGroup
	mdnsGroup = conn.LocalAddr().(*net.UDPAddr)
	t.Cleanup(func() { mdnsGroup = orig })

	packet := mdnsDevicePacket(t, true)
	go func() {
		buffer := make([]byte, 1500)
		_, addr, _ := conn.ReadFromUDP(buffer)
		_, _ = conn.WriteToUDP(packet, addr)
	}()

	devices := map[string]Device{}
	browseMDNS(context.Background(), devices)
	got := sorted(devices)
	mustMatch(t, len(got), 1)
	mustMatch(t, got[0].UUID, "FF98F2F7AABBCCDDEEFF0011")
	mustMatch(t, got[0].Name, "WiiM Amp")
	mustMatch(t, got[0].Address, "192.0.2.10")

	// The query the browse sends is a PTR question for the LinkPlay
	// service with the unicast-response bit set.
	var message dnsmessage.Message
	if err := message.Unpack(mdnsQuery()); err != nil {
		t.Fatal(err)
	}
	mustMatch(t, len(message.Questions), 1)
	mustMatch(t, message.Questions[0].Name.String(), linkplayService)
	mustMatch(t, message.Questions[0].Type, dnsmessage.TypePTR)
	mustMatch(t, message.Questions[0].Class, dnsmessage.Class(0x8001))
}

// Discover returns nothing when nothing answers. Both paths are pointed
// at a loopback address with no listener, so the searches send into the
// void, time out, and never touch a real device.
func TestDiscoverReturnsEmptyWhenNothingAnswers(t *testing.T) {
	useShortReads(t)
	origMDNS, origSSDP := mdnsGroup, ssdpGroup
	mdnsGroup = &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9}
	ssdpGroup = &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9}
	t.Cleanup(func() { mdnsGroup, ssdpGroup = origMDNS, origSSDP })
	origInterval := queryInterval
	queryInterval = 5 * time.Millisecond
	t.Cleanup(func() { queryInterval = origInterval })

	got := Discover(context.Background(), 20*time.Millisecond)
	mustMatch(t, got, []Device{})
}
