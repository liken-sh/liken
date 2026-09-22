// The device's UPnP event path. A WiiM is a UPnP MediaRenderer, and
// RenderingControl and AVTransport push their state to a callback the
// client registers. The client subscribes once and renews before the
// grant expires; a subscription the device refuses leaves the poll as
// the only path, which is the behavior the driver had before events.

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

	"github.com/liken-sh/equipment-operator/equipment"
)

// The UPnP MediaRenderer's port and the two services that push. The
// device serves its description on upnpPort, and these paths are the
// event URLs the description names.
const (
	upnpPort          = "49152"
	renderControlPath = "/upnp/event/rendercontrol1"
	avTransportPath   = "/upnp/event/rendertransport1"
)

// callbackPath is the one path the device calls back on. The SID on an
// inbound NOTIFY names the service, so one path carries both.
const callbackPath = "/events"

// The subscription grant and the margin before it expires at which the
// client renews. The device answered Second-1801 in the drill, so a
// grant of 1800 seconds leaves the renewal well inside the term.
const (
	subscribeSeconds = 1800
	renewBefore      = 5 * time.Minute
)

// eventRetryInterval bounds how often a device that has no event path
// is asked. A refused subscription is retried at this interval and not
// at the poll interval, so the fallback costs two requests a minute
// instead of two every ten seconds.
const eventRetryInterval = time.Minute

// eventPaths is the set the client subscribes to, in a fixed order.
var eventPaths = []string{renderControlPath, avTransportPath}

// subscription is one GENA subscription the device holds for this
// client.
type subscription struct {
	sid     string
	expires time.Time
}

// eventServer is the client's callback listener: the socket the device
// connects back to, and the address the client advertises for it. The
// host is the local address the device is reached on, so the device can
// route to it.
type eventServer struct {
	listener net.Listener
	server   *http.Server
	host     string
	port     int
}

// url is the callback URL the client sends in CALLBACK.
func (e *eventServer) url() string {
	return fmt.Sprintf("http://%s%s", net.JoinHostPort(e.host, fmt.Sprintf("%d", e.port)), callbackPath)
}

// upnpBaseFor answers the device's UPnP base URL, which is its host on
// the MediaRenderer port. The address carries the control port when it
// names one, so only the host is kept.
func upnpBaseFor(address string) string {
	host := address
	if h, _, err := net.SplitHostPort(address); err == nil {
		host = h
	}
	return "http://" + net.JoinHostPort(host, upnpPort)
}

