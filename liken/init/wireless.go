package main

// Init sets up each wireless interface at boot. wpa_supplicant runs the
// 802.11 session: association, keys, and rekeying. After the radio
// associates, init gives the interface its address with the same DHCP
// or static code as a wired port. Init reads the supplicant's events
// from its control socket, because those events tell a wrong
// passphrase apart from an access point that does not answer.

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/machine"
)

// supplicantBinary is a static build from buildroot, with the features
// that userland/wpa_supplicant.config selects.
const supplicantBinary = "/sbin/wpa_supplicant"

// wirelessRunDir holds the supplicant's configuration and control
// sockets. It is on /run, a tmpfs, because the configuration contains
// the passphrase and must not be written to a disk. worldreport.go
// mounts /run with the essential filesystems, because a radio comes up
// long before k3s does. Tests point it at a temporary directory.
var wirelessRunDir = machine.WirelessDir

// associationTimeout is how long awaitAssociation waits for the radio
// to associate. A scan of both bands and the four-way handshake take a
// few seconds on a working network, so 45 seconds leaves a wide margin.
const associationTimeout = 45 * time.Second

// radio is the state of one wireless interface: the SSID from the
// manifest, the result of the join, and the control client that stays
// attached to the supplicant for the rest of the boot.
type radio struct {
	ifname string
	ssid   string

	// state and message are the result of the join. They are reported
	// in the machine's facts and, from there, in the Machine status.
	state   machine.WirelessState
	message string

	// The access point that the radio is associated with, read from
	// the supplicant's reply to STATUS, and the time it associated.
	// The radio watch updates them (radiowatch.go).
	bssid        string
	frequency    int
	associatedAt time.Time

	// lastDisconnectReason is the 802.11 reason code of the radio's
	// most recent disconnect, or zero if it has not disconnected.
	lastDisconnectReason int

	// control is the client attached to the supplicant's control
	// socket. It stays open after the join, so a boot that waits in
	// waitForRadio returns when the radio joins later.
	control *wpaControl
}

// deterministic reports whether the radio failed in a way that waiting
// does not fix: the access point refused the passphrase, or init could
// not build a supplicant configuration from the manifest and the
// passphrase file. Both set WrongKey. Every other failure looks the
// same as an access point that has not answered yet.
func (r *radio) deterministic() bool {
	return r.state == machine.WirelessWrongKey
}

func (r *radio) wirelessStatus() *machine.WirelessStatus {
	w := &machine.WirelessStatus{
		SSID: r.ssid, State: r.state, Message: r.message,
		BSSID: r.bssid, Frequency: r.frequency, Band: machine.WirelessBand(r.frequency),
		LastDisconnectReason: r.lastDisconnectReason,
	}
	if !r.associatedAt.IsZero() {
		at := r.associatedAt
		w.AssociatedAt = &at
	}
	return w
}

// joinWireless generates the supplicant's configuration, starts the
// supplicant under supervision, and waits for the radio to associate.
// It always returns a radio, including when the join fails, because
// the status has to report that failure.
//
// It does not touch rfkill. The kernel starts radios unblocked, a soft
// block does not survive a reboot, and nothing in liken sets one. A
// hardware kill switch makes the radio never associate, and no
// software unblock can clear that.
func joinWireless(ifc machine.InterfaceSpec, stateRoot string) *radio {
	w := *ifc.Wireless
	r := &radio{ifname: ifc.Name, ssid: w.SSID, state: machine.WirelessAssociating}

	config, err := wirelessConfig(w, stateRoot, controlSocketDir(ifc.Name))
	if err != nil {
		r.state = machine.WirelessWrongKey
		r.message = err.Error()
		fmt.Fprintf(os.Stderr, "liken: wireless: %s: %v\n", ifc.Name, err)
		return r
	}

	path, err := writeWirelessConfig(ifc.Name, config)
	if err != nil {
		r.state = machine.WirelessNoCarrier
		r.message = err.Error()
		fmt.Fprintf(os.Stderr, "liken: wireless: %s: %v\n", ifc.Name, err)
		return r
	}

	fmt.Printf("liken: wireless: %s joins %s\n", ifc.Name, w.SSID)
	control, err := superviseSupplicant(ifc.Name, path)
	if err != nil {
		r.state = machine.WirelessNoCarrier
		r.message = err.Error()
		fmt.Fprintf(os.Stderr, "liken: wireless: %s: %v\n", ifc.Name, err)
		return r
	}
	r.control = control

	awaitAssociation(r)
	return r
}

