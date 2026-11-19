package main

// Interface bring-up runs in two passes. Pass one brings up the wired
// interfaces and finishes before the boot goes on. Pass two brings up
// the radios. It runs in the background when a wired interface has a
// route to the cluster and, when the cluster declares a nodeCIDR, a
// wired interface has an address inside it (backgroundable).
//
// Raising a link holds the kernel's rtnl lock while the driver's open
// routine runs. A wedged driver never returns from that routine, so it
// holds the lock forever, and no timeout can kill a kernel thread. If
// the boot waited for such a radio, nothing that could repair the
// machine would start, and every later boot would wait the same way.
// So the boot raises radios last, and waits for one only when it has
// no other path to the cluster.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"sync/atomic"
	"time"

	"github.com/vishvananda/netlink"

	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

// raiseTimeout is how long a raise may take before init reports the
// radio as stuck. Waiting longer does not help, because a raise that
// has not returned by then is a kernel thread that is not coming back.
const raiseTimeout = 10 * time.Second

// interfacePasses has the steps of the two passes as functions, so a
// test can run both passes with no kernel, no radio, and no supplicant.
type interfacePasses struct {
	wired     func(machine.InterfaceSpec) (*connection, error)
	radio     func(machine.InterfaceSpec) (*connection, error)
	route     routeLookup
	ciphers   func()
	readdress func([]*connection, []machine.InterfaceSpec, *radio) []*connection
}

func bootPasses(present []interfaceIdentity) interfacePasses {
	return interfacePasses{
		wired: func(ifc machine.InterfaceSpec) (*connection, error) {
			return bringUpInterface(ifc, present)
		},
		radio: func(ifc machine.InterfaceSpec) (*connection, error) {
			return bringUpRadio(ifc, present, raiseTimeout)
		},
		route:     routeVia,
		ciphers:   loadWirelessCiphers,
		readdress: readdressRadio,
	}
}

// radioPass is the state of a pass two that runs in the background.
// conns is the connection list that watchRadios publishes to status.
// Each radio sends its final connection on settled once, and
// watchRadios replaces that radio's entry in conns with it.
type radioPass struct {
	conns   []*connection
	settled chan *connection

	// pending is the number of radios that have not sent on settled.
	// watchRadios stops reading the channel when it reaches zero.
	pending int
}

// run brings up the wired interfaces first and then the radios, and
// returns the connections in spec order. When backgroundable reports
// that the boot can go on without the radios, run starts them in the
// background and returns a radioPass. Otherwise it brings them up
// before it returns, because the machine has nothing to do without the
// network.
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

	// The supplicant needs the wireless cipher modules. A machine with
	// no radio never loads them.
	p.ciphers()

	if p.backgroundable(conns, clusterDoc) {
		return p.background(interfaces, conns, radios)
	}
	return p.foreground(interfaces, conns, radios, clusterDoc.EndpointOrEmpty()), nil
}

// backgroundable reports whether the boot can go on without its
// radios. It can when the wired interfaces have a route toward the
// cluster's endpoint and, when the cluster declares a nodeCIDR, one of
// them has an address inside it.
//
// k3s reads the node address once, when it starts. If only the radio
// had an address inside the nodeCIDR, k3s would start with no node IP,
// and the Node and the Machine status would disagree about the
// machine's address.
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

// background starts pass two in the machine plane (components.go) and
// returns at once, with a pendingRadio connection in place of each
// radio. Nothing after this waits for a radio, so a wedged driver
// cannot stop the boot or the operator that repairs the machine.
func (p interfacePasses) background(interfaces []machine.InterfaceSpec, conns []*connection,
	radios []machine.InterfaceSpec) ([]*connection, *radioPass) {
	for _, ifc := range radios {
		conns = append(conns, pendingRadio(ifc))
	}
	conns = inSpecOrder(interfaces, conns)

	pass := &radioPass{settled: make(chan *connection, len(radios)), pending: len(radios)}
	// The pass has its own copy of the list, because watchRadios
	// replaces entries in it while the boot reads conns.
	pass.conns = append([]*connection(nil), conns...)

	plane.start("the wireless bring-up", p.joinRadios(pass, radios))
	return conns, pass
}

// joinRadios returns the machine plane component that brings up each
// radio in turn and sends each result on pass.settled.
//
// The radios come up one at a time because a raise holds the rtnl
// lock. A second raise would block inside the kernel behind a wedged
// first one, where no deadline reaches it, and its result would never
// arrive. So after one raise is stuck, joinRadios tries no other radio
// and sends a notAttemptedRadio connection for each.
//
// The machine plane runs a component again after it returns an error
// or panics. The claimed flags make a second run skip each radio that
// an earlier run started, so it starts no second supplicant and sends
// no second result. A second run would then send no result at all for
// a radio whose bring-up panicked, and the radio watch would wait for
// it until the next boot, so joinOneOrReportPanic turns that panic into
// the radio's result.
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
			conn := p.joinOneOrReportPanic(ifc)
			if conn.radio != nil && conn.radio.state == machine.WirelessNotRaised {
				stuck = ifc.Name
			}
			pass.settled <- conn
		}
		return nil
	}
}

func (p interfacePasses) foreground(interfaces []machine.InterfaceSpec, conns []*connection,
	radios []machine.InterfaceSpec, endpoint string) []*connection {
	for _, ifc := range radios {
		conns = append(conns, p.joinOne(ifc))
	}
	conns = inSpecOrder(interfaces, conns)

	// When a radio failed in a way that retrying will not fix and no
	// interface has a route to the cluster, waitForRadio blocks until the
	// supplicant reports a join or its control socket closes. When the
	// radio has no supplicant, waitForRadio never returns. If the radio
	// joined, readdress gives it an address.
	if failed, reason := radioToWaitFor(conns, endpoint, p.route); failed != nil {
		waitForRadio(failed, reason)
		conns = p.readdress(conns, radios, failed)
	}
	return conns
}

