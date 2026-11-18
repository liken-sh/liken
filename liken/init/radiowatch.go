package main

// The radio watch keeps each radio's status current after the boot.
// wpa_supplicant rejoins on its own when an access point goes away, and
// moves to a stronger access point of the same network when bgscan
// finds one. The watch reads the supplicant's events, so status shows
// the access point the radio uses now, not the one it joined at boot.
//
// The join opens the event stream before the watch sends STATUS once
// for the baseline, so no event is lost in between. The watch sends
// STATUS again on each CTRL-EVENT-CONNECTED, because that event has the
// BSSID but not the channel. It also sends STATUS on wpaAttached, which
// the client puts on the stream when it attaches again after a
// supplicant restart, because the supplicant does not replay the events
// that it sent while nothing was attached.
//
// After publishBootFacts, only the watch writes the network subtree. It
// also applies the verdicts of the radios that join in the background,
// so no two goroutines write those files at the same time.
//
// The boot gives a radio an address only when its first join succeeds.
// When the supplicant joins later, the watch gives the radio its
// address, or the radio would be associated and pass no traffic.

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

// wirelessRetryCap is the longest the watch lets the supplicant wait
// to try the network again after access points refuse a join. The
// supplicant's own wait grows through 10, 20, 30, 60, 90, 120, and then
// 300 seconds. An access point controller can refuse a client for a
// minute or two after it disconnects it, so without the cap the radio
// can stay off the network for up to 300 seconds after the refusals
// end. A join attempt sends one authentication frame to each access
// point, so one attempt every 30 seconds adds little load.
const wirelessRetryCap = 30 * time.Second

// wpaRetry is the name of the event that the watch sends to itself
// when a capped wait ends.
const wpaRetry = "LIKEN-RETRY"

// supplicantRequestTimeout is how long one request to the supplicant
// can take. The supplicant responds to STATUS from memory, so a request
// still waiting after two seconds means the supplicant is restarting.
const supplicantRequestTimeout = 2 * time.Second

// radioEvent is one supplicant event with the interface it came from.
// When a radio's stream closes, the event has closed set, so the watch
// can count the streams that are still open.
type radioEvent struct {
	ifname string
	event  wpaEvent
	closed bool
}

// radioWatch publishes conns as status. events merges the events of
// every radio.
type radioWatch struct {
	ctx        context.Context
	tree       machine.FactsTree
	clusterDoc *cluster.Cluster
	conns      []*connection
	events     chan radioEvent
	following  int
	now        func() time.Time

	// interfaces is the spec's list, which says how to address each
	// radio. address does the addressing. It is a field so a test can
	// address a radio without a link or a DHCP server.
	interfaces []machine.InterfaceSpec
	address    func(machine.InterfaceSpec) (*connection, error)

	// after is time.AfterFunc. It is a field so a test can run the
	// function without waiting.
	after func(time.Duration, func())
}

// watchRadios returns the function that watches every declared radio.
// conns is the boot's connection list. pass is the background join, or
// nil when every radio has a verdict from the boot. The function
// returns when every radio has a verdict and every event stream has
// closed. A radio whose configuration was refused has no supplicant and
// no stream.
func watchRadios(conns []*connection, pass *radioPass, interfaces []machine.InterfaceSpec,
	tree machine.FactsTree, clusterDoc *cluster.Cluster) func(context.Context) error {
	return func(ctx context.Context) error {
		w := &radioWatch{
			ctx:  ctx,
			tree: tree, clusterDoc: clusterDoc,
			events:     make(chan radioEvent, 16),
			now:        time.Now,
			interfaces: interfaces,
			address:    addressByName,
			after:      func(d time.Duration, f func()) { time.AfterFunc(d, f) },
		}
		var settled chan *connection
		pending := 0
		if pass != nil {
			// The pass's list has the pending entries that the
			// verdicts replace. The watch never changes the boot's own
			// list, so a reader of that list never sees it change.
			w.conns = pass.conns
			settled, pending = pass.settled, pass.pending
		} else {
			w.conns = append([]*connection(nil), conns...)
		}

		changed := false
		for i, conn := range w.conns {
			if next, follows := w.follow(conn); follows {
				w.conns[i] = next
				changed = true
			}
		}
		if changed {
			w.publish()
		}

		for pending > 0 || w.following > 0 {
			select {
			case <-ctx.Done():
				return nil
			case conn := <-settled:
				pending--
				conn.report()
				conn, _ = w.follow(conn)
				w.fold(conn)
				w.publish()
				if err := writeResolvConf(w.conns); err != nil {
					fmt.Fprintf(os.Stderr, "liken: network: %v\n", err)
				}
			case e := <-w.events:
				if e.closed {
					w.following--
					continue
				}
				if w.apply(e) {
					w.publish()
				}
			}
		}
		return nil
	}
}

