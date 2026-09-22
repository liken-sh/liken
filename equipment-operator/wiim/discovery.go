// The network discovery: it finds the LinkPlay devices on the local
// network by browsing the mDNS service they advertise and by sending an
// SSDP search, and it merges the two answers by identity.
// wiim/AGENTS.md records that both paths are link-local multicast and
// that each one misses a device, so a search repeats over a window.

package wiim

import (
	"context"
	"net"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// The multicast groups the two discovery paths reach. They are
// variables so a test points them at a loopback responder instead of
// the real multicast address.
var (
	mdnsGroup = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}
	ssdpGroup = &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 1900}
)

// queryInterval is how often each path repeats its search across the
// discovery window. Multicast drops a packet, so a search that runs
// once misses a device; the window lets the repeats fill the gap.
var queryInterval = time.Second

// The per-attempt read windows. A responder answers within a second,
// and the repeats re-send when it does not. They are variables so a
// test holds the wait short.
var (
	mdnsReadTimeout = 250 * time.Millisecond
	ssdpReadTimeout = 250 * time.Millisecond
)

// Device is one WiiM found on the local network.
type Device struct {
	UUID    string // normalized twelve-byte LinkPlay uuid, upper hex (use normalizeUUID)
	Name    string // the device's friendly name, from the mDNS instance or the SSDP description
	Address string // the IPv4 address it answers on
}

// Discover finds the LinkPlay devices on the local network. It browses
// mDNS and sends an SSDP search, repeats over the window, and merges
// the answers by UUID. It returns what it found when the window closes,
// sorted by UUID.
func Discover(ctx context.Context, window time.Duration) []Device {
	ctx, cancel := context.WithTimeout(ctx, window)
	defer cancel()

	devices := make(map[string]Device)
	for {
		browseMDNS(ctx, devices)
		searchSSDP(ctx, devices)

		select {
		case <-ctx.Done():
			return sorted(devices)
		case <-time.After(queryInterval):
		}
	}
}

// sorted orders the found devices by UUID, so a caller sees a stable
// list across runs.
func sorted(devices map[string]Device) []Device {
	result := make([]Device, 0, len(devices))
	for _, device := range devices {
		result = append(result, device)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].UUID < result[j].UUID })
	return result
}

// mergeDevice folds one found device into the set, keyed by identity.
// One path may carry a field the other does not, so a partial record
// fills in the gap and never overwrites a fuller one.
func mergeDevice(devices map[string]Device, device Device) {
	if device.UUID == "" {
		return
	}
	existing, held := devices[device.UUID]
	if !held {
		devices[device.UUID] = device
		return
	}
	if existing.Name == "" {
		existing.Name = device.Name
	}
	if existing.Address == "" {
		existing.Address = device.Address
	}
	devices[device.UUID] = existing
}

// linkplayService is the mDNS service name the LinkPlay devices browse
// and advertise.
const linkplayService = "_linkplay._tcp.local."

// mdnsInstance is the partial record for one service instance while
// the mDNS answers arrive. A responder may split the PTR, SRV, TXT, and
// A records across several packets, so discovery collects them under
// the instance name until the device is complete.
type mdnsInstance struct {
	name    string // the friendly name, the first label of the instance
	host    string // the SRV target host
	uuid    string // the uuid= value from the TXT record
	address string // the IPv4 address of the A record
}

// browseMDNS sends one mDNS PTR query for the LinkPlay service and
// reads the answers until the read window closes, merging any complete
// device into the set.
func browseMDNS(ctx context.Context, devices map[string]Device) {
	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return
	}
	defer conn.Close()

	query := mdnsQuery()
	if _, err := conn.WriteToUDP(query, mdnsGroup); err != nil {
		return
	}

	instances := make(map[string]*mdnsInstance)
	_ = conn.SetReadDeadline(time.Now().Add(mdnsReadTimeout))
	buffer := make([]byte, 1500)
	for {
		n, _, err := conn.ReadFromUDP(buffer)
		if err != nil {
			break
		}
		parseMDNS(buffer[:n], instances)
	}
	for _, device := range assembleMDNS(instances) {
		mergeDevice(devices, device)
	}
}

// mdnsQuery is one PTR question for the LinkPlay service. The
// unicast-response bit in the question class tells the responders to
// answer the socket that sent the query rather than the mDNS group, so
// a plain UDP read receives the answers.
func mdnsQuery() []byte {
	builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{})
	_ = builder.StartQuestions()
	_ = builder.Question(dnsmessage.Question{
		Name:  dnsmessage.MustNewName(linkplayService),
		Type:  dnsmessage.TypePTR,
		Class: 0x8001, // IN, with the unicast-response bit set
	})
	packet, err := builder.Finish()
	if err != nil {
		return nil
	}
	return packet
}

// parseMDNS reads one mDNS answer packet and folds its records into
// the instances map. It is a pure function, so a test builds a packet
// with dnsmessage and checks what the instance assembles to.
func parseMDNS(packet []byte, instances map[string]*mdnsInstance) {
	var message dnsmessage.Message
	if err := message.Unpack(packet); err != nil {
		return
	}
	for _, resource := range message.Answers {
		handleMDNSResource(resource, instances)
	}
	for _, resource := range message.Additionals {
		handleMDNSResource(resource, instances)
	}
}