// awaitAssociation reads the supplicant's events until one settles the
// join or associationTimeout runs out, and records the result on the
// radio.
//
// A scan that finds no network does not settle the join, because an
// access point that is off, rebooting, or out of range must never make
// a boot wait (see radioToWaitFor). awaitAssociation still prints every
// event, so a person at the console can see what the radio does.
func awaitAssociation(r *radio) {
	deadline := time.After(associationTimeout)
	for {
		select {
		case event, ok := <-r.control.events():
			if !ok {
				r.state = machine.WirelessNoCarrier
				r.message = "the supplicant's control socket closed before the radio associated"
				return
			}
			if line := describeWirelessEvent(r.ifname, event); line != "" {
				fmt.Println(line)
			}
			if state, message, settled := classifyWirelessEvent(event); settled {
				r.state, r.message = state, message
				return
			}
		case <-deadline:
			r.state = machine.WirelessNoCarrier
			r.message = fmt.Sprintf("no access point answered for %s, and the supplicant keeps trying", associationTimeout)
			fmt.Fprintf(os.Stderr, "liken: wireless: %s: %s\n", r.ifname, r.message)
			return
		}
	}
}

// controlSocketDir returns the directory for one interface's control
// socket. The supplicant names the socket after the interface, and a
// directory per interface keeps two interfaces' sockets apart.
func controlSocketDir(ifname string) string {
	return filepath.Join(wirelessRunDir, ifname, "ctrl")
}

// writeWirelessConfig writes one interface's supplicant configuration
// under wirelessRunDir and returns its path. The mode is 0600 because
// the file contains the passphrase.
func writeWirelessConfig(ifname, config string) (string, error) {
	dir := filepath.Join(wirelessRunDir, ifname)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("preparing the supplicant's directory for %s: %w", ifname, err)
	}
	path := filepath.Join(dir, "wpa_supplicant.conf")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		return "", fmt.Errorf("writing the supplicant configuration of %s: %w", ifname, err)
	}
	return path, nil
}

// supplicants lists the supervised supplicants, so the shutdown can
// stop them before it signals the other processes. supplicantsMu
// guards the list, because a radio that joins in the background
// registers its supplicant from the radio component's goroutine, while
// the boot and the shutdown run on others. supplicantsStopping is set
// when the shutdown takes the list. A supplicant started after that
// would never be stopped, so registerSupplicant refuses it.
var (
	supplicantsMu       sync.Mutex
	supplicants         []*supplicantProcess
	supplicantsStopping bool
)

// registerSupplicant adds a supplicant to the list that the shutdown
// stops, or returns false when the shutdown has already taken the
// list.
func registerSupplicant(p *supplicantProcess) bool {
	supplicantsMu.Lock()
	defer supplicantsMu.Unlock()
	if supplicantsStopping {
		return false
	}
	supplicants = append(supplicants, p)
	return true
}

// supplicantProcess is the supervision of one interface's supplicant.
// Closing stop ends the supervision loop, which then closes done.
type supplicantProcess struct {
	ifname string
	stop   chan struct{}
	done   chan struct{}

	// control is the control client. The loop attaches it again after
	// each restart, because a supplicant keeps its list of attached
	// clients in memory, and a new process sends events to no one
	// until a client attaches.
	control *wpaControl

	// backoff is the first restart delay and maxBackoff is the longest.
	// The delay doubles at each restart, and returns to backoff when a
	// process ran for over a minute before it died. Tests set short
	// delays.
	backoff    time.Duration
	maxBackoff time.Duration
}

