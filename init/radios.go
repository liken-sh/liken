package main

// Interface bring-up runs in two passes: wired interfaces settle in
// line, and radios settle behind the boot (plans/64). The split
// exists because of one hard fact about the kernel: raising a link
// holds the rtnl lock while the driver's open routine runs, and a
// wedged driver never returns, holds the lock forever, and cannot be
// killed. On 2026-08-26 exactly that took a machine down. Its wired
// path was healthy, but the boot waited on the radio, so nothing
// that could act ever started, and every following boot repeated the
// wait. No timeout can contain a stuck kernel thread. What the boot
// controls is what it risks before the machine can act, so the
// radios go last, and on a machine that does not need them, they go
// in the background.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/vishvananda/netlink"

	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/machine"
)

// How long the boot waits for a raise to return. A healthy radio
// raises in under two seconds, most of it firmware load. A raise
// still out at ten seconds is a kernel thread that is not coming
// back, and the wait exists only so the boot can say so; waiting
// longer would report the same thing later.
const raisePatience = 10 * time.Second

// interfacePasses is the pass split with its moving parts as
// function values, so a test can run the whole split, the route
// question included, with no kernel, no radio, and no supplicant.
// The seams follow startSupplicant and parkConsole: the real boot
// binds the real functions once, in bootPasses below.
type interfacePasses struct {
	wired     func(machine.InterfaceSpec) (*connection, error)
	radio     func(machine.InterfaceSpec) (*connection, error)
	route     routeLookup
	ciphers   func()
	readdress func([]*connection, []machine.InterfaceSpec, *radio) []*connection
}

// bootPasses binds the passes a real boot runs.
func bootPasses(present []interfaceIdentity) interfacePasses {
	return interfacePasses{
		wired: func(ifc machine.InterfaceSpec) (*connection, error) {
			return bringUpInterface(ifc, present)
		},
		radio: func(ifc machine.InterfaceSpec) (*connection, error) {
			return bringUpRadio(ifc, present, raisePatience)
		},
		route:     routeVia,
		ciphers:   loadWirelessCiphers,
		readdress: readdressRadio,
	}
}

// radioPass is what pass two hands the rest of the boot: the
// connection list as status should report it now, and a channel that
// delivers each radio's settled connection as it lands.
type radioPass struct {
	conns   []*connection
	settled chan *connection

	// pending counts the radios that have not answered yet. Every
	// radio sends exactly one verdict, so the handler that drains the
	// channel knows when its work is over.
	pending int
}

// run is the whole bring-up: pass one in spec order, then the gate
// below decides where pass two goes. A machine that can already
// reach its cluster backgrounds the radios and boots; a machine
// whose only declared path is a radio waits for it in the
// foreground, park included, because it has nothing to do without
// it.
func (p interfacePasses) run(interfaces []machine.InterfaceSpec, clusterDoc *cluster.Cluster) ([]*connection, *radioPass) {
	var conns []*connection
	var radios []machine.InterfaceSpec
	for _, ifc := range interfaces {
		if ifc.Wireless != nil {
			radios = append(radios, ifc)
			continue
		}
		conn, err := p.wired(ifc)
		if err != nil {
			fmt.Fprintf(os.Stderr, "liken: network: %s: %v\n", ifc.Name, err)
			continue
		}
		conns = append(conns, conn)
	}
	if len(radios) == 0 {
		return conns, nil
	}

	// The ciphers load before any supplicant starts, and only on a
	// boot that a declared radio brought this far (ciphers.go).
	p.ciphers()

	if p.backgroundable(conns, clusterDoc) {
		return p.background(interfaces, conns, radios)
	}
	return p.foreground(interfaces, conns, radios, clusterDoc.EndpointOrEmpty()), nil
}

// backgroundable answers whether the boot may go on without its
// radios. Two things must hold: pass one gives a route toward the
// cluster's endpoint, and pass one holds the address k3s will start
// with. nodeAddress (k3s.go) picks that address, so the gate asks it
// rather than restating the rule.
//
// The second condition exists because k3s registers with the node
// address at start and never re-reads it. A machine whose nodeCIDR
// only the radio can answer must wait for the radio, or k3s starts
// with no node IP and the Node and the status disagree about the
// machine's address. A cluster that declares no nodeCIDR has no
// address rule to check, so the route question decides alone.
func (p interfacePasses) backgroundable(conns []*connection, clusterDoc *cluster.Cluster) bool {
	if routed, _ := routeToward(conns, clusterDoc.EndpointOrEmpty(), p.route); !routed {
		return false
	}
	if !declaresNodeCIDR(clusterDoc) {
		return true
	}
	ip, _ := nodeAddress(clusterDoc, conns)
	return ip != ""
}