// inSpecOrder sorts the connections into the order of the spec, so a
// radio that the spec lists first comes first. The order matters
// because resolv.conf keeps only the first three nameservers, and the
// Machine status summarizes the first interface with an address when
// no interface has the node address.
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

// joinOneOrReportPanic brings up one radio, and returns a failedRadio
// connection that names the panic when the bring-up panics.
func (p interfacePasses) joinOneOrReportPanic(ifc machine.InterfaceSpec) (conn *connection) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "liken: network: %s: the bring-up panicked: %v\n%s", ifc.Name, r, debug.Stack())
			conn = failedRadio(ifc, fmt.Errorf("the bring-up panicked: %v", r))
		}
	}()
	return p.joinOne(ifc)
}

// joinOne brings up one radio. It returns a connection even when the
// bring-up fails, so status shows the failure.
func (p interfacePasses) joinOne(ifc machine.InterfaceSpec) *connection {
	conn, err := p.radio(ifc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: network: %s: %v\n", ifc.Name, err)
		return failedRadio(ifc, err)
	}
	return conn
}

// pendingRadio is the connection that status shows for a radio until
// pass two sends its result.
func pendingRadio(ifc machine.InterfaceSpec) *connection {
	return &connection{ifname: ifc.Name, radio: &radio{
		ifname: ifc.Name, ssid: ifc.Wireless.SSID, state: machine.WirelessAssociating,
	}}
}

// failedRadio is the connection for a radio whose bring-up returned
// an error before the join, for example because the machine has no
// such interface. A radio that joined and then failed to get an
// address stays Connected, and bringUpRadio returns no error for it.
func failedRadio(ifc machine.InterfaceSpec, err error) *connection {
	return &connection{ifname: ifc.Name, radio: &radio{
		ifname: ifc.Name, ssid: ifc.Wireless.SSID,
		state: machine.WirelessNoCarrier, message: err.Error(),
	}}
}

// notAttemptedRadio is the connection for a radio that joinRadios
// skipped because an earlier radio's raise did not return. Its state
// is NoCarrier and not NotRaised, because NotRaised means that this
// radio's own raise is still running.
func notAttemptedRadio(ifc machine.InterfaceSpec, stuck string) *connection {
	return &connection{ifname: ifc.Name, radio: &radio{
		ifname: ifc.Name, ssid: ifc.Wireless.SSID,
		state: machine.WirelessNoCarrier,
		message: fmt.Sprintf("not attempted, because raising %s has not returned and can still hold the kernel rtnl lock that every interface needs",
			stuck),
	}}
}

// joinRadio joins the radio to its network. It is a variable so that
// a test can run the addressing that follows a join with no radio and
// no supplicant.
var joinRadio = joinWireless

// bringUpRadio raises the link under a deadline, joins the network,
// and then addresses the interface the way bringUpInterface addresses
// a wired one. A raise that does not return within the timeout gives a
// connection in the NotRaised state, not an error.
func bringUpRadio(ifc machine.InterfaceSpec, present []interfaceIdentity, timeout time.Duration) (*connection, error) {
	link, err := raiseUnderDeadline(ifc, present, timeout)
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

	// A radio that is not associated sends no frames, so DHCP before
	// the join would only wait until its own deadline.
	r := joinRadio(ifc, machine.MachineStateDir)
	if r.state != machine.WirelessConnected {
		// A failed join still returns a connection, so that status
		// shows why the interface has no address and radioToWaitFor
		// can read the radio's state.
		return &connection{ifname: ifc.Name, mac: link.Attrs().HardwareAddr, radio: r}, nil
	}
	conn, err := addressInterface(link, ifc)
	if err != nil {
		// The radio did join, so its state stays Connected. NoCarrier
		// would blame the radio for an addressing failure.
		fmt.Fprintf(os.Stderr, "liken: network: %s: %v\n", ifc.Name, err)
		r.message = err.Error()
		return &connection{ifname: ifc.Name, mac: link.Attrs().HardwareAddr, radio: r}, nil
	}
	conn.radio = r
	return conn, nil
}

// raiseStuck is the error for a raise that has not returned within
// the timeout. bringUpRadio turns it into the NotRaised state, not
// NoCarrier, because no supplicant event will ever follow it.
type raiseStuck struct {
	ifname  string
	timeout time.Duration
}

// The message says only what init observed, that the call has not
// returned. init cannot see which lock the stuck thread holds, and a
// guess would appear in status as a fact.
func (s raiseStuck) Error() string {
	return fmt.Sprintf("raising %s did not return in %s. The netlink call is still running, and it cannot be canceled",
		s.ifname, s.timeout)
}

// raiseUnderDeadline looks up one link and raises it. When that has
// not finished within the timeout, it returns a raiseStuck error and
// leaves the goroutine running, because nothing can cancel a thread
// that is stuck in the kernel. If the call returns later, its result
// goes to a buffered channel that nobody reads.
//
// The link lookup is a netlink call too, so it runs under the same
// deadline. After an earlier raise wedges the rtnl lock, the lookup
// blocks, and a deadline around only the raise would never start.
func raiseUnderDeadline(ifc machine.InterfaceSpec, present []interfaceIdentity,
	timeout time.Duration) (netlink.Link, error) {
	type raised struct {
		link netlink.Link
		err  error
	}
	done := make(chan raised, 1)
	// Read the variables before the goroutine starts. The goroutine
	// can outlive this function, and a test may replace the
	// variables after it returns.
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
	case <-time.After(timeout):
		return nil, raiseStuck{ifname: ifc.Name, timeout: timeout}
	}
}
