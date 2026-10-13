package main

// Fake INDI servers for the operator's tests, built from the bytes that
// the real simulators sent, which indi/inditest/testdata/ records. Each
// server runs a driver for each device pod that its server pod's
// annotation names and that the fake API server holds Ready, as
// indiserver does through the shims: a device pod that comes or goes
// defines or deletes its device on the server, and a new pod is a new
// driver, disconnected, with its settings lost. A driver defines what
// its baseline transcript defines, adds what its connect transcript
// defines when it connects, and answers every other change with the
// values sent and the state Ok. A test can hold a property, so that
// its driver never answers.
//
// indi/inditest holds the properties, the drivers, and the connections
// that these servers share with that package's own Server, which serves
// one fixed set of simulators to another component's tests. These
// servers follow the fake API server's pods, and add the locks, the
// presets, and the configuration files that the operator's tests need.
//
// The servers answer over in-memory pipes, so the tests run in a
// synctest bubble.

import (
	"bufio"
	"context"
	"encoding/xml"
	"fmt"
	"maps"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/liken-sh/liken/observatory-operator/indi/inditest"
)

// definitions reads what one phase of a simulator's transcripts
// defines, such as definitions(t, "ccd", "connect").
func definitions(t *testing.T, simulator, phase string) []*inditest.Property {
	t.Helper()
	data, err := inditest.Transcript(simulator, phase)
	if err != nil {
		t.Fatal(err)
	}
	props, err := inditest.Definitions(data)
	if err != nil {
		t.Fatalf("%s/%s: %v", simulator, phase, err)
	}
	return props
}

// fakeDriver is one simulator behind a fake server.
type fakeDriver struct {
	device    string
	onConnect []*inditest.Property
	props     []*inditest.Property
	connected bool
	// snooped holds the reports that clients relayed to the driver
	// (fakelocks_test.go), by "<device>.<property>", each member's
	// value by its name.
	snooped map[string]map[string]string
	// saved holds the driver's configuration file
	// (fakeconfig_test.go), each property's members by its name, or
	// nil while the driver has saved none.
	saved map[string][]inditest.Member
}

var transcriptCache sync.Map

// transcriptPair is what a driver defines before it connects and what
// it defines when it connects.
type transcriptPair struct{ baseline, connect []*inditest.Property }

func newFakeDriver(t *testing.T, exec string) *fakeDriver {
	cached, ok := transcriptCache.Load(exec)
	if !ok {
		if v, isVariant := fakeVariants[exec]; isVariant {
			cached = v.transcripts(t, exec)
		} else {
			simulator := strings.TrimPrefix(exec, "indi_simulator_")
			cached = transcriptPair{definitions(t, simulator, "baseline"), definitions(t, simulator, "connect")}
		}
		transcriptCache.Store(exec, cached)
	}
	p := cached.(transcriptPair)
	d := inditest.DriverOf(p.baseline, p.connect)
	return &fakeDriver{device: d.Device, props: d.Props, onConnect: d.OnConnect}
}

func (d *fakeDriver) find(name string) *inditest.Property {
	for _, p := range d.props {
		if p.Name == name {
			return p
		}
	}
	return nil
}

// fakeServer is one INDI server and the drivers it runs, by the name of
// each device pod and its UID.
type fakeServer struct {
	uid     string
	drivers map[string]*fakeDriver
	uids    map[string]string
	conns   []*inditest.Conn
	// applied is the list of device pods that the shim applied last,
	// and target is the list that the server pod's annotation names
	// now (fakeshim_test.go).
	applied, target []string
}

type indiWorld struct {
	t   *testing.T
	api *fakeAPI

	mu      sync.Mutex
	servers map[string]*fakeServer
	// sent records each change that a client sent, as
	// "<server> <device>.<property> <member>=<value> ...".
	sent []string
	// relayed records each report that a client relayed, in the same
	// form (fakelocks_test.go).
	relayed []string
	// held holds the properties, as "<device>.<property>", whose
	// driver never answers a change.
	held map[string]bool
	// refused holds the properties whose driver answers each change
	// with Alert and keeps its values, as a driver whose hardware
	// fails does.
	refused map[string]bool
	// moving holds the properties whose driver answers a change with
	// Busy, as a mount that slews does, until a test sets the state.
	moving map[string]bool
	// lateDials holds how many dials each server refuses after its pod
	// is Ready, as an indiserver that has not opened its port yet does.
	lateDials map[string]int
	// presets holds the switch that a driver turns On when it defines a
	// property, by "<device>.<property>" (fakepresets_test.go).
	presets map[string]string
	// texts holds the text that a driver gives a member when it
	// defines its property, by "<device>.<property>.<member>"
	// (fakepresets_test.go).
	texts map[string]string
	// withheld holds the properties, as "<device>.<property>", that a
	// driver leaves out when it connects, until reveal defines them
	// (fakepresets_test.go).
	withheld map[string]bool
	// silent holds the devices whose driver does not answer a
	// getProperties that names the device, and asked counts each such
	// getProperties by device (fakesync_test.go).
	silent map[string]bool
	asked  map[string]int
	// late holds the servers that leave a client's first getProperties
	// unanswered until answerLate, and waiting holds those clients
	// (fakesync_test.go).
	late    map[string]bool
	waiting map[string][]*inditest.Conn
	// shimDelay is the time from a change of a server pod's annotation
	// to the shim's start or stop of a driver (fakeshim_test.go).
	shimDelay time.Duration
	// restarts records each driver whose device pod was gone while the
	// shim still listed it, as "<server> <pod>".
	restarts []string

	// followMu serializes catchUp, and guards last, the pods that the
	// servers follow now.
	followMu sync.Mutex
	last     map[string]map[string]any
}