// background starts pass two behind the boot. The machine can
// already act, so nothing after this line waits on a radio, and the
// worst a wedged driver can do is what it does anyway; the operator
// it cannot stop is the recovery path.
func (p interfacePasses) background(interfaces []machine.InterfaceSpec, conns []*connection,
	radios []machine.InterfaceSpec) ([]*connection, *radioPass) {
	for _, ifc := range radios {
		conns = append(conns, pendingRadio(ifc))
	}
	conns = inSpecOrder(interfaces, conns)

	pass := &radioPass{settled: make(chan *connection, len(radios)), pending: len(radios)}
	// The pass copies the list before the boot goes on with it. The
	// verdict handler replaces entries in its own copy, so no reader
	// of the boot's list ever sees a connection change under it.
	pass.conns = append([]*connection(nil), conns...)

	plane.start("the wireless bring-up", p.joinRadios(pass, radios))
	return conns, pass
}

// joinRadios is the component behind the boot: every declared radio,
// one at a time, each joined at most once.
//
// The radios are serial because a raise holds the kernel's rtnl lock
// while the driver runs. Two raises at once means the second waits on
// the first inside the kernel, where no deadline reaches it, so the
// first wedge would take every other radio's report with it. After a
// wedge the rest are not attempted at all, and each says so.
//
// The claim flags exist because the machine plane restarts a
// component whose function returns an error, and a panic inside a
// join becomes exactly that. Each radio is claimed before its join
// runs, so a restarted component joins none of them again, starts no
// second supplicant, and sends no second verdict.
func (p interfacePasses) joinRadios(pass *radioPass, radios []machine.InterfaceSpec) func(context.Context) error {
	claimed := make([]atomic.Bool, len(radios))
	return func(context.Context) error {
		stuck := ""
		for i, ifc := range radios {
			if !claimed[i].CompareAndSwap(false, true) {
				continue
			}
			if stuck != "" {
				pass.settled <- notAttemptedRadio(ifc, stuck)
				continue
			}
			fmt.Printf("liken: wireless: %s joins %s in the background\n", ifc.Name, ifc.Wireless.SSID)
			conn := p.joinOne(ifc)
			if conn.radio != nil && conn.radio.state == machine.WirelessNotRaised {
				stuck = ifc.Name
			}
			pass.settled <- conn
		}
		return nil
	}
}

// foreground runs pass two in the boot path, for the machine whose
// only declared path is its radio. This is the one machine the radio
// is allowed to hold, and the park rule is unchanged from plan 62.
func (p interfacePasses) foreground(interfaces []machine.InterfaceSpec, conns []*connection,
	radios []machine.InterfaceSpec, endpoint string) []*connection {
	for _, ifc := range radios {
		conns = append(conns, p.joinOne(ifc))
	}
	conns = inSpecOrder(interfaces, conns)

	// The park (plans/completed/62-wifi.md). Every interface has settled by
	// this line, so the decision has everything it needs. The hold
	// ends on a join event rather than a keypress, and the
	// addressing that follows is the same addressing a radio that
	// joined on the first try would have taken.
	if failed, reason := parkDecision(conns, endpoint, p.route); failed != nil {
		park(failed, reason)
		conns = p.readdress(conns, radios, failed)
	}
	return conns
}

// inSpecOrder puts the connections back into the order the spec
// declared, whichever pass produced each one. The order is not
// cosmetic: resolv.conf keeps the first three nameservers in
// interface order, and the facts summarize the first addressed
// interface, so a radio the spec named first must appear first.
//
// A wired interface that failed its bring-up has no connection, so
// it simply has no place in the result; the map lookup skips it.
func inSpecOrder(interfaces []machine.InterfaceSpec, conns []*connection) []*connection {
	byName := make(map[string]*connection, len(conns))
	for _, conn := range conns {
		byName[conn.ifname] = conn
	}
	ordered := make([]*connection, 0, len(conns))
	for _, ifc := range interfaces {
		if conn, ok := byName[ifc.Name]; ok {
			ordered = append(ordered, conn)
		}
	}
	return ordered
}