// follow starts reading one radio's events and sets its baseline from
// STATUS. It returns false and the connection unchanged when the
// connection has no supplicant to watch.
func (w *radioWatch) follow(conn *connection) (*connection, bool) {
	if conn.radio == nil || conn.radio.control == nil {
		return conn, false
	}
	w.following++
	go func(ifname string, stream <-chan wpaEvent) {
		for event := range stream {
			if !w.send(radioEvent{ifname: ifname, event: event}) {
				return
			}
		}
		w.send(radioEvent{ifname: ifname, closed: true})
	}(conn.ifname, conn.radio.control.events())
	if next, changed := w.associate(conn); changed {
		return w.addressLate(next), true
	}
	return conn, true
}

// send queues one event for the watch. It returns false when the
// watch has ended.
func (w *radioWatch) send(e radioEvent) bool {
	select {
	case w.events <- e:
		return true
	case <-w.ctx.Done():
		return false
	}
}

// fold replaces the list entry that has the connection's interface
// name, or appends the connection when there is none.
func (w *radioWatch) fold(conn *connection) {
	for i, existing := range w.conns {
		if existing.ifname == conn.ifname {
			w.conns[i] = conn
			return
		}
	}
	w.conns = append(w.conns, conn)
}

// publish writes the network subtree from the watch's list.
func (w *radioWatch) publish() {
	// When the write fails, status keeps its old value, and this line
	// is the only record of the change.
	if err := w.tree.WriteNetwork(networkFacts(w.clusterDoc, w.conns)); err != nil {
		fmt.Fprintf(os.Stderr, "liken: writing facts: %v\n", err)
	}
}

// apply updates the radio that an event is about, and returns whether
// the radio's status changed.
func (w *radioWatch) apply(e radioEvent) bool {
	i := w.index(e.ifname)
	if i < 0 {
		return false
	}
	conn := w.conns[i]
	// bgscan finishes a scan every 30 seconds while the signal is
	// weak, and logging each one would fill the log. associate logs
	// the connected event with more detail.
	if e.event.name != "CTRL-EVENT-SCAN-RESULTS" && e.event.name != "CTRL-EVENT-CONNECTED" {
		if line := describeWirelessEvent(conn.ifname, e.event); line != "" {
			fmt.Println(line)
		}
	}

	var next *connection
	changed := false
	switch e.event.name {
	case "CTRL-EVENT-CONNECTED", wpaAttached:
		next, changed = w.associate(conn)
	case "CTRL-EVENT-DISCONNECTED":
		next, changed = disconnect(conn, e.event)
	case wpaRetry:
		w.retry(conn, e.event.fields["id"])
	default:
		w.capWait(conn.ifname, e.event)
		state, message, settled := classifyWirelessEvent(e.event)
		if settled && state != conn.radio.state {
			next, changed = withRadio(conn, func(r *radio) {
				r.state, r.message = state, message
				r.bssid, r.frequency, r.associatedAt = "", 0, time.Time{}
			}), true
		}
	}
	if changed {
		w.conns[i] = w.addressLate(next)
	}
	return changed
}

// addressLate addresses a radio that has associated and has no
// address, and returns the connection with the address. A radio that
// already has an address keeps it when it moves to another access
// point, because the access points of one network share a subnet.
//
// When addressing fails, the radio stays Connected with the error as
// its message, which is the verdict the boot gives a radio that joined
// and then failed DHCP. The next association tries again.
func (w *radioWatch) addressLate(conn *connection) *connection {
	if conn.addr != nil || conn.radio.state != machine.WirelessConnected {
		return conn
	}
	var spec machine.InterfaceSpec
	for _, ifc := range w.interfaces {
		if ifc.Name == conn.ifname {
			spec = ifc
		}
	}
	addressed, err := w.address(spec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: network: %s: %v\n", conn.ifname, err)
		return withRadio(conn, func(r *radio) { r.message = err.Error() })
	}
	addressed.radio = conn.radio
	// writeResolvConf orders the nameservers by interface and keeps at
	// most three, the same as at boot.
	w.fold(addressed)
	if err := writeResolvConf(w.conns); err != nil {
		fmt.Fprintf(os.Stderr, "liken: network: %v\n", err)
	}
	return addressed
}

