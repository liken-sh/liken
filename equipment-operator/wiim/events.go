// A WiiM is a UPnP MediaRenderer. The client subscribes to its
// RenderingControl and AVTransport services with GENA, and the device
// sends a NOTIFY request to the client's callback URL when the volume,
// the mute, or the play state changes. So a change made at the device
// reaches the client without waiting for a poll. When the device
// refuses to subscribe, the client has only the poll.

package wiim

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/liken-sh/liken/equipment-operator/equipment"
)

// upnpPort is the port of the device's UPnP services. The paths are
// the event URLs that the device's UPnP description lists for
// RenderingControl and AVTransport.
const (
	upnpPort          = "49152"
	renderControlPath = "/upnp/event/rendercontrol1"
	avTransportPath   = "/upnp/event/rendertransport1"
)

// callbackPath receives the NOTIFY requests of every service. The SID
// header of a NOTIFY identifies the subscription, and so the service.
const callbackPath = "/events"

// subscribeSeconds is the subscription length that the client asks
// for. The first poll within renewBefore of the end renews it.
const (
	subscribeSeconds = 1800
	renewBefore      = 5 * time.Minute
)

// eventRetryInterval is how long the client waits after a refused
// subscription before it subscribes again. Without it, a device that
// never accepts a subscription would receive a SUBSCRIBE for each
// service on every poll.
const eventRetryInterval = time.Minute

var eventPaths = []string{renderControlPath, avTransportPath}

type subscription struct {
	sid     string
	expires time.Time
}

// eventServer receives NOTIFY requests. Its host is this machine's
// address on the route to the device, because the device must be able
// to connect to the callback URL.
type eventServer struct {
	listener net.Listener
	server   *http.Server
	host     string
	port     int
}

// url returns the callback URL for the CALLBACK header.
func (e *eventServer) url() string {
	return fmt.Sprintf("http://%s%s", net.JoinHostPort(e.host, fmt.Sprintf("%d", e.port)), callbackPath)
}

// upnpBaseFor returns the base URL of the device's UPnP services. The
// address can include the port of the HTTPS control API, so it keeps
// only the host and uses upnpPort.
func upnpBaseFor(address string) string {
	host := address
	if h, _, err := net.SplitHostPort(address); err == nil {
		host = h
	}
	return "http://" + net.JoinHostPort(host, upnpPort)
}

// startEvents starts the callback listener. When it cannot, it logs
// why, and the client runs on the poll alone.
func (c *Client) startEvents(ctx context.Context) {
	if strings.TrimSpace(c.address) == "" {
		// Discovery has not found an address yet. When it does, the
		// operator builds a new client with the address.
		return
	}
	host := localAddress(c.address)
	if host == "" {
		c.reportEventLoss("no local address of this host reaches the device")
		return
	}
	listener, err := c.Listen("tcp", ":0")
	if err != nil {
		c.reportEventLoss("opening the callback listener: %v", err)
		return
	}
	server := &eventServer{
		listener: listener,
		host:     host,
		port:     listener.Addr().(*net.TCPAddr).Port,
	}
	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, c.handleNotify)
	server.server = &http.Server{Handler: mux}
	c.eventsMutex.Lock()
	c.events = server
	c.eventsMutex.Unlock()
	go server.server.Serve(listener)
	go func() {
		<-ctx.Done()
		server.server.Close()
	}()
}

// stopEvents unsubscribes and closes the listener, so the device does
// not keep sending NOTIFY requests to an operator that has stopped.
func (c *Client) stopEvents() {
	c.unsubscribe()
	c.eventsMutex.Lock()
	server := c.events
	c.events = nil
	c.eventsMutex.Unlock()
	if server != nil {
		server.server.Close()
	}
}

// localAddress returns the source address that the routing table
// chooses for a connection to the device. A UDP dial sends no packet,
// so this only looks up the route.
func localAddress(address string) string {
	conn, err := net.Dial("udp", hostPort(address))
	if err != nil {
		return ""
	}
	defer conn.Close()
	if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return addr.IP.String()
	}
	return ""
}

