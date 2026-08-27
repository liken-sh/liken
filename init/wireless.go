package main

// Everything the boot does for an interface that declares a radio.
// The split of the work is deliberate: the supplicant owns the 802.11
// session, the association, the keys, and the rekeys, and nothing
// else. Init keeps its own addressing, so once a radio associates the
// interface takes DHCP or a static address on the same code a wired
// port does. The order is the supplicant, then the join, then the
// address. The events on the supplicant's control socket
// matter because the kernel cannot tell a wrong passphrase apart from
// an access point that is switched off; only the supplicant can say
// which one it is (plans/62-wifi.md).

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

	"github.com/liken-sh/liken/machine"
)

// supplicantBinary is the program the image carries for the 802.11
// session. wpa-supplicant/fetch.sh builds it static, with the nl80211
// driver, the control interface, and SAE, and nothing else.
const supplicantBinary = "/sbin/wpa_supplicant"

// The whole wireless runtime lives under /run because /run is a
// tmpfs, the generated configuration holds the passphrase, and a
// passphrase must never reach a disk that outlives the boot. /run is
// mounted with the essential filesystems rather than with k3s's,
// because a radio comes up long before k3s does (worldreport.go).
//
// It is a variable rather than a constant so tests can write into a
// directory of their own. A real boot never points it anywhere but /run.
var wirelessRunDir = "/run/liken/wireless"

// How long a boot waits for a radio to associate. A scan of both
// bands plus a four-way handshake finishes in a few seconds on a
// healthy network, and the DHCP exchange beside this wait already
// bounds itself at 30 seconds. A boot still waiting at 45 seconds is
// waiting on something that waiting will not fix. The wait ends early
// on the first settling event either way, so the bound costs nothing
// when the join works.
const associationPatience = 45 * time.Second

// radio holds everything init keeps about one wireless interface: what
// the manifest asked for, what the join did, and the two things that
// stay live for the rest of the boot, the supervised process and the
// event stream it publishes.
type radio struct {
	ifname string
	ssid   string

	// state and message are the join's verdict, bound for the facts
	// tree and, through it, for the Machine's status.
	state   machine.WirelessState
	message string

	// control is the attached client on the supplicant's control
	// socket. The park below keeps reading it, which is what lets a fix
	// on the network side resume a parked boot with no power cycle.
	control *wpaControl
}

// deterministic says which failures no amount of waiting corrects. A
// wrong passphrase, a missing passphrase file, and a passphrase with
// no safe rendering are all decisions the machine can state now; all
// three carry the WrongKey state. Every other failure looks exactly
// like an access point that has not answered yet.
func (r *radio) deterministic() bool {
	return r.state == machine.WirelessWrongKey
}

// wirelessStatus renders the radio for the facts tree.
func (r *radio) wirelessStatus() *machine.WirelessStatus {
	return &machine.WirelessStatus{SSID: r.ssid, State: r.state, Message: r.message}
}

// joinWireless runs the wireless half of one interface's bring-up: it
// generates the supplicant's configuration, starts the supplicant
// under supervision, and waits for the association. It returns a
// radio in every case, because a radio that failed to join is exactly
// what the status must report.
//
// liken does not touch rfkill. The kernel starts radios unblocked, a
// soft block does not survive a reboot, and nothing in liken writes
// one, so there is no block to clear. A hardware kill switch shows up
// as a radio that never associates, and no software unblock can clear
// that either.
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

	awaitAssociation(r, associationPatience)
	return r
}

// awaitAssociation reads the supplicant's events until one of them
// settles the join, or until patience runs out. It writes the verdict
// onto the radio.
//
// A scan that finds nothing is not a verdict. An access point that is
// off, rebooting, or out of range produces exactly these events, and
// the plan's rule is that absence never parks a boot. The console
// line still goes out for every event, because a person watching the
// boot needs to know what the radio is doing.
func awaitAssociation(r *radio, patience time.Duration) {
	deadline := time.After(patience)
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
			if state, message, settled := judgeWirelessEvent(event); settled {
				r.state, r.message = state, message
				return
			}
		case <-deadline:
			r.state = machine.WirelessNoCarrier
			r.message = fmt.Sprintf("no access point answered for %s; the supplicant keeps trying", patience)
			fmt.Fprintf(os.Stderr, "liken: wireless: %s: %s\n", r.ifname, r.message)
			return
		}
	}
}