func startIndiWorld(t *testing.T, api *fakeAPI) *indiWorld {
	w := &indiWorld{t: t, api: api, servers: map[string]*fakeServer{}, held: map[string]bool{}, refused: map[string]bool{}, moving: map[string]bool{}, lateDials: map[string]int{}, presets: map[string]string{}, texts: map[string]string{}, withheld: map[string]bool{}, silent: map[string]bool{}, asked: map[string]int{}, late: map[string]bool{}, waiting: map[string][]*inditest.Conn{}}
	go w.follow(t.Context())
	return w
}

// follow brings each server's drivers in line with the pods after each
// change of the fake API server's pods.
func (w *indiWorld) follow(ctx context.Context) {
	for {
		w.api.mu.Lock()
		changed := w.api.changed
		w.api.mu.Unlock()
		w.catchUp()
		select {
		case <-ctx.Done():
			w.mu.Lock()
			for _, s := range w.servers {
				for _, c := range s.conns {
					c.Close()
				}
			}
			w.mu.Unlock()
			return
		case <-changed:
		}
	}
}

// catchUp brings each server's drivers in line with the pods that the
// fake API server holds now. A stored object never changes
// (fakeAPI.store), so it reads the pods without a copy. A dial calls it
// too: the operator's watch can deliver a Ready server pod before
// follow wakes, and the real server listens once its pod is Ready.
func (w *indiWorld) catchUp() {
	w.followMu.Lock()
	defer w.followMu.Unlock()
	w.api.mu.Lock()
	pods := maps.Clone(w.api.objects[podsCollection])
	w.api.mu.Unlock()
	if !samePods(pods, w.last) {
		w.sync(pods)
		w.last = pods
	}
}

// samePods answers whether two reads of the pods hold the same stored
// objects.
func samePods(a, b map[string]map[string]any) bool {
	return maps.EqualFunc(a, b, func(x, y map[string]any) bool {
		return x["metadata"].(map[string]any)["resourceVersion"] == y["metadata"].(map[string]any)["resourceVersion"]
	})
}

func podReady(p map[string]any) bool {
	status, _ := p["status"].(map[string]any)
	conditions, _ := status["conditions"].([]any)
	for _, c := range conditions {
		if c.(map[string]any)["type"] == "Ready" && c.(map[string]any)["status"] == "True" {
			return true
		}
	}
	return false
}

func podUID(p map[string]any) string { return p["metadata"].(map[string]any)["uid"].(string) }

// links answers the device pods that a server pod's annotation names.
// The real server's shim reads the annotation within about a second;
// the fake reads it at once.
func links(p map[string]any) []string {
	var out []string
	annotations, _ := p["metadata"].(map[string]any)["annotations"].(map[string]any)
	list, _ := annotations[annotationDrivers].(string)
	for _, address := range strings.Fields(list) {
		out = append(out, strings.TrimSuffix(address, ":7625"))
	}
	return out
}

