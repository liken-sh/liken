package main

// Fake INDI servers for the operator's tests. Their drivers replay the
// transcripts in indi/inditest/testdata/, which real simulators sent.
//
// A fake server runs while its server pod is Ready in the fake API
// server. Like indiserver behind the shims, it runs one driver for each
// device pod that is Ready and listed in the server pod's annotation.
// When a device pod is deleted or replaced, the server deletes its
// device on every client. A replaced pod gets a new driver, which is
// disconnected and has none of the old driver's settings.
//
// A driver defines the properties of its baseline transcript, and adds
// those of its connect transcript when a client connects it. It
// responds to any other change by storing the values sent and setting
// the state to Ok, unless a test holds, refuses, or moves the property.
//
// The properties, drivers, and connections come from indi/inditest. That
// package's own Server serves a fixed set of simulators to the tests of
// other components. These servers follow the pods of the fake API server
// instead, and add the locks, presets, and configuration files that the
// operator's tests need.
//
// Clients connect over net.Pipe, because a real socket would stop the
// fake clock of the synctest bubble.

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

// definitions returns the properties that one phase of a simulator's
// transcripts defines, such as definitions(t, "ccd", "connect").
func definitions(t *testing.T, simulator, phase string) []*inditest.Property {
	t.Helper()
	data, err := inditest.Transcript(simulator, phase)
	if err != nil {
		t.Fatalf("reading the %s/%s transcript: %v", simulator, phase, err)
	}
	props, err := inditest.Definitions(data)
	if err != nil {
		t.Fatalf("parsing the definitions of %s/%s: %v", simulator, phase, err)
	}
	return props
}

// fakeDriver is one simulator behind a fake server.
type fakeDriver struct {
	device    string
	onConnect []*inditest.Property
	props     []*inditest.Property
	connected bool
	// snooped maps "<device>.<property>" to member values, from each
	// report that a client relayed to this driver (fakelocks_test.go).
	snooped map[string]map[string]string
	// saved is the driver's configuration file (fakeconfig_test.go),
	// keyed by property name. It is nil until the driver first saves.
	saved map[string][]inditest.Member
}

// transcriptCache holds the parsed transcripts of each driver
// executable, so the test binary parses them once.
var transcriptCache sync.Map

type transcriptPair struct{ baseline, connect []*inditest.Property }

// newFakeDriver returns a new driver for the executable exec, from the
// transcripts of its simulator or of a variant (fakevariants_test.go).
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

// fakeServer is one INDI server and the drivers it runs.
type fakeServer struct {
	// uid is the server pod's UID. When the pod is replaced, sync
	// starts a new fakeServer.
	uid string
	// drivers is keyed by device pod name, and uids holds the UID of the
	// device pod that each driver started for.
	drivers map[string]*fakeDriver
	uids    map[string]string
	conns   []*inditest.Conn
	// target is the list of device pods in the server pod's annotation
	// now, and applied is the list that the shim has applied. They
	// differ while the shim's delay runs (fakeshim_test.go).
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
	// held is the set of properties, as "<device>.<property>", whose
	// driver never responds to a change.
	held map[string]bool
	// refused is the set of properties whose driver responds to each
	// change with Alert and keeps its old values, as a driver does when
	// its hardware fails.
	refused map[string]bool
	// moving is the set of properties whose driver responds to a change
	// with Busy, as a mount does while it slews, until a test calls
	// setState. A change that arrives while the property is Busy
	// interrupts it: the members go Off and the state goes to Alert.
	moving map[string]bool
	// lateDials is how many more dials to each server fail
	// (listenLate).
	lateDials map[string]int
	// presets maps "<device>.<property>" to the switch member that a
	// driver sets to On when it defines the property
	// (fakepresets_test.go).
	presets map[string]string
	// texts maps "<device>.<property>.<member>" to the text that a
	// driver gives the member when it defines the property
	// (fakepresets_test.go).
	texts map[string]string
	// withheld is the set of properties, as "<device>.<property>", that
	// a driver leaves out of its definitions on connect until reveal
	// defines them (fakepresets_test.go).
	withheld map[string]bool
	// silent is the set of devices whose driver ignores a
	// getProperties that names the device, and asked counts the getProperties messages that
	// name each device (fakesync_test.go).
	silent map[string]bool
	asked  map[string]int
	// late is the set of servers that hold back their response to a
	// getProperties that names no device, until sendDelayedDefinitions. waiting
	// holds the connections that wait for it (fakesync_test.go).
	late    map[string]bool
	waiting map[string][]*inditest.Conn
	// shimDelay is the time between a change to a server pod's
	// annotation and the shim starting or stopping the driver
	// (fakeshim_test.go).
	shimDelay time.Duration
	// restarts records each driver whose device pod was gone while the
	// shim still listed it, as "<server> <pod>".
	restarts []string

	// followMu serializes catchUp, and guards last, the pods that the
	// servers matched at the last catchUp.
	followMu sync.Mutex
	last     map[string]map[string]any
}