// controlSocketDir is where the supplicant puts one interface's control
// socket. Each interface gets its own directory, so the socket's name
// inside it is always the interface's name and nothing has to be
// cleaned up between interfaces.
func controlSocketDir(ifname string) string {
	return filepath.Join(wirelessRunDir, ifname, "ctrl")
}

// writeWirelessConfig puts one interface's generated configuration on
// the tmpfs and reports where. The mode is owner-only, because the file
// holds the network's passphrase.
func writeWirelessConfig(ifname, config string) (string, error) {
	dir := filepath.Join(wirelessRunDir, ifname)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("preparing %s: %w", dir, err)
	}
	path := filepath.Join(dir, "wpa_supplicant.conf")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return path, nil
}

// supplicants holds every supplicant this boot started, so the shutdown
// can stop them before it signals the rest of the machine. It is a
// package variable for the same reason the death registry is: init is
// one program, and these are its children.
//
// The lock exists because background radios register their
// supplicants from the radio component's goroutine while the boot
// and the shutdown run on others. The stopping flag latches when the
// shutdown runs, and no supplicant starts after it: a radio that
// settles late during a reboot must not leave a process the shutdown
// already finished stopping.
var (
	supplicantsMu       sync.Mutex
	supplicants         []*supplicantProcess
	supplicantsStopping bool
)

// registerSupplicant adds one supervised supplicant to the list the
// shutdown stops, and reports false once the shutdown has run. The
// check and the append happen under one hold of the lock, so a
// supplicant either makes the list the shutdown will stop, or is
// refused; there is no window between the two.
func registerSupplicant(p *supplicantProcess) bool {
	supplicantsMu.Lock()
	defer supplicantsMu.Unlock()
	if supplicantsStopping {
		return false
	}
	supplicants = append(supplicants, p)
	return true
}

// supplicantProcess is one supervised supplicant: the loop that keeps
// it running, and the channels that stop it.
type supplicantProcess struct {
	ifname string
	stop   chan struct{}
	done   chan struct{}

	// The restart loop holds the control client because a supplicant
	// keeps its list of attached clients in memory. The process that
	// dies takes liken's attachment with it, and the new process
	// reports to nobody until it is asked again.
	control *wpaControl

	// Restart pacing, in the machine plane's own form: a start that
	// keeps failing waits twice as long each time, up to a cap, and a
	// process that ran for a while before dying starts the pacing over.
	// They are fields rather than constants so a test can run the loop
	// without waiting out real seconds.
	backoff    time.Duration
	maxBackoff time.Duration
}

// superviseSupplicant starts the supplicant for one interface, keeps it
// running for the life of the boot, and returns a client attached to
// its control socket.
//
// This is the k3s supervisor's second resident, on the supervisor's
// own terms: the reaper stays the only caller of wait, and this loop
// subscribes to the death registry exactly as superviseK3s does. The
// association dies with the process, so a restart is the only repair.
// The address stays on the interface across the restart, which is
// what makes the outage seconds rather than a reboot.
func superviseSupplicant(ifname, config string) (*wpaControl, error) {
	dir := controlSocketDir(ifname)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("preparing %s: %w", dir, err)
	}

	pid, err := startSupplicant(ifname, config)
	if err != nil {
		return nil, err
	}
	control, err := attachWPAControl(filepath.Join(dir, ifname))
	if err != nil {
		return nil, fmt.Errorf("attaching to the supplicant on %s: %w", ifname, err)
	}

	p := &supplicantProcess{
		ifname: ifname, control: control,
		stop: make(chan struct{}), done: make(chan struct{}),
		backoff: time.Second, maxBackoff: 30 * time.Second,
	}
	// A radio that settles after the shutdown began gets its
	// supplicant stopped, not supervised. The process just started,
	// so it is stopped by the same deliberate path the shutdown
	// uses, and the caller reports the refusal.
	if !registerSupplicant(p) {
		control.close()
		died := make(chan unix.WaitStatus, 1)
		go func() { died <- deaths.await(pid) }()
		stopSupplicant(ifname, pid, died)
		return nil, fmt.Errorf("the machine is stopping its supplicants; the one on %s was stopped again", ifname)
	}
	plane.start("the supplicant on "+ifname, func(ctx context.Context) error {
		p.run(ctx, pid, config)
		return nil
	})
	return control, nil
}