// execOf answers the driver that a device pod's socat starts.
func execOf(p map[string]any) string {
	container := p["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)
	args := container["args"].([]any)
	return strings.TrimSuffix(strings.TrimPrefix(args[1].(string), "EXEC:"), ",pipes")
}

func (w *indiWorld) sync(pods map[string]map[string]any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for name, s := range w.servers {
		if p, ok := pods[name]; !ok || podUID(p) != s.uid || !podReady(p) {
			// The server stopped: every connection ends, and every
			// driver behind it restarts with the next server.
			for _, c := range s.conns {
				c.Close()
			}
			delete(w.servers, name)
		}
	}
	for name, p := range pods {
		labels := p["metadata"].(map[string]any)["labels"].(map[string]any)
		if labels[labelRole] != roleServer || !podReady(p) {
			continue
		}
		s, ok := w.servers[name]
		if !ok {
			s = &fakeServer{uid: podUID(p), drivers: map[string]*fakeDriver{}, uids: map[string]string{}, applied: links(p), target: links(p)}
			w.servers[name] = s
		}
		w.shim(s, links(p))
		running := map[string]bool{}
		for _, link := range s.applied {
			device, ok := pods[link]
			if _, runs := s.drivers[link]; runs && !ok {
				w.restarts = append(w.restarts, name+" "+link)
			}
			if !ok || !podReady(device) {
				continue
			}
			running[link] = true
			if s.uids[link] == podUID(device) {
				continue
			}
			if old, ok := s.drivers[link]; ok {
				s.broadcast(fmt.Sprintf("<delProperty device=\"%s\"/>\n", inditest.Escape(old.device)))
			}
			d := newFakeDriver(w.t, execOf(device))
			s.drivers[link], s.uids[link] = d, podUID(device)
			for _, prop := range d.props {
				w.applyPreset(prop)
				s.broadcast(prop.Def())
			}
		}
		for link, d := range s.drivers {
			if !running[link] {
				s.broadcast(fmt.Sprintf("<delProperty device=\"%s\"/>\n", inditest.Escape(d.device)))
				delete(s.drivers, link)
				delete(s.uids, link)
			}
		}
	}
}

func (s *fakeServer) broadcast(text string) {
	for _, c := range s.conns {
		c.Send(text)
	}
}

// DialContext answers a pipe to the server that an address names, such
// as east-telescope.observatory.svc:7624, while its pod is Ready.
func (w *indiWorld) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	name, _, _ := strings.Cut(address, ".")
	w.catchUp()
	w.mu.Lock()
	defer w.mu.Unlock()
	s, ok := w.servers[name]
	if !ok || w.lateDials[name] > 0 {
		w.lateDials[name]--
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	}
	client, server := net.Pipe()
	c := inditest.NewConn(server)
	s.conns = append(s.conns, c)
	go w.serve(name, s, c)
	return client, nil
}

// serve reads one client's messages until the connection closes.
func (w *indiWorld) serve(name string, s *fakeServer, c *inditest.Conn) {
	decoder := xml.NewDecoder(bufio.NewReader(c))
	for {
		token, err := decoder.Token()
		if err != nil {
			c.Close()
			return
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		var v inditest.Vector
		if err := decoder.DecodeElement(&v, &start); err != nil {
			c.Close()
			return
		}
		w.answer(name, s, c, v)
	}
}

func (w *indiWorld) answer(name string, s *fakeServer, c *inditest.Conn, v inditest.Vector) {
	w.mu.Lock()
	defer w.mu.Unlock()
	tag := v.XMLName.Local
	if tag == "getProperties" {
		if v.Device != "" {
			w.asked[v.Device]++
		}
		if v.Device == "" && w.late[name] {
			w.waiting[name] = append(w.waiting[name], c)
			return
		}
		w.define(s, c, v.Device)
		return
	}
	if strings.HasPrefix(tag, "set") {
		w.relay(name, s, v)
		return
	}
	if !strings.HasPrefix(tag, "new") {
		return
	}
	var d *fakeDriver
	for _, driver := range s.drivers {
		if driver.device == v.Device {
			d = driver
		}
	}
	if d == nil {
		return
	}
	p := d.find(v.Name)
	if p == nil {
		return
	}
	var change []string
	for _, m := range v.Members {
		change = append(change, m.Name+"="+strings.TrimSpace(m.Value))
	}
	w.sent = append(w.sent, fmt.Sprintf("%s %s.%s %s", name, v.Device, v.Name, strings.Join(change, " ")))
	if w.held[v.Device+"."+v.Name] {
		return
	}
	if d.lockRefuses(p, v) {
		s.broadcast(p.Set())
		return
	}
	if w.refused[v.Device+"."+v.Name] {
		p.State = "Alert"
		s.broadcast(p.Set())
		return
	}
	if w.moving[v.Device+"."+v.Name] && p.State == "Busy" {
		inditest.Interrupt(p)
		s.broadcast(p.Set())
		return
	}
	parked := d.value("DOME_PARK", "PARK")
	for _, m := range v.Members {
		for i := range p.Members {
			if p.Members[i].Name == m.Name {
				p.Members[i].Value = strings.TrimSpace(m.Value)
			}
		}
	}
	p.State = "Ok"
	if w.moving[v.Device+"."+v.Name] {
		p.State = "Busy"
	}
	d.saveConfig(p)
	if parked != d.value("DOME_PARK", "PARK") {
		d.shutterFollowsPark(s)
	}
	s.broadcast(p.Set())
	// An abort ends what moves, as the simulators' abort does.
	if moving, ok := aborts[p.Name]; ok {
		if target := d.find(moving); target != nil && target.State == "Busy" {
			target.State = "Ok"
			s.broadcast(target.Set())
		}
	}
	// An abort during a park ends the park, as libindi's telescope does
	// (Telescope::ISNewSwitch in libs/indibase/inditelescope.cpp).
	if park := d.find("TELESCOPE_PARK"); p.Name == "TELESCOPE_ABORT_MOTION" && park != nil && park.State == "Busy" {
		inditest.Interrupt(park)
		s.broadcast(park.Set())
	}
	if p.Name != "CONNECTION" {
		return
	}
	connect := slices.ContainsFunc(p.Members, func(m inditest.Member) bool { return m.Name == "CONNECT" && m.Value == "On" })
	switch {
	case connect && !d.connected:
		d.connected = true
		for _, prop := range d.onConnect {
			if w.withheld[d.device+"."+prop.Name] {
				continue
			}
			added := prop.Clone()
			w.applyPreset(added)
			d.props = append(d.props, added)
			s.broadcast(added.Def())
		}
	case !connect && d.connected:
		d.connected = false
		for _, prop := range d.onConnect {
			d.props = slices.DeleteFunc(d.props, func(o *inditest.Property) bool { return o.Name == prop.Name })
			s.broadcast(fmt.Sprintf("<delProperty device=\"%s\" name=\"%s\"/>\n", inditest.Escape(d.device), inditest.Escape(prop.Name)))
		}
	}
}

// hold makes a driver never answer a change to one property.
func (w *indiWorld) hold(device, property string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.held[device+"."+property] = true
}

// changes answers the changes that clients sent, in order.
func (w *indiWorld) changes() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.sent)
}

