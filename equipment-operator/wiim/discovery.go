// Discovery of LinkPlay devices on the local network. It browses the
// mDNS service the devices advertise, sends an SSDP search, and merges
// the results by UUID. Both are link-local multicast and either can
// miss a device, so Discover repeats the searches over a time window.

package wiim

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// The multicast groups for mDNS and SSDP.
var (
	mdnsGroup = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}
	ssdpGroup = &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 1900}
)

// listenPacket opens the socket a search sends from and reads its
// responses on. A test replaces it to answer searches in memory.
var listenPacket = net.ListenPacket

// queryInterval is the wait between rounds of searches inside the
// discovery window. Multicast packets get lost, so one search can miss
// a device that a later one finds.
const queryInterval = time.Second

// ssdpMX is the MX header of the M-SEARCH: the longest, in seconds, that
// a renderer may delay its response. Each renderer picks a random delay
// up to this value. One second is the smallest value UPnP allows, and
// it is enough for the few renderers on a home network.
const ssdpMX = 1

// How long one search reads responses. RFC 6762 has an mDNS responder
// delay its answer to a query for a shared record, such as this PTR
// query, by 20 to 120 ms. An SSDP renderer can answer at any time up to
// ssdpMX, so that read waits that long plus a margin for the network.
const (
	mdnsReadTimeout = 250 * time.Millisecond
	ssdpReadTimeout = ssdpMX*time.Second + 250*time.Millisecond
)

// Device is one LinkPlay device found on the local network. It may not
// be a WiiM, because other brands use LinkPlay too. The UPnP description
// at Description names the model.
type Device struct {
	UUID    string // the first twelve bytes of the LinkPlay uuid in upper-case hex, from normalizeUUID
	Name    string // the friendly name, from the mDNS instance name
	Address string // the IPv4 address
	// Description is the URL of the device's UPnP description over
	// plain HTTP. mDNS gives only its port, in the SRV record, and SSDP
	// gives the whole URL in the LOCATION header. It is empty when
	// neither search returned it.
	Description string
}

// Discover browses mDNS and sends SSDP searches repeatedly for the
// length of window, and returns the devices found, merged by UUID and
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

// sorted returns the devices ordered by UUID, so the list is stable
// from one run to the next.
func sorted(devices map[string]Device) []Device {
	result := make([]Device, 0, len(devices))
	for _, device := range devices {
		result = append(result, device)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].UUID < result[j].UUID })
	return result
}

// mergeDevice adds a device to the set, keyed by UUID. mDNS and SSDP
// each return fields the other does not, so a field that is already set
// is kept and an empty one is filled from the new record.
func mergeDevice(devices map[string]Device, device Device) {
	if device.UUID == "" {
		return
	}
	existing, found := devices[device.UUID]
	if !found {
		devices[device.UUID] = device
		return
	}
	if existing.Name == "" {
		existing.Name = device.Name
	}
	if existing.Address == "" {
		existing.Address = device.Address
	}
	if existing.Description == "" {
		existing.Description = device.Description
	}
	devices[device.UUID] = existing
}

// linkplayService is the mDNS service type that LinkPlay devices
// advertise.
const linkplayService = "_linkplay._tcp.local."

// mdnsInstance collects the records of one mDNS service instance. A
// responder can spread the PTR, SRV, TXT, and A records over several
// packets, so they are gathered by instance name until the device is
// complete.
type mdnsInstance struct {
	name    string // the first label of the instance name
	host    string // the SRV target host
	port    uint16 // the SRV port, where the device serves its UPnP description
	uuid    string // the uuid= value of the TXT record
	address string // the IPv4 address of the A record
}

// browseMDNS sends one PTR query for the LinkPlay service, reads
// responses until mdnsReadTimeout passes, and merges each complete
// device into devices.
func browseMDNS(ctx context.Context, devices map[string]Device) {
	conn, err := listenPacket("udp4", ":0")
	if err != nil {
		return
	}
	defer conn.Close()

	query := mdnsQuery()
	if _, err := conn.WriteTo(query, mdnsGroup); err != nil {
		return
	}

	instances := make(map[string]*mdnsInstance)
	_ = conn.SetReadDeadline(time.Now().Add(mdnsReadTimeout))
	buffer := make([]byte, 1500)
	for {
		n, _, err := conn.ReadFrom(buffer)
		if err != nil {
			break
		}
		parseMDNS(buffer[:n], instances)
	}
	for _, device := range assembleMDNS(instances) {
		mergeDevice(devices, device)
	}
}

// mdnsQuery builds a PTR query for the LinkPlay service. It sets the
// unicast-response bit in the question class, which asks responders to
// reply to the sending socket instead of the multicast group. RFC 6762
// lets a responder reply on the group anyway, and the amps did, so the
// browse socket receives the responses either way.
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

// parseMDNS adds the answer and additional records of one mDNS packet
// to instances. It ignores a packet that is not a DNS message.
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