// supplicantOutcome is why one wait inside the supervision loop ended.
type supplicantOutcome int

const (
	supplicantDied   supplicantOutcome = iota // the process exited on its own
	supplicantEnded                           // the loop was asked to finish
	supplicantWaited                          // the delay elapsed and nothing else happened
)

// await waits for whichever comes first: the process exiting, the loop
// being asked to finish, or a delay elapsing. A nil died channel says
// there is no process to wait on, and a nil delay says wait as long as
// it takes.
//
// The stop step is conditional because between a death and the next
// successful start there is no process, so a stop arriving in that
// window has nothing to signal. Signalling the pid of a process whose
// exit the reaper already collected would wait on an exit that can
// never be reported a second time.
func (p *supplicantProcess) await(ctx context.Context, pid int,
	died <-chan unix.WaitStatus, delay <-chan time.Time) supplicantOutcome {
	select {
	case status := <-died:
		fmt.Printf("liken: wireless: the supplicant on %s exited (%s)\n", p.ifname, describeExit(status))
		return supplicantDied
	case <-p.stop:
		if died != nil {
			stopSupplicant(p.ifname, pid, died)
		}
		return supplicantEnded
	case <-ctx.Done():
		if died != nil {
			stopSupplicant(p.ifname, pid, died)
		}
		return supplicantEnded
	case <-delay:
		return supplicantWaited
	}
}

// next doubles the restart delay, up to the cap. A start that keeps
// failing then retries at a steady pace rather than faster and faster.
func (p *supplicantProcess) next(backoff time.Duration) time.Duration {
	if backoff >= p.maxBackoff {
		return p.maxBackoff
	}
	return backoff * 2
}

// run keeps one supplicant running for the life of the boot, modelled on
// superviseK3s: the reaper reports each death, the loop reports it on the
// console, and the next start waits out a backoff that doubles while the
// failures stay fast.
//
// The loop has exactly two states, and they are separate functions
// because they wait on different things. While a process runs, watch
// holds it. Between processes, restart holds it, and there is no pid to
// signal or to wait on until restart hands back a live one.
func (p *supplicantProcess) run(ctx context.Context, pid int, config string) {
	defer close(p.done)
	backoff := p.backoff
	attached := true
	for {
		started := time.Now()
		died := make(chan unix.WaitStatus, 1)
		// The pid is passed in rather than captured, because the loop
		// gives the variable the next process's pid before this
		// goroutine is guaranteed to have read it.
		go func(pid int) { died <- deaths.await(pid) }(pid)

		if p.watch(ctx, pid, died, attached) == supplicantEnded {
			return
		}
		// A supplicant that ran for a while and then died is a fresh
		// failure, not a continuing one, so its restart starts the
		// pacing over.
		if time.Since(started) > time.Minute {
			backoff = p.backoff
		}

		next, ok := p.restart(ctx, config, &backoff)
		if !ok {
			return
		}
		// A new process arrives unattached. The list of attached
		// clients lives in the process that died, so the new one
		// reports to nobody until it is asked, and a parked boot is
		// waiting on exactly those reports.
		pid, attached = next, false
	}
}

// watch holds one running supplicant. It returns supplicantDied when the
// process exited on its own, and supplicantEnded when the loop was asked
// to finish, in which case it has already stopped the process.
//
// While the event stream is detached, watch keeps asking for it, on the
// same backoff the restarts use. A single failed attach would otherwise
// leave the supplicant running and reporting to nobody, which reads on
// the console and in the status exactly like a radio that went quiet.
func (p *supplicantProcess) watch(ctx context.Context, pid int,
	died <-chan unix.WaitStatus, attached bool) supplicantOutcome {
	backoff := p.backoff
	for {
		if attached {
			return p.await(ctx, pid, died, nil)
		}
		// The attach runs in a goroutine so that waiting for the
		// supplicant's socket to appear cannot delay a stop.
		attempt := make(chan error, 1)
		go func() { attempt <- p.control.attach() }()
		select {
		case err := <-attempt:
			if err == nil {
				fmt.Printf("liken: wireless: the event stream on %s is attached again\n", p.ifname)
				attached = true
				continue
			}
			fmt.Fprintf(os.Stderr, "liken: wireless: %s: %v\n", p.ifname, err)
		case status := <-died:
			fmt.Printf("liken: wireless: the supplicant on %s exited (%s)\n", p.ifname, describeExit(status))
			return supplicantDied
		case <-p.stop:
			stopSupplicant(p.ifname, pid, died)
			return supplicantEnded
		case <-ctx.Done():
			stopSupplicant(p.ifname, pid, died)
			return supplicantEnded
		}
		backoff = p.next(backoff)
		if outcome := p.await(ctx, pid, died, time.After(withJitter(backoff))); outcome != supplicantWaited {
			return outcome
		}
	}
}