// superviseSupplicant starts the supplicant for one interface, restarts
// it whenever it exits, and returns a client attached to its control
// socket.
//
// It supervises the way superviseK3s does: the reaper collects each
// exit, and the loop reads it from the death registry. A dead
// supplicant drops the association, so the loop starts a new one. The
// address stays on the interface across the restart, so the machine
// does not reboot.
func superviseSupplicant(ifname, config string) (*wpaControl, error) {
	dir := controlSocketDir(ifname)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating the control socket directory of %s: %w", ifname, err)
	}

	proc, err := startSupplicant(ifname, config)
	if err != nil {
		return nil, err
	}
	control, err := attachWPAControl(filepath.Join(dir, ifname))
	if err != nil {
		// Nothing supervises this supplicant yet, so stop it and wait
		// for its exit here. The death registry keeps the exit status
		// of a process that nobody awaits, and a later child that
		// reuses the pid, such as k3s, would read it as its own exit.
		endSupplicant(ifname, proc)
		return nil, fmt.Errorf("attaching to the control socket of the supplicant on %s: %w", ifname, err)
	}

	p := &supplicantProcess{
		ifname: ifname, control: control,
		stop: make(chan struct{}), done: make(chan struct{}),
		backoff: time.Second, maxBackoff: 30 * time.Second,
	}
	if !registerSupplicant(p) {
		control.close()
		endSupplicant(ifname, proc)
		return nil, fmt.Errorf("the shutdown has started, so the supplicant on %s was stopped instead of supervised", ifname)
	}
	plane.start("the supplicant on "+ifname, func(ctx context.Context) error {
		p.run(ctx, proc, config)
		return nil
	})
	return control, nil
}

// endSupplicant stops a supplicant that no supervision loop owns and
// waits for its exit, so the death registry keeps nothing for its pid.
func endSupplicant(ifname string, proc runningSupplicant) {
	died := make(chan unix.WaitStatus, 1)
	go func() { died <- deaths.await(proc.pid) }()
	stopSupplicant(ifname, proc, died)
}

// supplicantOutcome is the reason a wait in the supervision loop
// ended.
type supplicantOutcome int

const (
	supplicantDied   supplicantOutcome = iota // the process exited on its own
	supplicantEnded                           // stop or the context ended the loop
	supplicantWaited                          // the delay elapsed first
)

// await waits for the process to exit, for stop or the context to end
// the loop, or for delay to elapse, whichever happens first. A nil
// died channel means there is no process, and a nil delay means no
// time limit.
//
// A stop calls stopSupplicant only when died is not nil. Between a
// death and the next start there is no process, and stopping the dead
// one would wait forever for an exit that the reaper already reported.
func (p *supplicantProcess) await(ctx context.Context, proc runningSupplicant,
	died <-chan unix.WaitStatus, delay <-chan time.Time) supplicantOutcome {
	select {
	case status := <-died:
		fmt.Printf("liken: wireless: the supplicant on %s exited (%s)\n", p.ifname, describeExit(status))
		return supplicantDied
	case <-p.stop:
		if died != nil {
			stopSupplicant(p.ifname, proc, died)
		}
		return supplicantEnded
	case <-ctx.Done():
		if died != nil {
			stopSupplicant(p.ifname, proc, died)
		}
		return supplicantEnded
	case <-delay:
		return supplicantWaited
	}
}

// next returns the delay after backoff: double it, up to maxBackoff.
func (p *supplicantProcess) next(backoff time.Duration) time.Duration {
	if backoff >= p.maxBackoff {
		return p.maxBackoff
	}
	return backoff * 2
}

