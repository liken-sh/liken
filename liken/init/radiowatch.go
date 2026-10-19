package main

// The radio watch keeps each radio's status current for the life of
// the boot (plans/completed/85-the-access-point-a-radio-joined.md).
//
// The join settles a radio once. After that the supplicant keeps the
// session alive on its own: it rejoins when an access point goes away,
// and it moves to another access point of the same network when
// bgscan finds a stronger one. The watch reads the supplicant's events
// for each of those changes, so status says which access point the
// radio holds now, and not only the one it joined at boot.
//
// The watch follows the order in the root AGENTS.md. The event stream
// is attached first, during the join, and the watch then asks the
// supplicant for STATUS once to set the baseline. After that, each
// CTRL-EVENT-CONNECTED asks STATUS again, because the event names the
// BSSID and not the channel. When the stream attaches again after the
// supplicant restarts, the client puts wpaAttached on the stream, and
// the watch asks STATUS again, because the supplicant does not repeat
// what happened while nothing was attached.
//
// The watch is the only writer of the network subtree after
// publishBootFacts. It also lands each background radio's verdict
// (plans/completed/64-the-boot-does-not-wait-for-radios.md), so the
// verdict and the association can never race to write the same files.
//
// The watch also addresses a radio that associates with no address.
// The boot addresses a radio only when its first join succeeds, so a
// radio whose first join failed, and whose supplicant joined later,
// would otherwise hold an access point and carry no traffic.

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

// supplicantAskPatience bounds one request to the supplicant. The
// supplicant answers STATUS from memory in microseconds, so a request
// still out after two seconds is a supplicant that is restarting.
const supplicantAskPatience = 2 * time.Second

// radioEvent is one supplicant event, tagged with its interface. A
// closed stream arrives as closed, so the watch can count the streams
// that are still open.
type radioEvent struct {
	ifname string
	event  wpaEvent
	closed bool
}

// radioWatch is the state of the watch: the connection list as status
// reports it, and the merged stream of every radio's events.
type radioWatch struct {
	ctx        context.Context
	tree       machine.FactsTree
	clusterDoc *cluster.Cluster
	conns      []*connection
	events     chan radioEvent
	following  int
	now        func() time.Time

	// interfaces is the spec's list, which holds how to address each
	// radio. address is the addressing itself, a function value so a
	// test can address a radio with no link and no DHCP server.
	interfaces []machine.InterfaceSpec
	address    func(machine.InterfaceSpec) (*connection, error)
}

// watchRadios is the machine-plane component behind every declared
// radio. conns is the boot's connection list, and pass is the
// background pass, nil when every radio settled in the boot path. The
// component ends when every radio has answered and no radio has a
// supplicant left to watch, which is the case for a radio whose
// configuration was refused.
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
		}
		var settled chan *connection
		pending := 0
		if pass != nil {
			// The pass's list holds the pending entries that the
			// verdicts replace. The boot's own list is never changed,
			// so no reader of it sees a connection change under it.
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
				// A late radio's nameservers join resolv.conf the
				// same way an early one's would have: in interface
				// order, under the cap of three.
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
// STATUS. It reports false for a connection with no supplicant to
// watch, which it hands back unchanged.
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

// send hands one event to the watch, and reports false once the watch
// has ended.
func (w *radioWatch) send(e radioEvent) bool {
	select {
	case w.events <- e:
		return true
	case <-w.ctx.Done():
		return false
	}
}

// fold puts one connection into the list in place of the entry with
// its interface's name.
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
	// A failed facts write leaves the status as it was; the stderr
	// line is the only other record of the change.
	if err := w.tree.WriteNetwork(networkFacts(w.clusterDoc, w.conns)); err != nil {
		fmt.Fprintf(os.Stderr, "liken: writing facts: %v\n", err)
	}
}

// apply reads one event into the radio it is about, and reports
// whether the radio's status changed.
func (w *radioWatch) apply(e radioEvent) bool {
	i := w.index(e.ifname)
	if i < 0 {
		return false
	}
	conn := w.conns[i]
	// A bgscan scan finishes every 30 seconds while the signal is
	// weak, and its line would fill the log with nothing a person
	// needs. The connected event gets a fuller line from associate.
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
	default:
		state, message, settled := judgeWirelessEvent(e.event)
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

// addressLate addresses a radio that has just associated and has no
// address, and answers the connection with the address. A radio that
// already has an address keeps it across a move to another access
// point, because the access points of one network share its subnet.
//
// A failed addressing leaves the radio Connected with the error as its
// message, the same verdict the boot gives a radio that joined and then
// failed its DHCP. The next association tries again.
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
	// The radio's nameservers join resolv.conf the same way a radio's
	// that joined at boot would have: in interface order, under the
	// cap of three.
	w.fold(addressed)
	if err := writeResolvConf(w.conns); err != nil {
		fmt.Fprintf(os.Stderr, "liken: network: %v\n", err)
	}
	return addressed
}

// index answers the position of one interface in the list, or -1.
func (w *radioWatch) index(ifname string) int {
	for i, conn := range w.conns {
		if conn.ifname == ifname {
			return i
		}
	}
	return -1
}

// associate asks the supplicant which access point the radio holds,
// and answers the connection with that access point when it differs
// from the one in status. A supplicant that does not answer leaves the
// status as it is, because the next event or the next attach asks
// again.
func (w *radioWatch) associate(conn *connection) (*connection, bool) {
	r := conn.radio
	socket := machine.SupplicantSocket(wirelessRunDir, conn.ifname)
	association, err := machine.AskSupplicantAssociation(socket, supplicantAskPatience)
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
		// can hold the error of the addressing that followed the join.
		if next.state != machine.WirelessConnected {
			next.message = ""
		}
		// The time of association moves only on a new association. A
		// change of channel on the same access point is the same
		// association.
		if next.state != machine.WirelessConnected || next.bssid != association.BSSID {
			next.associatedAt = now
		}
		next.state = machine.WirelessConnected
		next.bssid, next.frequency = association.BSSID, association.Frequency
	}), true
}

// disconnect records that a connected radio lost its access point. The
// state goes to Associating, not NoCarrier, because the supplicant
// rejoins on its own at once, and WirelessJoined reports Associating
// as Joining, which does not mark the machine Degraded. A radio that
// was not connected, such as one that fails its handshake again and
// again, reports its disconnects on the console only.
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
	message := fmt.Sprintf("lost %s (%s%s); the supplicant is rejoining",
		from, machine.DisconnectReasonText(reason), sender)
	return withRadio(conn, func(r *radio) {
		r.state, r.message = machine.WirelessAssociating, message
		r.bssid, r.frequency, r.associatedAt = "", 0, time.Time{}
		r.lastDisconnectReason = reason
	}), true
}

// withRadio answers a copy of the connection whose radio change has
// applied. The copy leaves the old connection and its radio as they
// were, so the boot's list never sees a radio change under it.
func withRadio(conn *connection, change func(*radio)) *connection {
	next := *conn
	r := *conn.radio
	change(&r)
	next.radio = &r
	return &next
}

// declaresRadio reports whether any connection is a radio.
func declaresRadio(conns []*connection) bool {
	for _, conn := range conns {
		if conn.radio != nil {
			return true
		}
	}
	return false
}