// manageSubscriptions subscribes to each service that has no
// subscription, and renews each subscription within renewBefore of its
// end. The poll calls it after the device has answered getStatusEx.
// When a subscribe fails, later polls send no new SUBSCRIBE until
// eventRetryInterval has passed. It returns true when it opened a
// subscription, and the poll then reads every family, because the
// device can have changed while no subscription was open.
func (c *Client) manageSubscriptions(ctx context.Context) (opened bool) {
	c.eventsMutex.Lock()
	server := c.events
	due := time.Now().After(c.eventsRetry)
	c.eventsMutex.Unlock()
	if server == nil {
		return false
	}
	for _, path := range eventPaths {
		c.subscriptionsMutex.Lock()
		sub := c.subscriptions[path]
		c.subscriptionsMutex.Unlock()
		if sub != nil {
			if time.Now().After(sub.expires.Add(-renewBefore)) {
				c.renew(ctx, server, path)
			}
			continue
		}
		if !due {
			continue
		}
		if c.subscribe(ctx, server, path) {
			opened = true
			continue
		}
		c.eventsMutex.Lock()
		c.eventsRetry = time.Now().Add(eventRetryInterval)
		c.eventsMutex.Unlock()
	}
	return opened
}

// subscribe returns whether the device accepted the subscription.
func (c *Client) subscribe(ctx context.Context, server *eventServer, path string) bool {
	req, err := http.NewRequestWithContext(ctx, "SUBSCRIBE", c.upnpBase+path, nil)
	if err != nil {
		c.reportEventLoss("building the subscription request for %s: %v", path, err)
		c.report(CommandFailed)
		return false
	}
	req.Header.Set("CALLBACK", "<"+server.url()+">")
	req.Header.Set("NT", "upnp:event")
	req.Header.Set("TIMEOUT", "Second-"+fmt.Sprint(subscribeSeconds))
	resp, err := c.upnp.Do(req)
	if err != nil {
		c.reportEventLoss("sending the subscription request for %s: %v", path, err)
		c.report(CommandFailed)
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	sid := resp.Header.Get("SID")
	if resp.StatusCode != http.StatusOK || sid == "" {
		c.reportEventLoss("the device responded %s with SID %q to the subscription for %s", resp.Status, sid, path)
		c.report(CommandFailed)
		return false
	}
	c.recordSubscription(path, sid, subscribeSeconds)
	c.report(CommandOK)
	c.clearEventLoss()
	return true
}

// renew extends one subscription. It returns true when the device
// responds with a status other than 200, as a device that rebooted does
// for a SID it no longer has. When the renewal fails for any reason,
// renew drops the subscription, and the next manageSubscriptions
// subscribes again.
func (c *Client) renew(ctx context.Context, server *eventServer, path string) (refused bool) {
	c.subscriptionsMutex.Lock()
	sub := c.subscriptions[path]
	c.subscriptionsMutex.Unlock()
	if sub == nil {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, "SUBSCRIBE", c.upnpBase+path, nil)
	if err != nil {
		c.dropSubscription(path)
		c.report(CommandFailed)
		return false
	}
	req.Header.Set("SID", sub.sid)
	req.Header.Set("TIMEOUT", "Second-"+fmt.Sprint(subscribeSeconds))
	resp, err := c.upnp.Do(req)
	if err != nil {
		c.dropSubscription(path)
		c.report(CommandFailed)
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		c.dropSubscription(path)
		c.report(CommandFailed)
		return true
	}
	c.recordSubscription(path, sub.sid, timeoutSeconds(resp.Header.Get("TIMEOUT")))
	c.report(CommandOK)
	return false
}

// unsubscribe runs at shutdown, so it ignores errors and gives all the
// UNSUBSCRIBE requests five seconds in total.
func (c *Client) unsubscribe() {
	c.subscriptionsMutex.Lock()
	held := c.subscriptions
	c.subscriptions = map[string]*subscription{}
	c.subscriptionsMutex.Unlock()
	if len(held) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for path, sub := range held {
		req, err := http.NewRequestWithContext(ctx, "UNSUBSCRIBE", c.upnpBase+path, nil)
		if err != nil {
			continue
		}
		req.Header.Set("SID", sub.sid)
		resp, err := c.upnp.Do(req)
		if err != nil {
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

func (c *Client) recordSubscription(path, sid string, seconds int) {
	if seconds <= 0 {
		seconds = subscribeSeconds
	}
	c.subscriptionsMutex.Lock()
	c.subscriptions[path] = &subscription{sid: sid, expires: time.Now().Add(time.Duration(seconds) * time.Second)}
	c.subscriptionsMutex.Unlock()
}

// dropSubscription forgets one subscription, so the next
// manageSubscriptions subscribes again.
func (c *Client) dropSubscription(path string) {
	c.subscriptionsMutex.Lock()
	delete(c.subscriptions, path)
	c.subscriptionsMutex.Unlock()
}

// renewAll renews every subscription now, however long it has left,
// and returns true when the device refused a renewal. The poll calls it
// when the device answers after missed polls, to find out whether the
// device rebooted and lost the subscriptions.
func (c *Client) renewAll(ctx context.Context) (refused bool) {
	c.eventsMutex.Lock()
	server := c.events
	c.eventsMutex.Unlock()
	if server == nil {
		return false
	}
	for _, path := range eventPaths {
		if c.renew(ctx, server, path) {
			refused = true
		}
	}
	return refused
}

// handleNotify ignores a NOTIFY whose SID matches none of the client's
// subscriptions, and responds 200 to every request.
func (c *Client) handleNotify(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	sid := r.Header.Get("SID")
	path := c.pathForSID(sid)
	if path != "" {
		c.applyEvent(path, body)
	}
	w.WriteHeader(http.StatusOK)
}

func (c *Client) pathForSID(sid string) string {
	c.subscriptionsMutex.Lock()
	defer c.subscriptionsMutex.Unlock()
	for path, sub := range c.subscriptions {
		if sub.sid == sid {
			return path
		}
	}
	return ""
}

// applyEvent writes the values of one NOTIFY to the state. A NOTIFY
// shows that the device is reachable, so it also sets reachable and
// clears the count of missed polls. It records a write number for each
// field it writes, so a poll that read the device before the event does
// not overwrite the event's value (sequence.go).
func (c *Client) applyEvent(path string, body []byte) {
	fields, ok := parseEvent(path, body)
	if !ok {
		return
	}
	c.mutex.Lock()
	previous := c.state
	wasReachable := c.reachable
	next := c.state
	fields.apply(&next)
	if fields.volume != nil {
		c.wrote(&c.written.volume)
	}
	if fields.mute != nil {
		c.wrote(&c.written.mute)
	}
	if fields.status != nil {
		c.wrote(&c.written.status)
	}
	c.state = next
	c.reachable = equipment.ConditionTrue
	c.misses = 0
	c.mutex.Unlock()
	c.announce(previous, next, wasReachable)
}

// eventFields holds the values in one NOTIFY. A nil field was not in
// the body, because an event lists only the values that changed.
type eventFields struct {
	volume *int
	mute   *bool
	status *string
}

func (f eventFields) apply(s *Status) {
	if f.volume != nil {
		s.Playback.Volume = *f.volume
	}
	if f.mute != nil {
		s.Playback.Mute = *f.mute
	}
	if f.status != nil {
		s.Playback.Status = *f.status
	}
}

// propertyset is the XML envelope of a NOTIFY body. Each LastChange
// element holds the service's own XML document as escaped text.
type propertyset struct {
	Properties []struct {
		LastChange string `xml:"LastChange"`
	} `xml:"property"`
}

// parseEvent returns false when the body has no value that the client
// uses.
func parseEvent(path string, body []byte) (eventFields, bool) {
	var set propertyset
	if xml.Unmarshal(body, &set) != nil {
		return eventFields{}, false
	}
	for _, property := range set.Properties {
		lastChange := strings.TrimSpace(property.LastChange)
		if lastChange == "" {
			continue
		}
		switch path {
		case renderControlPath:
			return parseRenderControl(lastChange)
		case avTransportPath:
			return parseAVTransport(lastChange)
		}
	}
	return eventFields{}, false
}

// renderControlChange is the RenderingControl LastChange document. Each
// value has a channel attribute, and the driver reads only the Master
// channel.
type renderControlChange struct {
	InstanceID struct {
		Volume []renderControlField `xml:"Volume"`
		Mute   []renderControlField `xml:"Mute"`
	} `xml:"InstanceID"`
}

type renderControlField struct {
	Channel string `xml:"channel,attr"`
	Val     string `xml:"val,attr"`
}

func parseRenderControl(lastChange string) (eventFields, bool) {
	var change renderControlChange
	if xml.Unmarshal([]byte(lastChange), &change) != nil {
		return eventFields{}, false
	}
	fields := eventFields{}
	for _, field := range change.InstanceID.Volume {
		if !strings.EqualFold(field.Channel, "Master") {
			continue
		}
		if volume, ok := atoi(field.Val); ok {
			fields.volume = &volume
		}
	}
	for _, field := range change.InstanceID.Mute {
		if !strings.EqualFold(field.Channel, "Master") {
			continue
		}
		muted := field.Val == "1"
		fields.mute = &muted
	}
	return fields, fields.volume != nil || fields.mute != nil
}

// avTransportChange is the AVTransport LastChange document. The driver
// reads only the transport state from it. The track comes from the
// poll's getMetaInfo request, because the track metadata in an event
// has no sample rate, bit depth, or bit rate.
type avTransportChange struct {
	InstanceID struct {
		TransportState avTransportField `xml:"TransportState"`
	} `xml:"InstanceID"`
}

type avTransportField struct {
	Val string `xml:"val,attr"`
}

func parseAVTransport(lastChange string) (eventFields, bool) {
	var change avTransportChange
	if xml.Unmarshal([]byte(lastChange), &change) != nil {
		return eventFields{}, false
	}
	if change.InstanceID.TransportState.Val == "" {
		return eventFields{}, false
	}
	status := playbackStatusFor(change.InstanceID.TransportState.Val)
	fields := eventFields{status: &status}
	return fields, true
}

// playbackStatusFor converts an AVTransport state to the word that
// getPlayerStatus uses for it, so an event and a poll write the same
// value for the same state. It returns any other state in lower case.
func playbackStatusFor(state string) string {
	switch state {
	case "PLAYING":
		return "play"
	case "PAUSED_PLAYBACK", "PAUSED":
		return "pause"
	case "STOPPED":
		return "stop"
	default:
		return strings.ToLower(state)
	}
}

// timeoutSeconds reads a GENA TIMEOUT header such as "Second-1800". It
// returns subscribeSeconds when the header has no positive number.
func timeoutSeconds(header string) int {
	_, value, ok := strings.Cut(header, "-")
	if !ok {
		return subscribeSeconds
	}
	seconds, ok := atoi(value)
	if !ok || seconds <= 0 {
		return subscribeSeconds
	}
	return seconds
}

// reportEventLoss logs why events are off. It logs only the first
// failure since the last successful subscribe, so a device that refuses
// subscriptions does not add a log line on every retry.
func (c *Client) reportEventLoss(format string, args ...any) {
	c.eventsMutex.Lock()
	already := c.eventsLost
	c.eventsLost = true
	c.eventsMutex.Unlock()
	if already {
		return
	}
	fmt.Fprintf(os.Stderr, "wiim: not receiving events from %s: %s\n", c.address, fmt.Sprintf(format, args...))
}

// clearEventLoss runs after a successful subscribe, so that
// reportEventLoss logs the next failure.
func (c *Client) clearEventLoss() {
	c.eventsMutex.Lock()
	c.eventsLost = false
	c.eventsMutex.Unlock()
}