// handleMDNSResource adds one record to instances. The PTR record lists
// an instance of the service. The SRV, TXT, and A records fill in that
// instance's host, port, uuid, and address.
func handleMDNSResource(resource dnsmessage.Resource, instances map[string]*mdnsInstance) {
	switch body := resource.Body.(type) {
	case *dnsmessage.PTRResource:
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
		inst.port = body.Port
	case *dnsmessage.TXTResource:
		inst := instanceFor(instances, resource.Header.Name.String())
		for _, text := range body.TXT {
			if strings.HasPrefix(text, "uuid=") {
				inst.uuid = strings.TrimPrefix(text, "uuid=")
			}
		}
	case *dnsmessage.AResource:
		// An A record belongs to a host, not to an instance, so it
		// applies to every instance whose SRV target is that host.
		address := net.IP(body.A[:]).String()
		for _, inst := range instances {
			if inst.host == resource.Header.Name.String() {
				inst.address = address
			}
		}
	}
}

// instanceFor returns the record for an instance name, and creates it
// when it does not exist.
func instanceFor(instances map[string]*mdnsInstance, name string) *mdnsInstance {
	inst, found := instances[name]
	if !found {
		inst = &mdnsInstance{}
		instances[name] = inst
	}
	return inst
}

// firstLabel returns the first label of an mDNS name, which is the
// device's friendly name. dnsmessage escapes a space in a label as \032
// and a dot as \., so firstLabel converts them back.
func firstLabel(name string) string {
	label := name
	if index := strings.Index(name, "."); index >= 0 {
		label = name[:index]
	}
	label = strings.ReplaceAll(label, `\032`, " ")
	return strings.ReplaceAll(label, `\.`, ".")
}

// assembleMDNS returns a device for each instance that has a UUID and
// an address. Without a UUID the device cannot be matched with its SSDP
// record, and without an address nothing can reach it.
func assembleMDNS(instances map[string]*mdnsInstance) []Device {
	devices := make([]Device, 0, len(instances))
	for _, inst := range instances {
		uuid := normalizeUUID(inst.uuid)
		if uuid == "" || inst.address == "" {
			continue
		}
		device := Device{
			UUID:    uuid,
			Name:    inst.name,
			Address: inst.address,
		}
		if inst.port != 0 {
			// LinkPlay devices serve the description at this path, on
			// the port of the SRV record.
			device.Description = "http://" + net.JoinHostPort(inst.address, strconv.Itoa(int(inst.port))) + "/description.xml"
		}
		devices = append(devices, device)
	}
	return devices
}

// ssdpRequest is the M-SEARCH message. Its ST header selects UPnP
// MediaRenderer devices, which includes every LinkPlay device.
var ssdpRequest = []byte(fmt.Sprintf("M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: %d\r\nST: urn:schemas-upnp-org:device:MediaRenderer:1\r\n\r\n", ssdpMX))

// searchSSDP sends one M-SEARCH, reads responses until ssdpReadTimeout
// or the end of ctx, and merges each LinkPlay device into devices.
func searchSSDP(ctx context.Context, devices map[string]Device) {
	conn, err := listenPacket("udp4", ":0")
	if err != nil {
		return
	}
	defer conn.Close()

	if _, err := conn.WriteTo(ssdpRequest, ssdpGroup); err != nil {
		return
	}

	// Stop reading at the deadline of ctx too, so a search that starts
	// late in the window does not make Discover run past it.
	deadline := time.Now().Add(ssdpReadTimeout)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	_ = conn.SetReadDeadline(deadline)
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

// parseSSDP reads one M-SEARCH response. The USN header holds the UUID,
// and the host of the LOCATION header is the address. The response has
// no friendly name, so Name stays empty until mergeDevice fills it from
// mDNS. It returns false for a response without a UUID or an address,
// or from a device that is not LinkPlay.
func parseSSDP(response []byte) (Device, bool) {
	headers := httpHeaders(response)
	uuid := uuidFromUSN(headers["usn"])
	address := hostFromLocation(headers["location"])
	if uuid == "" || address == "" || !isLinkPlayUUID(uuid) {
		return Device{}, false
	}
	return Device{UUID: normalizeUUID(uuid), Address: address, Description: headers["location"]}, true
}

// isLinkPlayUUID reports whether an SSDP uuid is a LinkPlay uuid. The
// mDNS service type already selects LinkPlay devices, but the SSDP
// search matches every MediaRenderer on the network, including other
// brands such as Denon. A LinkPlay uuid is 16 bytes: 12 bytes that
// identify the device, then the bytes FF98F2F7.
func isLinkPlayUUID(value string) bool {
	clean := strings.ToUpper(value)
	clean = strings.NewReplacer("-", "", ":", "").Replace(clean)
	return len(clean) >= 32 && strings.HasSuffix(clean, "FF98F2F7")
}

// httpHeaders returns the headers of an HTTP response keyed by
// lower-case name, because devices differ in header capitalization.
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

// uuidFromUSN returns the value of the "uuid:<value>" part of an SSDP
// USN header, which can be followed by "::<service>". The value can
// have dashes and any letter case.
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

// hostFromLocation returns the host of an SSDP LOCATION URL of the form
// "http://<host>:<port>/<path>". It drops the port, because the control
// API uses a different one.
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