// startEvents brings up the callback listener. A listener that cannot
// bind reports once and leaves the client on the poll, because a
// cluster with no reachable callback still reads every value.
func (c *Client) startEvents(ctx context.Context) {
	if strings.TrimSpace(c.address) == "" {
		// The unit is wired before discovery has an address for the amp,
		// and it is rebuilt once one arrives. There is nothing to
		// subscribe to yet, and the poll is the only path meanwhile.
		return
	}
	host := localAddress(c.address)
	if host == "" {
		c.reportEventLoss("cannot find a local address the device reaches")
		return
	}
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		c.reportEventLoss("cannot open the callback listener: %v", err)
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

// stopEvents closes the callback listener and tells the device the
// subscriptions are done, so a stopped operator leaves no callback the
// device keeps sending to.
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

// localAddress answers the local address the device reaches this
// process on, by asking the routing table for the source address it
// would use for the device. Nothing is sent.
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

// manageSubscriptions brings each subscription up, and renews one
// whose grant is near its end. It runs on the poll loop. A pass that
// finds no subscription waits eventRetryInterval before it tries
// again, and the poll keeps carrying the evented fields in the
// meantime.
func (c *Client) manageSubscriptions(ctx context.Context) {
	c.eventsMutex.Lock()
	server := c.events
	due := time.Now().After(c.eventsRetry)
	c.eventsMutex.Unlock()
	if server == nil {
		return
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
		if !c.subscribe(ctx, server, path) {
			c.eventsMutex.Lock()
			c.eventsRetry = time.Now().Add(eventRetryInterval)
			c.eventsMutex.Unlock()
		}
	}
}

// subscribe registers the callback for one service, and answers
// whether the device took it.
func (c *Client) subscribe(ctx context.Context, server *eventServer, path string) bool {
	req, err := http.NewRequestWithContext(ctx, "SUBSCRIBE", c.upnpBase+path, nil)
	if err != nil {
		c.reportEventLoss("cannot build the subscription for %s: %v", path, err)
		c.report(CommandFailed)
		return false
	}
	req.Header.Set("CALLBACK", "<"+server.url()+">")
	req.Header.Set("NT", "upnp:event")
	req.Header.Set("TIMEOUT", "Second-"+fmt.Sprint(subscribeSeconds))
	resp, err := c.upnp.Do(req)
	if err != nil {
		c.reportEventLoss("the device refused the subscription for %s: %v", path, err)
		c.report(CommandFailed)
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	sid := resp.Header.Get("SID")
	if resp.StatusCode != http.StatusOK || sid == "" {
		c.reportEventLoss("the device answered %s to the subscription for %s", resp.Status, path)
		c.report(CommandFailed)
		return false
	}
	c.recordSubscription(path, sid, subscribeSeconds)
	c.report(CommandOK)
	c.reportEventsBack()
	return true
}

// renew extends a subscription before its grant ends. A renewal the
// device refuses drops the subscription, and the next pass subscribes
// it again.
func (c *Client) renew(ctx context.Context, server *eventServer, path string) {
	c.subscriptionsMutex.Lock()
	sub := c.subscriptions[path]
	c.subscriptionsMutex.Unlock()
	if sub == nil {
		return
	}
	req, err := http.NewRequestWithContext(ctx, "SUBSCRIBE", c.upnpBase+path, nil)
	if err != nil {
		c.dropSubscription(path)
		c.report(CommandFailed)
		return
	}
	req.Header.Set("SID", sub.sid)
	req.Header.Set("TIMEOUT", "Second-"+fmt.Sprint(subscribeSeconds))
	resp, err := c.upnp.Do(req)
	if err != nil {
		c.dropSubscription(path)
		c.report(CommandFailed)
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		c.dropSubscription(path)
		c.report(CommandFailed)
		return
	}
	c.recordSubscription(path, sub.sid, timeoutSeconds(resp.Header.Get("TIMEOUT")))
	c.report(CommandOK)
}

// unsubscribe tells the device to stop sending to the callback.
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

// recordSubscription stores a subscription and when its grant ends.
func (c *Client) recordSubscription(path, sid string, seconds int) {
	if seconds <= 0 {
		seconds = subscribeSeconds
	}
	c.subscriptionsMutex.Lock()
	c.subscriptions[path] = &subscription{sid: sid, expires: time.Now().Add(time.Duration(seconds) * time.Second)}
	c.subscriptionsMutex.Unlock()
}

// dropSubscription forgets one subscription, so the next pass
// subscribes it again.
func (c *Client) dropSubscription(path string) {
	c.subscriptionsMutex.Lock()
	delete(c.subscriptions, path)
	c.subscriptionsMutex.Unlock()
}

// subscribed answers whether the client holds a subscription for one
// service, which is what lets the poll leave that service's fields to
// the events.
func (c *Client) subscribed(path string) bool {
	c.subscriptionsMutex.Lock()
	defer c.subscriptionsMutex.Unlock()
	sub, ok := c.subscriptions[path]
	return ok && sub != nil && sub.sid != ""
}

// handleNotify folds one NOTIFY into the state. A NOTIFY for a SID the
// client did not take is not its own and is dropped.
func (c *Client) handleNotify(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	sid := r.Header.Get("SID")
	path := c.pathForSID(sid)
	if path != "" {
		c.foldEvent(path, body)
	}
	w.WriteHeader(http.StatusOK)
}

// pathForSID answers the service a SID belongs to, or an empty string
// when the SID is not one this client took.
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

// mergeEvents keeps a poll from undoing an event. The poll's reads were
// taken before any event that arrived while it ran, so writing them
// would move a value back to where it stood before the change the
// device already pushed. A field whose service is not subscribed is
// left to the poll, which is the fallback.
func (c *Client) mergeEvents(next *Status) {
	rendering := c.subscribed(renderControlPath)
	transport := c.subscribed(avTransportPath)
	if !rendering && !transport {
		return
	}
	c.mutex.Lock()
	held := c.state
	c.mutex.Unlock()
	if rendering {
		next.Playback.Volume = held.Playback.Volume
		next.Playback.Mute = held.Playback.Mute
	}
	if transport {
		next.Playback.Status = held.Playback.Status
	}
}

// foldEvent applies one NOTIFY. The event is the device reporting its
// own state, so it also clears the run of missed polls.
func (c *Client) foldEvent(path string, body []byte) {
	fields, ok := parseEvent(path, body)
	if !ok {
		return
	}
	c.mutex.Lock()
	previous := c.state
	wasReachable := c.reachable
	next := c.state
	fields.apply(&next)
	c.state = next
	c.reachable = equipment.ConditionTrue
	c.misses = 0
	c.mutex.Unlock()
	c.announce(previous, next, wasReachable)
}

// eventFields is what one NOTIFY carried. A nil field was not in the
// body, because a change event names only what moved.
type eventFields struct {
	volume *int
	mute   *bool
	status *string
}

// apply writes the fields the event carried into the snapshot.
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

// propertyset is the GENA envelope. LastChange holds the service's own
// document, which the envelope carries escaped.
type propertyset struct {
	Properties []struct {
		LastChange string `xml:"LastChange"`
	} `xml:"property"`
}

// parseEvent reads one NOTIFY body into the fields it moved.
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

// renderControlChange is the RenderingControl document. Each field
// names its channel, and the driver follows the master.
type renderControlChange struct {
	InstanceID struct {
		Volume []renderControlField `xml:"Volume"`
		Mute   []renderControlField `xml:"Mute"`
	} `xml:"InstanceID"`
}

// renderControlField is one channel's value.
type renderControlField struct {
	Channel string `xml:"channel,attr"`
	Val     string `xml:"val,attr"`
}

// parseRenderControl reads the volume and mute the master channel
// reports.
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

// avTransportChange is the AVTransport document. The driver reads the
// transport state; the track comes from the poll's getMetaInfo, which
// carries the format fields the event's metadata does not.
type avTransportChange struct {
	InstanceID struct {
		TransportState avTransportField `xml:"TransportState"`
	} `xml:"InstanceID"`
}

// avTransportField is one attribute-valued field.
type avTransportField struct {
	Val string `xml:"val,attr"`
}

// parseAVTransport reads the transport state.
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

// playbackStatusFor maps an AVTransport state onto the word
// getPlayerStatus uses, so the snapshot does not name the same state
// two ways.
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

// timeoutSeconds reads a GENA TIMEOUT header, which is "Second-1800",
// and answers the default when it names no number.
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

// reportEventLoss writes the fallback to the log once per loss, so a
// device that refuses events does not fill the log at the poll
// interval.
func (c *Client) reportEventLoss(format string, args ...any) {
	c.eventsMutex.Lock()
	already := c.eventsLost
	c.eventsLost = true
	c.eventsMutex.Unlock()
	if already {
		return
	}
	fmt.Fprintf(os.Stderr, "wiim: events off for %s: %s\n", c.address, fmt.Sprintf(format, args...))
}

// reportEventsBack clears the loss flag once a subscription is taken.
func (c *Client) reportEventsBack() {
	c.eventsMutex.Lock()
	c.eventsLost = false
	c.eventsMutex.Unlock()
}