// joinOne is one radio's whole bring-up. It returns a connection in
// every case, because a radio that failed is a report the status
// must carry, never a missing interface.
func (p interfacePasses) joinOne(ifc machine.InterfaceSpec) *connection {
	conn, err := p.radio(ifc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: network: %s: %v\n", ifc.Name, err)
		return failedRadio(ifc, err)
	}
	return conn
}

// pendingRadio is the connection status reports while pass two still
// works on the radio: associating, no address yet. The verdict
// replaces it.
func pendingRadio(ifc machine.InterfaceSpec) *connection {
	return &connection{ifname: ifc.Name, radio: &radio{
		ifname: ifc.Name, ssid: ifc.Wireless.SSID, state: machine.WirelessAssociating,
	}}
}

// failedRadio is the connection for a radio whose bring-up errored
// before the join could say anything better, for example a port the
// machine does not have. The interface still owes status a reason,
// and NoCarrier with the error's text is the closest true statement.
// A radio that joined and then failed its addressing never comes
// here; it keeps its Connected verdict with the error as its
// message.
func failedRadio(ifc machine.InterfaceSpec, err error) *connection {
	return &connection{ifname: ifc.Name, radio: &radio{
		ifname: ifc.Name, ssid: ifc.Wireless.SSID,
		state: machine.WirelessNoCarrier, message: err.Error(),
	}}
}

// notAttemptedRadio is the verdict for a radio the pass never tried,
// because an earlier radio's raise did not return. The state is
// NoCarrier, not NotRaised: NotRaised means this radio's own raise
// is still out, and a radio nothing touched must not claim that.
// The message names the interface actually at fault.
func notAttemptedRadio(ifc machine.InterfaceSpec, stuck string) *connection {
	return &connection{ifname: ifc.Name, radio: &radio{
		ifname: ifc.Name, ssid: ifc.Wireless.SSID,
		state: machine.WirelessNoCarrier,
		message: fmt.Sprintf("not attempted; raising %s is stuck holding the netlink lock every interface needs",
			stuck),
	}}
}

// joinRadio is the 802.11 session, a variable holding the real join
// below it, so a test can drive the addressing that follows a join
// with no radio and no supplicant. It is the seam startSupplicant
// and parkConsole already are.
var joinRadio = joinWireless

// bringUpRadio is the wireless half of bringUpInterface, moved here
// so the raise can run under a deadline: raise, join, then address
// exactly as a wired port would.
func bringUpRadio(ifc machine.InterfaceSpec, present []interfaceIdentity, patience time.Duration) (*connection, error) {
	link, err := raiseUnderDeadline(ifc, present, patience)
	if err != nil {
		var stuck raiseStuck
		if !errors.As(err, &stuck) {
			return nil, err
		}
		fmt.Fprintf(os.Stderr, "liken: wireless: %s: %v\n", ifc.Name, err)
		return &connection{ifname: ifc.Name, radio: &radio{
			ifname: ifc.Name, ssid: ifc.Wireless.SSID,
			state: machine.WirelessNotRaised, message: err.Error(),
		}}, nil
	}

	// The join comes before the addressing because an unassociated
	// radio carries no frames: a DHCP exchange on it would only wait
	// out its own deadline. Once the radio associates, the interface
	// behaves exactly like an ethernet port, which is why the same
	// two addressing paths run below with nothing added.
	r := joinRadio(ifc, machine.MachineStateDir)
	if r.state != machine.WirelessConnected {
		// A failed join still yields a connection. The interface
		// exists, the status must carry the reason it has no
		// address, and the park decision reads these same
		// connections to learn what settled.
		return &connection{ifname: ifc.Name, mac: link.Attrs().HardwareAddr, radio: r}, nil
	}
	conn, err := addressInterface(link, ifc)
	if err != nil {
		// The join happened, so Connected stands; reporting
		// NoCarrier here would blame the radio for a DHCP failure.
		// The address error is the message, and the missing address
		// on the interface says the rest.
		fmt.Fprintf(os.Stderr, "liken: network: %s: %v\n", ifc.Name, err)
		r.message = err.Error()
		return &connection{ifname: ifc.Name, mac: link.Attrs().HardwareAddr, radio: r}, nil
	}
	conn.radio = r
	return conn, nil
}