// restart gets a supplicant running again, however many attempts that
// takes. It returns the pid of a live process, or false when the loop
// was asked to finish. It never returns after a failed start, because a
// start that failed leaves the machine with no supplicant at all, and
// the radio then has nothing keeping its session alive.
func (p *supplicantProcess) restart(ctx context.Context, config string, backoff *time.Duration) (int, bool) {
	for {
		*backoff = p.next(*backoff)
		delay := withJitter(*backoff)
		fmt.Printf("liken: wireless: restarting the supplicant on %s in %s\n", p.ifname, delay.Round(time.Millisecond))
		if p.await(ctx, 0, nil, time.After(delay)) == supplicantEnded {
			return 0, false
		}
		pid, err := startSupplicant(p.ifname, config)
		if err == nil {
			return pid, true
		}
		fmt.Fprintf(os.Stderr, "liken: wireless: %v\n", err)
	}
}

// startSupplicant launches the supplicant for one interface and reports
// its process id. It is a variable holding the real launcher below, so a
// test can script the outcomes of the restart loop without a radio, a
// binary, or a reaper, the same way parkConsole and wirelessRunDir let a
// test stand in for a device and a tmpfs.
var startSupplicant = execSupplicant

// execSupplicant runs the vendored program.
//
// -i names the interface and -c names the generated file. -D names
// the driver outright rather than letting the program try each one it
// was built with; nl80211 is the only driver in liken's build anyway.
// The program stays in the foreground because the supervisor above is
// what keeps it running, and a daemonized process would exit
// immediately and put its own child out of the death registry's
// reach.
func execSupplicant(ifname, config string) (int, error) {
	cmd := exec.Command(supplicantBinary, "-i", ifname, "-c", config, "-D", "nl80211")
	cmd.Stdout = &lineWriter{dest: console, prefix: "wpa | "}
	cmd.Stderr = &lineWriter{dest: console, prefix: "wpa | "}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("starting the supplicant on %s: %w", ifname, err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	fmt.Printf("liken: wireless: the supplicant on %s started (pid %d)\n", ifname, pid)
	return pid, nil
}

// stopSupplicant asks one supplicant to exit and waits for the reaper
// to confirm it, escalating when it does not.
//
// The supplicant is stopped deliberately rather than left to the
// general kill. On SIGTERM it deauthenticates from the access point
// and takes the interface down, which leaves the radio in a state the
// next boot can start from. And the shutdown's kill(-1) would race
// the restart loop above into starting a supplicant it is about to
// kill.
func stopSupplicant(ifname string, pid int, died <-chan unix.WaitStatus) {
	fmt.Printf("liken: wireless: stopping the supplicant on %s (pid %d)\n", ifname, pid)
	_ = killProcess(pid, unix.SIGTERM)
	select {
	case <-died:
	case <-time.After(5 * time.Second):
		fmt.Fprintf(os.Stderr, "liken: wireless: the supplicant on %s ignored SIGTERM for 5s; killing it\n", ifname)
		_ = killProcess(pid, unix.SIGKILL)
		<-died
	}
}

// killProcess sends a signal to one process. It is a variable holding
// the syscall, so a test can watch which process the supervision loop
// signals without a test run being able to signal anything real.
var killProcess = unix.Kill

// stopSupplicants ends every supervised supplicant. The shutdown calls
// it before it signals the rest of the machine, so the restart loops are
// finished before kill(-1) reaches anything.
func stopSupplicants() {
	// The latch and the list move under one hold of the lock: after
	// this block, every registered supplicant is in running, and
	// every later register is refused.
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
			fmt.Fprintf(os.Stderr, "liken: wireless: the supplicant on %s did not stop; going on without it\n", p.ifname)
		}
	}
}

// routeLookup answers which interface a packet toward one address would
// leave by. It is a function value so that the park decision below is a
// pure function that a test can drive with no netlink and no kernel.
type routeLookup func(net.IP) (string, error)