// run supervises one supplicant until stop closes or ctx ends. It
// alternates between watch, while a process runs, and restart, after a
// death, when there is no process to signal or wait on.
func (p *supplicantProcess) run(ctx context.Context, proc runningSupplicant, config string) {
	defer close(p.done)
	backoff := p.backoff
	attached := true
	for {
		started := time.Now()
		died := make(chan unix.WaitStatus, 1)
		// The pid is a parameter because the loop reassigns proc, and
		// the goroutine could otherwise read the next process's pid.
		go func(pid int) { died <- deaths.await(pid) }(proc.pid)

		if p.watch(ctx, proc, died, attached) == supplicantEnded {
			return
		}
		if time.Since(started) > time.Minute {
			backoff = p.backoff
		}

		next, ok := p.restart(ctx, config, &backoff)
		if !ok {
			return
		}
		proc, attached = next, false
	}
}

// watch supervises one running supplicant. It returns supplicantDied
// when the process exits on its own, and supplicantEnded after it
// stops the process because stop closed or ctx ended.
//
// While the control client is not attached, watch retries the attach
// with the same doubling delay as restart. Without the retry, one
// failed attach leaves a supplicant that sends its events to no one,
// and on the console and in the status that looks like a quiet radio.
func (p *supplicantProcess) watch(ctx context.Context, proc runningSupplicant,
	died <-chan unix.WaitStatus, attached bool) supplicantOutcome {
	backoff := p.backoff
	for {
		if attached {
			return p.await(ctx, proc, died, nil)
		}
		// attach waits for the supplicant's socket to appear. It runs
		// in a goroutine so that a stop does not have to wait for it.
		attempt := make(chan error, 1)
		go func() { attempt <- p.control.attach() }()
		select {
		case err := <-attempt:
			if err == nil {
				fmt.Printf("liken: wireless: the control client of the supplicant on %s attached again\n", p.ifname)
				attached = true
				continue
			}
			fmt.Fprintf(os.Stderr, "liken: wireless: %s: %v\n", p.ifname, err)
		case status := <-died:
			fmt.Printf("liken: wireless: the supplicant on %s exited (%s)\n", p.ifname, describeExit(status))
			return supplicantDied
		case <-p.stop:
			stopSupplicant(p.ifname, proc, died)
			return supplicantEnded
		case <-ctx.Done():
			stopSupplicant(p.ifname, proc, died)
			return supplicantEnded
		}
		backoff = p.next(backoff)
		if outcome := p.await(ctx, proc, died, time.After(withJitter(backoff))); outcome != supplicantWaited {
			return outcome
		}
	}
}

// restart starts the supplicant again, with a doubling delay before
// each attempt, until a start succeeds. It returns the new process, or
// false when stop closes or ctx ends. It never gives up, because
// without a supplicant the radio stays disconnected.
func (p *supplicantProcess) restart(ctx context.Context, config string, backoff *time.Duration) (runningSupplicant, bool) {
	for {
		*backoff = p.next(*backoff)
		delay := withJitter(*backoff)
		fmt.Printf("liken: wireless: restarting the supplicant on %s in %s\n", p.ifname, delay.Round(time.Millisecond))
		if p.await(ctx, runningSupplicant{}, nil, time.After(delay)) == supplicantEnded {
			return runningSupplicant{}, false
		}
		proc, err := startSupplicant(p.ifname, config)
		if err == nil {
			return proc, true
		}
		fmt.Fprintf(os.Stderr, "liken: wireless: %v\n", err)
	}
}

// runningSupplicant is one supplicant process. signal sends through
// the os.Process that started it. That Process holds a pidfd, and Go
// signals with pidfd_send_signal, so a signal to a process that the
// reaper already collected returns os.ErrProcessDone and cannot reach
// a later process that reuses the pid. Tests replace signal to record
// signals.
type runningSupplicant struct {
	pid    int
	signal func(os.Signal) error
}

func supplicantHandle(p *os.Process) runningSupplicant {
	return runningSupplicant{pid: p.Pid, signal: p.Signal}
}

// startSupplicant is a variable so that a test can script start
// results without a radio, the binary, or the reaper.
var startSupplicant = execSupplicant