// raiseStuck is a raise that never returned, kept apart from a
// refusal the kernel gave. A refusal is an answer; this is the
// absence of one, and it gets its own wireless state because no
// supplicant event will ever follow it.
type raiseStuck struct {
	ifname   string
	patience time.Duration
}

// The message states only what init observed: the call has not come
// back. Which lock the stuck thread holds is not observable from
// here, and a guess would land in status as a fact.
func (s raiseStuck) Error() string {
	return fmt.Sprintf("raising %s did not return in %s; the netlink call is still out and nothing can cancel it",
		s.ifname, s.patience)
}

// raiseUnderDeadline opens one link and raises it, and stops waiting
// after the patience runs out. The goroutine is abandoned then, not
// cancelled, because nothing can cancel a thread that is stuck inside
// the kernel; the report is the whole remedy. A raise that returns
// after the deadline lands its answer in a buffered channel that
// nothing reads again, which is the cheapest true accounting of it.
//
// The link lookup runs inside the deadline too, because it is also a
// netlink call: once an earlier raise wedges the kernel's rtnl lock,
// the lookup blocks the same way the raise does, and a deadline that
// covered only the raise would hang before reaching it.
func raiseUnderDeadline(ifc machine.InterfaceSpec, present []interfaceIdentity,
	patience time.Duration) (netlink.Link, error) {
	type raised struct {
		link netlink.Link
		err  error
	}
	done := make(chan raised, 1)
	// The seams bind before the goroutine starts, because the
	// goroutine can outlive this function and a test must not have
	// its stand-ins swapped out mid-raise.
	byName, setUp := linkByName, linkSetUp
	go func() {
		if err := requirePort(ifc.Name, present); err != nil {
			done <- raised{err: err}
			return
		}
		link, err := byName(ifc.Name)
		if err != nil {
			done <- raised{err: fmt.Errorf("opening interface %q: %w", ifc.Name, err)}
			return
		}
		fmt.Printf("liken: bringing up %s\n", ifc.Name)
		if err := setUp(link); err != nil {
			done <- raised{err: fmt.Errorf("raising %s: %w", ifc.Name, err)}
			return
		}
		done <- raised{link: link}
	}()
	select {
	case out := <-done:
		return out.link, out.err
	case <-time.After(patience):
		return nil, raiseStuck{ifname: ifc.Name, patience: patience}
	}
}

// publishRadioVerdicts is the machine-plane component that carries
// pass two's verdicts into the world. It starts after
// publishBootFacts, because publishBootFacts writes the network
// subtree itself and would overwrite an earlier verdict with the
// pending state it replaced (main.go places the start).
func publishRadioVerdicts(pass *radioPass, tree machine.FactsTree, clusterDoc *cluster.Cluster) func(context.Context) error {
	return func(ctx context.Context) error {
		// The component ends when every declared radio has answered
		// once. The supplicant's supervision owns the session after
		// that; this component only lands the boot's verdicts.
		for settled := 0; settled < pass.pending; {
			select {
			case <-ctx.Done():
				return nil
			case conn := <-pass.settled:
				settled++
				pass.fold(conn)
				conn.report()
				// A failed facts write means the status still shows
				// the pending state; the stderr line is the only
				// other record that the verdict existed.
				if err := tree.WriteNetwork(networkFacts(clusterDoc, pass.conns)); err != nil {
					fmt.Fprintf(os.Stderr, "liken: writing facts: %v\n", err)
				}
				// A late radio's nameservers join resolv.conf the
				// same way an early one's would have: in interface
				// order, under the cap of three.
				if err := writeResolvConf(pass.conns); err != nil {
					fmt.Fprintf(os.Stderr, "liken: network: %v\n", err)
				}
			}
		}
		return nil
	}
}

// fold replaces the pending entry the pass has been reporting with
// the settled connection. The match is by interface name, and every
// radio has a pending entry, so the append is a guard, not a path.
func (p *radioPass) fold(conn *connection) {
	for i, existing := range p.conns {
		if existing.ifname == conn.ifname {
			p.conns[i] = conn
			return
		}
	}
	p.conns = append(p.conns, conn)
}