// routeVia asks the kernel's routing table the same question the
// kernel would ask when a packet is sent.
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

// endpointAddress reads the literal address out of a cluster endpoint,
// for example https://10.10.0.1:6443. It reports false for an endpoint
// that names no address, which covers both an empty endpoint and a
// name that only DNS could resolve.
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

// parkDecision answers the plan's question: does this boot stop and
// wait, or does it go on degraded? It returns the radio to wait on and
// the reason to print, or nil when the boot goes on.
//
// The rule is the plan's: only a deterministic failure qualifies, and
// the boot goes on whenever any settled interface gives a route
// toward the cluster's endpoint. A machine whose only path was the
// failed radio waits, because a machine that cannot reach its cluster
// has nothing to do anyway.
//
// An endpoint that names no address never parks. A DNS name needs
// DNS, and DNS needs the network that is in question, so nothing
// reliable can be decided from it. A machine alone is its own cluster
// and declares no endpoint at all. The plan's bias in both cases is
// to boot rather than to wait.
//
// A route that leaves by loopback counts as reaching the endpoint. A
// leader's endpoint is its own address, so the kernel answers
// loopback, and a leader must never park on a radio it does not need.
func parkDecision(conns []*connection, endpoint string, route routeLookup) (*radio, string) {
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
	return failed, fmt.Sprintf("%s; %s, so this boot waits here", reason, why)
}

// routeToward answers the one question both the park and the pass
// split ask: can this machine, on the interfaces that settled, send
// a packet toward its cluster's endpoint? The false answers carry
// the reason, in the words the park has always printed.
//
// An endpoint that names no address counts as routed. A DNS name
// needs DNS, and DNS needs the network in question, so nothing
// reliable can be decided from it; a machine alone declares no
// endpoint at all. The bias in both cases is the plan's: boot rather
// than wait. A route that leaves by loopback also counts, because a
// leader's endpoint is its own address.
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
		return false, fmt.Sprintf("nothing on this machine has a route toward %s", dst)
	}
	if ifname == "lo" || settled[ifname] {
		return true, ""
	}
	return false, fmt.Sprintf("the route toward %s leaves by %s, which never came up", dst, ifname)
}

// parkConsole is the device a park writes its reason to. It is a
// variable so a test can point the hold at a file of its own.
var parkConsole = consoleDevice

// park holds the boot on a radio that cannot join, and releases it the
// moment the supplicant reports that the radio did.
//
// The message goes out twice: the log copy travels the kmsg pipeline
// to every console the command line named, and the direct copy
// reaches the one device a person is looking at. Unlike the
// installer's holds, this hold reads no key. The thing that ends it
// is an event on the socket rather than a keypress, so a fix on the
// network side resumes the boot with nobody at the machine.
func park(r *radio, reason string) {
	fmt.Fprintln(os.Stderr, reason)
	fmt.Fprintln(os.Stderr, "liken: wireless: the supplicant keeps trying; this boot goes on the moment the radio joins")
	if device, err := os.OpenFile(parkConsole, os.O_RDWR, 0); err == nil {
		fmt.Fprintln(device, reason)
		fmt.Fprintln(device, "liken: wireless: the supplicant keeps trying; this boot goes on the moment the radio joins")
		device.Close()
	}

	// A park with no supplicant waits anyway. The configuration
	// itself was refused, so no process is running to report a
	// repair, and nothing this machine can do would produce one; the
	// fix is the install media. PID 1 must not exit, which is why
	// this waits rather than returning.
	if r.control == nil {
		fmt.Fprintln(os.Stderr, "liken: wireless: no supplicant is running for this interface; the passphrase on the install media is the fix")
		for {
			time.Sleep(time.Hour)
		}
	}

	for event := range r.control.events() {
		if line := describeWirelessEvent(r.ifname, event); line != "" {
			fmt.Println(line)
		}
		state, message, settled := judgeWirelessEvent(event)
		if !settled {
			continue
		}
		r.state, r.message = state, message
		if state == machine.WirelessConnected {
			return
		}
	}
	// A closed stream ends the hold. The events are the only thing
	// that can release this wait, and a boot stopped forever with no
	// way to report why is worse than a boot that goes on degraded.
	fmt.Fprintf(os.Stderr, "liken: wireless: %s: the supplicant's control socket closed; going on without the radio\n", r.ifname)
}