// handleMDNSResource folds one mDNS record into the instances. The PTR
// record names the service and points at each instance; the SRV, TXT,
// and A records complete that instance across the packets that carry
// them.
func handleMDNSResource(resource dnsmessage.Resource, instances map[string]*mdnsInstance) {
	switch body := resource.Body.(type) {
	case *dnsmessage.PTRResource:
		// Only the record for the service itself points at instances.
		if resource.Header.Name.String() != linkplayService {
			return
		}
		instance := body.PTR.String()
		inst := instanceFor(instances, instance)
		if inst.name == "" {
			inst.name = firstLabel(instance)
		}
	case *dnsmessage.SRVResource:
		inst := instanceFor(instances, resource.Header.Name.String())
		inst.host = body.Target.String()
	case *dnsmessage.TXTResource:
		inst := instanceFor(instances, resource.Header.Name.String())
		for _, text := range body.TXT {
			if strings.HasPrefix(text, "uuid=") {
				inst.uuid = strings.TrimPrefix(text, "uuid=")
			}
		}
	case *dnsmessage.AResource:
		// The A record names a host, and the SRV records point at it,
		// so every instance whose target is this host takes the address.
		address := net.IP(body.A[:]).String()
		for _, inst := range instances {
			if inst.host == resource.Header.Name.String() {
				inst.address = address
			}
		}
	}
}

// instanceFor returns the partial record for one instance name,
// creating it when this is the first record to mention the name.
func instanceFor(instances map[string]*mdnsInstance, name string) *mdnsInstance {
	inst, held := instances[name]
	if !held {
		inst = &mdnsInstance{}
		instances[name] = inst
	}
	return inst
}

// firstLabel is the first label of an mDNS name, the friendly name the
// device advertised. dnsmessage spells a space in a label as \032 and a
// dot as \., so the escapes are turned back before the name is used.
func firstLabel(name string) string {
	label := name
	if index := strings.Index(name, "."); index >= 0 {
		label = name[:index]
	}
	label = strings.ReplaceAll(label, `\032`, " ")
	return strings.ReplaceAll(label, `\.`, ".")
}

// assembleMDNS builds a device for every instance that has both a UUID
// and an address. An instance without one of them is not a device yet,
// because the identity is what ties the two paths together.
func assembleMDNS(instances map[string]*mdnsInstance) []Device {
	devices := make([]Device, 0, len(instances))
	for _, inst := range instances {
		uuid := normalizeUUID(inst.uuid)
		if uuid == "" || inst.address == "" {
			continue
		}
		devices = append(devices, Device{
			UUID:    uuid,
			Name:    inst.name,
			Address: inst.address,
		})
	}
	return devices
}

// ssdpRequest is the M-SEARCH a device answers. The ST names the
// MediaRenderer service, which every LinkPlay device carries.
var ssdpRequest = []byte("M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 2\r\nST: urn:schemas-upnp-org:device:MediaRenderer:1\r\n\r\n")

// searchSSDP sends one M-SEARCH and reads the answers until the read
// window closes, merging every device it can parse into the set.
func searchSSDP(ctx context.Context, devices map[string]Device) {
	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return
	}
	defer conn.Close()

	if _, err := conn.WriteTo(ssdpRequest, ssdpGroup); err != nil {
		return
	}

	_ = conn.SetReadDeadline(time.Now().Add(ssdpReadTimeout))
	buffer := make([]byte, 2048)
	for {
		n, _, err := conn.ReadFrom(buffer)
		if err != nil {
			return
		}
		if device, ok := parseSSDP(buffer[:n]); ok {
			mergeDevice(devices, device)
		}
	}
}

// parseSSDP reads one M-SEARCH answer into a device. The response
// carries the identity in the USN header and the address in the
// LOCATION header. It carries no name, so Name stays empty and the
// merge fills it from the mDNS path.
func parseSSDP(response []byte) (Device, bool) {
	headers := httpHeaders(response)
	uuid := uuidFromUSN(headers["usn"])
	address := hostFromLocation(headers["location"])
	if uuid == "" || address == "" {
		return Device{}, false
	}
	return Device{UUID: normalizeUUID(uuid), Address: address}, true
}

// httpHeaders reads the header block of an HTTP response into a map
// keyed by the lowercased field name, so the casing a device uses does
// not matter.
func httpHeaders(response []byte) map[string]string {
	headers := make(map[string]string)
	for _, line := range strings.Split(string(response), "\n") {
		line = strings.TrimSpace(line)
		if index := strings.Index(line, ":"); index > 0 {
			key := strings.ToLower(strings.TrimSpace(line[:index]))
			value := strings.TrimSpace(line[index+1:])
			headers[key] = value
		}
	}
	return headers
}

// uuidFromUSN pulls the identity out of an SSDP USN header, which
// spells it "uuid:<value>" and may follow it with "::<service>". The
// value may be written with dashes or in either case.
func uuidFromUSN(usn string) string {
	lower := strings.ToLower(usn)
	index := strings.Index(lower, "uuid:")
	if index < 0 {
		return ""
	}
	value := usn[index+len("uuid:"):]
	if cut := strings.Index(value, ":"); cut >= 0 {
		value = value[:cut]
	}
	return value
}

// hostFromLocation pulls the host out of an SSDP LOCATION header, which
// spells the device's address as "http://<host>:<port>/<path>". The
// port is dropped because the control API has its own.
func hostFromLocation(location string) string {
	rest := strings.TrimSpace(location)
	if index := strings.Index(rest, "://"); index >= 0 {
		rest = rest[index+3:]
	}
	if index := strings.IndexAny(rest, "/"); index >= 0 {
		rest = rest[:index]
	}
	if host, _, err := net.SplitHostPort(rest); err == nil {
		return host
	}
	return rest
}