// connected answers the devices of one server that are connected now.
func (w *indiWorld) connected(server string) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	if s, ok := w.servers[server]; ok {
		for _, d := range s.drivers {
			if d.connected {
				out = append(out, d.device)
			}
		}
	}
	slices.Sort(out)
	return out
}

// release makes a held property's driver answer again. A change it did
// not answer stays unanswered, as a real driver's would.
func (w *indiWorld) release(device, property string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.held, device+"."+property)
}

// count answers how many changes the clients sent to one property.
func (w *indiWorld) count(server, property string) int {
	n := 0
	for _, change := range w.changes() {
		if strings.HasPrefix(change, server+" "+property+" ") {
			n++
		}
	}
	return n
}

// aborts maps each abort property to the property whose motion it
// ends.
var aborts = map[string]string{
	"CCD_ABORT_EXPOSURE":     "CCD_EXPOSURE",
	"TELESCOPE_ABORT_MOTION": "EQUATORIAL_EOD_COORD",
}

// setState sets a property's state, as a driver does when an exposure
// or a slew begins.
func (w *indiWorld) setState(server, device, property, state string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, d := range w.servers[server].drivers {
		if p := d.find(property); d.device == device && p != nil {
			p.State = state
			w.servers[server].broadcast(p.Set())
		}
	}
}

// state answers a property's state on one server, or "" when no
// driver of the server defines it. A driver that restarts defines its
// properties again as its transcript does.
func (w *indiWorld) state(server, device, property string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if s, ok := w.servers[server]; ok {
		for _, d := range s.drivers {
			if p := d.find(property); d.device == device && p != nil {
				return p.State
			}
		}
	}
	return ""
}

// report sets one member of a property and sends the update to every
// client, as a driver reports a reading.
func (w *indiWorld) report(device, property, member, value string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, s := range w.servers {
		for _, d := range s.drivers {
			p := d.find(property)
			if d.device != device || p == nil {
				continue
			}
			for i := range p.Members {
				if p.Members[i].Name == member {
					p.Members[i].Value = value
				}
			}
			s.broadcast(p.Set())
		}
	}
}

// refuse makes a driver answer every change to one property with
// Alert.
func (w *indiWorld) refuse(device, property string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.refused[device+"."+property] = true
}

// accept makes a driver that refused a property answer it again.
func (w *indiWorld) accept(device, property string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.refused, device+"."+property)
}

// listenLate makes a server refuse its next dials after its pod is
// Ready.
func (w *indiWorld) listenLate(server string, dials int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lateDials[server] = dials
}

// cut closes every client connection to a server while the server and
// its drivers run, as a network that drops a connection does.
func (w *indiWorld) cut(server string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := w.servers[server]
	for _, c := range s.conns {
		c.Close()
	}
	s.conns = nil
}

// clients answers how many client connections a server holds.
func (w *indiWorld) clients(server string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.servers[server].conns)
}