// capWait shortens the supplicant's wait after access points refuse a
// join. The supplicant reports the wait in
// CTRL-EVENT-SSID-TEMP-DISABLED, and the watch sends wpaRetry to
// itself after wirelessRetryCap. Only the reason CONN_FAILED is
// capped, because it means the access points refused the join. For a
// rejected passphrase the supplicant keeps its whole wait, because an
// earlier retry would be rejected again.
func (w *radioWatch) capWait(ifname string, event wpaEvent) {
	if event.name != "CTRL-EVENT-SSID-TEMP-DISABLED" || event.fields["reason"] != "CONN_FAILED" {
		return
	}
	wait, err := strconv.Atoi(event.fields["duration"])
	if err != nil || time.Duration(wait)*time.Second <= wirelessRetryCap {
		return
	}
	retry := radioEvent{ifname: ifname, event: wpaEvent{
		name: wpaRetry, fields: map[string]string{"id": event.fields["id"]},
	}}
	w.after(wirelessRetryCap, func() {
		// The retry only shortens a wait that ends by itself. When
		// the watch is busy or has ended, dropping the event costs
		// nothing, so the send does not block.
		select {
		case w.events <- retry:
		default:
		}
	})
}

// retry makes the supplicant try the network again now. SELECT_NETWORK
// clears the network's wait and failure count and starts a join.
// ENABLE_NETWORK does not work here, because it only changes a
// disabled network, and a wait leaves the network enabled. A radio
// that joined during the wait is left alone.
func (w *radioWatch) retry(conn *connection, id string) {
	if conn.radio.state == machine.WirelessConnected {
		return
	}
	fmt.Printf("liken: wireless: %s: trying the network again after %s\n", conn.ifname, wirelessRetryCap)
	socket := machine.SupplicantSocket(wirelessRunDir, conn.ifname)
	if _, err := machine.AskSupplicant(socket, "SELECT_NETWORK "+id, supplicantRequestTimeout); err != nil {
		fmt.Fprintf(os.Stderr, "liken: wireless: %s: %v\n", conn.ifname, err)
	}
}

// index returns the position of an interface in the list, or -1.
func (w *radioWatch) index(ifname string) int {
	for i, conn := range w.conns {
		if conn.ifname == ifname {
			return i
		}
	}
	return -1
}

// associate sends STATUS to read which access point the radio is on.
// It returns the updated connection and true when the access point or
// its channel differs from status. When the request fails, status stays
// as it is, because the next event or attach sends STATUS again.
func (w *radioWatch) associate(conn *connection) (*connection, bool) {
	r := conn.radio
	socket := machine.SupplicantSocket(wirelessRunDir, conn.ifname)
	association, err := machine.AskSupplicantAssociation(socket, supplicantRequestTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: wireless: %s: %v\n", conn.ifname, err)
		return conn, false
	}
	if association.BSSID == "" {
		return conn, false
	}
	if r.state == machine.WirelessConnected && r.bssid == association.BSSID && r.frequency == association.Frequency {
		return conn, false
	}
	fmt.Printf("liken: wireless: %s associated with %s on %d MHz (%s)\n",
		conn.ifname, association.BSSID, association.Frequency, machine.WirelessBand(association.Frequency))
	now := w.now()
	return withRadio(conn, func(next *radio) {
		// A radio that was already connected keeps its message, which
		// can be the error from addressing after the join.
		if next.state != machine.WirelessConnected {
			next.message = ""
		}
		// associatedAt changes only for a new access point. A change
		// of channel on the same access point keeps it.
		if next.state != machine.WirelessConnected || next.bssid != association.BSSID {
			next.associatedAt = now
		}
		next.state = machine.WirelessConnected
		next.bssid, next.frequency = association.BSSID, association.Frequency
	}), true
}

// disconnect records that a connected radio lost its access point. The
// state becomes Associating and not NoCarrier, because the supplicant
// starts rejoining at once, and WirelessJoined reports Associating as
// Joining, which does not mark the machine Degraded. For a radio that
// was not connected, such as one that fails its handshake again and
// again, disconnect changes nothing, and the event only goes to the log.
func disconnect(conn *connection, event wpaEvent) (*connection, bool) {
	if conn.radio.state != machine.WirelessConnected {
		return conn, false
	}
	reason, _ := strconv.Atoi(event.fields["reason"])
	from := conn.radio.bssid
	if from == "" {
		from = "its access point"
	}
	sender := ""
	if event.fields["locally_generated"] == "1" {
		sender = ", sent by this machine"
	}
	message := fmt.Sprintf("lost %s (%s%s), and the supplicant is joining again",
		from, machine.DisconnectReasonText(reason), sender)
	return withRadio(conn, func(r *radio) {
		r.state, r.message = machine.WirelessAssociating, message
		r.bssid, r.frequency, r.associatedAt = "", 0, time.Time{}
		r.lastDisconnectReason = reason
	}), true
}

// withRadio returns a copy of the connection with change applied to a
// copy of its radio. The original connection and radio stay as they
// were, so the boot's list never changes.
func withRadio(conn *connection, change func(*radio)) *connection {
	next := *conn
	r := *conn.radio
	change(&r)
	next.radio = &r
	return &next
}

func declaresRadio(conns []*connection) bool {
	for _, conn := range conns {
		if conn.radio != nil {
			return true
		}
	}
	return false
}