// execSupplicant runs the supplicant in the foreground, without -B.
// With -B the supplicant forks and the parent exits at once, so the
// death registry would report only the exit of that parent.
func execSupplicant(ifname, config string) (runningSupplicant, error) {
	cmd := exec.Command(supplicantBinary, "-i", ifname, "-c", config, "-D", "nl80211")
	cmd.Stdout = &lineWriter{dest: console, prefix: "wpa | "}
	cmd.Stderr = &lineWriter{dest: console, prefix: "wpa | "}
	if err := deaths.start(cmd); err != nil {
		return runningSupplicant{}, fmt.Errorf("starting the supplicant on %s: %w", ifname, err)
	}
	fmt.Printf("liken: wireless: the supplicant on %s started (pid %d)\n", ifname, cmd.Process.Pid)
	return supplicantHandle(cmd.Process), nil
}

// stopSupplicant sends SIGTERM to a supplicant and waits for the
// reaper to report its exit, and sends SIGKILL after 5 seconds. On
// SIGTERM the supplicant deauthenticates from the access point and
// takes the interface down, which leaves the radio ready for the next
// boot.
func stopSupplicant(ifname string, proc runningSupplicant, died <-chan unix.WaitStatus) {
	fmt.Printf("liken: wireless: stopping the supplicant on %s (pid %d)\n", ifname, proc.pid)
	_ = proc.signal(unix.SIGTERM)
	select {
	case <-died:
	case <-time.After(5 * time.Second):
		fmt.Fprintf(os.Stderr, "liken: wireless: the supplicant on %s did not exit 5s after SIGTERM, so init sends SIGKILL\n", ifname)
		_ = proc.signal(unix.SIGKILL)
		<-died
	}
}

// stopSupplicants stops every supervised supplicant and waits up to 10
// seconds for each supervision loop to finish. The shutdown calls it
// before kill(-1). Otherwise a supervision loop restarts the
// supplicant that the SIGTERM of kill(-1) stopped, and the new process
// gets only the SIGKILL that follows, so it never deauthenticates.
func stopSupplicants() {
	supplicantsMu.Lock()
	supplicantsStopping = true
	running := supplicants
	supplicants = nil
	supplicantsMu.Unlock()

	for _, p := range running {
		close(p.stop)
	}
	for _, p := range running {
		select {
		case <-p.done:
		case <-time.After(10 * time.Second):
			fmt.Fprintf(os.Stderr, "liken: wireless: the supervision of the supplicant on %s did not finish within 10s, so the shutdown continues without it\n", p.ifname)
		}
	}
}

// routeLookup returns the name of the interface that a packet to the
// address would leave by. It is a function type so that tests can run
// radioToWaitFor without netlink.
type routeLookup func(net.IP) (string, error)

// routeVia is the routeLookup that reads the kernel's routing table.
func routeVia(dst net.IP) (string, error) {
	routes, err := netlink.RouteGet(dst)
	if err != nil {
		return "", err
	}
	if len(routes) == 0 {
		return "", fmt.Errorf("no route toward %s", dst)
	}
	link, err := netlink.LinkByIndex(routes[0].LinkIndex)
	if err != nil {
		return "", err
	}
	return link.Attrs().Name, nil
}

// endpointAddress returns the IP address in a cluster endpoint such as
// https://10.10.0.1:6443. It returns false when the endpoint is empty
// or has a host name instead of an address.
func endpointAddress(endpoint string) (net.IP, bool) {
	if endpoint == "" {
		return nil, false
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, false
	}
	ip := net.ParseIP(u.Hostname())
	return ip, ip != nil
}