func startIndiWorld(t *testing.T, api *fakeAPI) *indiWorld {
	w := &indiWorld{t: t, api: api, servers: map[string]*fakeServer{}, held: map[string]bool{}, refused: map[string]bool{}, moving: map[string]bool{}, lateDials: map[string]int{}, presets: map[string]string{}, texts: map[string]string{}, withheld: map[string]bool{}, silent: map[string]bool{}, asked: map[string]int{}, late: map[string]bool{}, waiting: map[string][]*inditest.Conn{}}
	go w.follow(t.Context())
	return w
}

// follow calls catchUp after each change to the fake API server's pods,
// until ctx ends, and then closes every client connection. It reads the
// change channel before it calls catchUp, so a change during catchUp
// wakes it again.
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

// catchUp makes the servers and drivers match the pods in the fake API
// server. It copies the map of pods but not the pods, because the fake
// API server never changes a stored object (fakeAPI.store). DialContext
// calls it too, because the operator's watch can deliver a Ready server
// pod before follow wakes, and a real server listens as soon as its pod
// is Ready.
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

// samePods reports whether two reads of the pods hold the same stored
// objects, by resourceVersion.
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

// links returns the names of the device pods in a server pod's
// annotation, which lists each one as an address like east-focuser:7625.
func links(p map[string]any) []string {
	var out []string
	annotations, _ := p["metadata"].(map[string]any)["annotations"].(map[string]any)
	list, _ := annotations[annotationDrivers].(string)
	for _, address := range strings.Fields(list) {
		out = append(out, strings.TrimSuffix(address, ":7625"))
	}
	return out
}

// execOf returns the driver executable that a device pod's socat runs,
// from the EXEC: address in its arguments.
func execOf(p map[string]any) string {
	container := p["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)
	args := container["args"].([]any)
	return strings.TrimSuffix(strings.TrimPrefix(args[1].(string), "EXEC:"), ",pipes")
}

// sync makes the servers and drivers match pods. When a server pod is
// gone, replaced, or not Ready, sync closes the server's connections and
// drops the server, so its drivers start again with the next Ready
// server pod. A server runs a driver for each Ready device pod that the
// shim has applied.
func (w *indiWorld) sync(pods map[string]map[string]any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for name, s := range w.servers {
		if p, ok := pods[name]; !ok || podUID(p) != s.uid || !podReady(p) {
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

// DialContext returns an in-memory connection to the server named by
// the first label of address, such as east-telescope.observatory.svc:7624.
// It fails with ECONNREFUSED when no Ready server pod has that name, or
// while lateDials is positive for it.
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

// serve reads the messages of one client until its connection closes,
// and passes each to respond.
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
		w.respond(name, s, c, v)
	}
}

// respond handles one message from a client: a getProperties, a set
// vector that it relays to the drivers that snoop it, or a new vector
// that changes a driver's property.
func (w *indiWorld) respond(name string, s *fakeServer, c *inditest.Conn, v inditest.Vector) {
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
	// An abort property sets the Busy property that it aborts to Ok, as
	// the simulators' abort does.
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

// hold makes a driver ignore every change to one property.
func (w *indiWorld) hold(device, property string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.held[device+"."+property] = true
}

// changes returns the changes that clients sent, in order.
func (w *indiWorld) changes() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.sent)
}

// connected returns the sorted names of the devices of one server that
// are connected.
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

// release undoes hold. A change sent while the property was held gets
// no response.
func (w *indiWorld) release(device, property string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.held, device+"."+property)
}

// count returns how many changes clients sent to one property of one
// server, where property is named "<device>.<property>".
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

// state returns a property's state on one server, or "" when no driver
// of the server defines it. After a driver restarts, the state is the
// one in its transcript.
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
// client, as a driver does with a new reading.
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

// refuse makes a driver respond to every change to one property with
// Alert.
func (w *indiWorld) refuse(device, property string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.refused[device+"."+property] = true
}

// accept undoes refuse.
func (w *indiWorld) accept(device, property string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.refused, device+"."+property)
}

// listenLate makes the next dials connection attempts to a server fail
// with ECONNREFUSED, as they do before indiserver opens its port. A dial
// before the server pod is Ready also fails, and counts.
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

// clients returns how many client connections a server has open.
func (w *indiWorld) clients(server string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.servers[server].conns)
}