// radioToWaitFor decides whether the boot waits for a radio or
// continues without it. It returns the radio to wait for and the
// message to print, or nil when the boot continues.
//
// The boot waits only when a radio failed deterministically and no
// interface that came up has a route toward the cluster's endpoint
// (see routeToward). A machine that cannot reach its cluster has
// nothing to do until the radio joins. Any other radio failure never
// makes the boot wait, so an access point that is off or out of range
// cannot stop a machine from booting.
func radioToWaitFor(conns []*connection, endpoint string, route routeLookup) (*radio, string) {
	var failed *radio
	for _, conn := range conns {
		if failed == nil && conn.radio != nil && conn.radio.deterministic() {
			failed = conn.radio
		}
	}
	if failed == nil {
		return nil, ""
	}
	routed, why := routeToward(conns, endpoint, route)
	if routed {
		return nil, ""
	}
	reason := fmt.Sprintf("liken: wireless: %s cannot join %s: %s", failed.ifname, failed.ssid, failed.message)
	return failed, fmt.Sprintf("%s, and %s, so the boot waits for the radio", reason, why)
}

// routeToward reports whether the machine can send a packet to its
// cluster's endpoint through an interface that has an address. When it
// cannot, the second result says why, for the console. With no
// addressed interface it always returns false.
//
// When some interface has an address, an empty endpoint or a host name
// counts as reachable. A machine with no endpoint is its own cluster,
// and a host name needs DNS, which can need the network that is
// failing, so its route cannot be checked. A route through loopback
// also counts, because a leader's endpoint is its own address.
func routeToward(conns []*connection, endpoint string, route routeLookup) (bool, string) {
	settled := map[string]bool{}
	for _, conn := range conns {
		if conn.addr != nil {
			settled[conn.ifname] = true
		}
	}
	if len(settled) == 0 {
		return false, "no other interface has an address"
	}
	dst, ok := endpointAddress(endpoint)
	if !ok {
		return true, ""
	}
	ifname, err := route(dst)
	if err != nil {
		return false, fmt.Sprintf("the machine has no route toward %s", dst)
	}
	if ifname == "lo" || settled[ifname] {
		return true, ""
	}
	return false, fmt.Sprintf("the route toward %s leaves by %s, which has no address", dst, ifname)
}

// radioWaitConsole is the device that waitForRadio writes its message
// to. It is a variable so that a test can use a file.
var radioWaitConsole = consoleDevice

// waitForRadio blocks the boot until the supplicant reports that the
// radio has joined, or until its control socket closes.
//
// It prints the reason to stderr, which init sends to the kernel log
// and so to every console on the kernel command line, and to
// radioWaitConsole, the device a person most likely watches. Unlike the
// installer's waits it reads no keys, so a repair to the network
// resumes the boot with nobody at the machine.
func waitForRadio(r *radio, reason string) {
	retrying := "liken: wireless: the supplicant keeps retrying, and the boot continues when the radio joins"
	fmt.Fprintln(os.Stderr, reason)
	fmt.Fprintln(os.Stderr, retrying)
	if device, err := os.OpenFile(radioWaitConsole, os.O_RDWR, 0); err == nil {
		fmt.Fprintln(device, reason)
		fmt.Fprintln(device, retrying)
		device.Close()
	}

	// A WrongKey radio with no supplicant is one whose configuration
	// init could not build, so no event can end the wait, and only a
	// new passphrase on the install media fixes it. PID 1 must not
	// exit, so waitForRadio sleeps forever.
	if r.control == nil {
		fmt.Fprintf(os.Stderr, "liken: wireless: no supplicant runs on %s, so the radio cannot join until the install media has a new passphrase\n", r.ifname)
		for {
			time.Sleep(time.Hour)
		}
	}

	for event := range r.control.events() {
		if line := describeWirelessEvent(r.ifname, event); line != "" {
			fmt.Println(line)
		}
		state, message, settled := classifyWirelessEvent(event)
		if !settled {
			continue
		}
		r.state, r.message = state, message
		if state == machine.WirelessConnected {
			return
		}
	}
	// When the event stream closes, nothing can end the wait, so the
	// boot continues without the radio rather than stop with no report.
	fmt.Fprintf(os.Stderr, "liken: wireless: %s: the supplicant's control socket closed, so the boot continues without the radio\n", r.ifname)
}
