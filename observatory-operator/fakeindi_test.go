package main

// Fake INDI servers for the operator's tests, built from the bytes that
// the real simulators sent, which indi/testdata/ records. Each server
// runs a driver for each device pod that its server pod links to and
// that the fake API server holds Ready, as indiserver does through the
// shims: a device pod that comes or goes defines or deletes its device
// on the server, and a new pod is a new driver, disconnected, with its
// settings lost. A driver defines what its baseline transcript
// defines, adds what its connect transcript defines when it connects,
// and answers every other change with the values sent and the state
// Ok. A test can hold a property, so that its driver never answers.
//
// The servers answer over in-memory pipes, so the tests run in a
// synctest bubble.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
)

type fakeMember struct {
	Name, Label, Format, Min, Max, Step, Value string
}

type fakeProperty struct {
	Type                                          string
	Device, Name, Label, Group, State, Perm, Rule string
	Members                                       []fakeMember
}

func (p *fakeProperty) copy() *fakeProperty {
	out := *p
	out.Members = slices.Clone(p.Members)
	return &out
}

func escape(text string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(text))
	return b.String()
}

func (p *fakeProperty) def() string {
	var b strings.Builder
	fmt.Fprintf(&b, `<def%sVector device="%s" name="%s" label="%s" group="%s" state="%s"`, p.Type, escape(p.Device), escape(p.Name), escape(p.Label), escape(p.Group), p.State)
	if p.Type != "Light" {
		fmt.Fprintf(&b, ` perm="%s"`, p.Perm)
	}
	if p.Type == "Switch" {
		fmt.Fprintf(&b, ` rule="%s"`, p.Rule)
	}
	b.WriteString(` timeout="60">`)
	for _, m := range p.Members {
		fmt.Fprintf(&b, `<def%s name="%s" label="%s"`, p.Type, escape(m.Name), escape(m.Label))
		if p.Type == "Number" {
			fmt.Fprintf(&b, ` format="%s" min="%s" max="%s" step="%s"`, escape(m.Format), m.Min, m.Max, m.Step)
		}
		fmt.Fprintf(&b, ">%s</def%s>", escape(m.Value), p.Type)
	}
	fmt.Fprintf(&b, "</def%sVector>\n", p.Type)
	return b.String()
}

func (p *fakeProperty) set() string {
	var b strings.Builder
	fmt.Fprintf(&b, `<set%sVector device="%s" name="%s" state="%s">`, p.Type, escape(p.Device), escape(p.Name), p.State)
	for _, m := range p.Members {
		fmt.Fprintf(&b, `<one%s name="%s">%s</one%s>`, p.Type, escape(m.Name), escape(m.Value), p.Type)
	}
	fmt.Fprintf(&b, "</set%sVector>\n", p.Type)
	return b.String()
}

// xmlVector is one vector as a transcript or a client writes it.
type xmlVector struct {
	XMLName xml.Name
	Device  string `xml:"device,attr"`
	Name    string `xml:"name,attr"`
	Label   string `xml:"label,attr"`
	Group   string `xml:"group,attr"`
	State   string `xml:"state,attr"`
	Perm    string `xml:"perm,attr"`
	Rule    string `xml:"rule,attr"`
	Members []struct {
		Name   string `xml:"name,attr"`
		Label  string `xml:"label,attr"`
		Format string `xml:"format,attr"`
		Min    string `xml:"min,attr"`
		Max    string `xml:"max,attr"`
		Step   string `xml:"step,attr"`
		Value  string `xml:",chardata"`
	} `xml:",any"`
}

// definitions reads the def*Vector elements of one transcript, in
// order, with a later definition of a property in place of an earlier
// one.
func definitions(t *testing.T, path string) []*fakeProperty {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	decoder.Strict = false
	var out []*fakeProperty
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		var v xmlVector
		if err := decoder.DecodeElement(&v, &start); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		tag := v.XMLName.Local
		if !strings.HasPrefix(tag, "def") || !strings.HasSuffix(tag, "Vector") || v.Name == "" {
			continue
		}
		p := &fakeProperty{Type: strings.TrimSuffix(strings.TrimPrefix(tag, "def"), "Vector"),
			Device: v.Device, Name: v.Name, Label: v.Label, Group: v.Group, State: v.State, Perm: v.Perm, Rule: v.Rule}
		for _, m := range v.Members {
			p.Members = append(p.Members, fakeMember{m.Name, m.Label, m.Format, m.Min, m.Max, m.Step, strings.TrimSpace(m.Value)})
		}
		if i := slices.IndexFunc(out, func(o *fakeProperty) bool { return o.Name == p.Name }); i >= 0 {
			out[i] = p
		} else {
			out = append(out, p)
		}
	}
}

// fakeDriver is one simulator behind a fake server.
type fakeDriver struct {
	device    string
	onConnect []*fakeProperty
	props     []*fakeProperty
	connected bool
}

var transcriptCache sync.Map

func newFakeDriver(t *testing.T, exec string) *fakeDriver {
	dir := strings.TrimPrefix(exec, "indi_simulator_")
	type pair struct{ baseline, connect []*fakeProperty }
	cached, ok := transcriptCache.Load(dir)
	if !ok {
		base := filepath.Join("indi", "testdata", dir)
		cached = pair{definitions(t, filepath.Join(base, "baseline.xml")), definitions(t, filepath.Join(base, "connect.xml"))}
		transcriptCache.Store(dir, cached)
	}
	p := cached.(pair)
	d := &fakeDriver{}
	for _, prop := range p.baseline {
		d.props = append(d.props, prop.copy())
		d.device = prop.Device
	}
	for _, prop := range p.connect {
		if d.find(prop.Name) == nil {
			d.onConnect = append(d.onConnect, prop)
		}
	}
	return d
}

func (d *fakeDriver) find(name string) *fakeProperty {
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
	conns   []*fakeConn
}

type fakeConn struct {
	conn net.Conn
	mu   sync.Mutex
}

func (c *fakeConn) send(text string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, _ = io.WriteString(c.conn, text)
}

type indiWorld struct {
	t   *testing.T
	api *fakeAPI

	mu      sync.Mutex
	servers map[string]*fakeServer
	// sent records each change that a client sent, as
	// "<server> <device>.<property> <member>=<value> ...".
	sent []string
	// held holds the properties, as "<device>.<property>", whose
	// driver never answers a change.
	held map[string]bool
	// refused holds the properties whose driver answers each change
	// with Alert and keeps its values, as a driver whose hardware
	// fails does.
	refused map[string]bool
}

func startIndiWorld(t *testing.T, api *fakeAPI) *indiWorld {
	w := &indiWorld{t: t, api: api, servers: map[string]*fakeServer{}, held: map[string]bool{}, refused: map[string]bool{}}
	go w.follow(t.Context())
	return w
}

// follow brings each server's drivers in line with the pods after each
// change of the fake API server.
func (w *indiWorld) follow(ctx context.Context) {
	for {
		w.api.mu.Lock()
		changed := w.api.changed
		pods := map[string]map[string]any{}
		for name, p := range w.api.objects[podsCollection] {
			pods[name] = clone(p)
		}
		w.api.mu.Unlock()
		w.sync(pods)
		select {
		case <-ctx.Done():
			w.mu.Lock()
			for _, s := range w.servers {
				for _, c := range s.conns {
					_ = c.conn.Close()
				}
			}
			w.mu.Unlock()
			return
		case <-changed:
		}
	}
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

// links answers the device pods that a server pod's arguments name.
func links(p map[string]any) []string {
	var out []string
	container := p["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)
	for _, arg := range container["args"].([]any) {
		if s := arg.(string); strings.HasPrefix(s, linksDir+"/") {
			out = append(out, strings.TrimSuffix(strings.TrimPrefix(s, linksDir+"/"), ":7625"))
		}
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
				_ = c.conn.Close()
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
			s = &fakeServer{uid: podUID(p), drivers: map[string]*fakeDriver{}, uids: map[string]string{}}
			w.servers[name] = s
		}
		running := map[string]bool{}
		for _, link := range links(p) {
			device, ok := pods[link]
			if !ok || !podReady(device) {
				continue
			}
			running[link] = true
			if s.uids[link] == podUID(device) {
				continue
			}
			if old, ok := s.drivers[link]; ok {
				s.broadcast(fmt.Sprintf("<delProperty device=\"%s\"/>\n", escape(old.device)))
			}
			d := newFakeDriver(w.t, execOf(device))
			s.drivers[link], s.uids[link] = d, podUID(device)
			for _, prop := range d.props {
				s.broadcast(prop.def())
			}
		}
		for link, d := range s.drivers {
			if !running[link] {
				s.broadcast(fmt.Sprintf("<delProperty device=\"%s\"/>\n", escape(d.device)))
				delete(s.drivers, link)
				delete(s.uids, link)
			}
		}
	}
}

func (s *fakeServer) broadcast(text string) {
	for _, c := range s.conns {
		c.send(text)
	}
}

// DialContext answers a pipe to the server that an address names, such
// as telescope-east.observatory.svc:7624, while its pod is Ready.
func (w *indiWorld) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	name, _, _ := strings.Cut(address, ".")
	w.mu.Lock()
	defer w.mu.Unlock()
	s, ok := w.servers[name]
	if !ok {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	}
	client, server := net.Pipe()
	c := &fakeConn{conn: server}
	s.conns = append(s.conns, c)
	go w.serve(name, s, c)
	return client, nil
}

// serve reads one client's messages until the connection closes.
func (w *indiWorld) serve(name string, s *fakeServer, c *fakeConn) {
	decoder := xml.NewDecoder(bufio.NewReader(c.conn))
	for {
		token, err := decoder.Token()
		if err != nil {
			_ = c.conn.Close()
			return
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		var v xmlVector
		if err := decoder.DecodeElement(&v, &start); err != nil {
			_ = c.conn.Close()
			return
		}
		w.answer(name, s, c, v)
	}
}

func (w *indiWorld) answer(name string, s *fakeServer, c *fakeConn, v xmlVector) {
	w.mu.Lock()
	defer w.mu.Unlock()
	tag := v.XMLName.Local
	if tag == "getProperties" {
		for _, link := range sortedKeys(s.drivers) {
			for _, prop := range s.drivers[link].props {
				c.send(prop.def())
			}
		}
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
	if w.refused[v.Device+"."+v.Name] {
		p.State = "Alert"
		s.broadcast(p.set())
		return
	}
	for _, m := range v.Members {
		for i := range p.Members {
			if p.Members[i].Name == m.Name {
				p.Members[i].Value = strings.TrimSpace(m.Value)
			}
		}
	}
	p.State = "Ok"
	s.broadcast(p.set())
	// An abort ends what moves, as the simulators' abort does.
	if moving, ok := aborts[p.Name]; ok {
		if target := d.find(moving); target != nil && target.State == "Busy" {
			target.State = "Ok"
			s.broadcast(target.set())
		}
	}
	if p.Name != "CONNECTION" {
		return
	}
	connect := slices.ContainsFunc(p.Members, func(m fakeMember) bool { return m.Name == "CONNECT" && m.Value == "On" })
	switch {
	case connect && !d.connected:
		d.connected = true
		for _, prop := range d.onConnect {
			added := prop.copy()
			d.props = append(d.props, added)
			s.broadcast(added.def())
		}
	case !connect && d.connected:
		d.connected = false
		for _, prop := range d.onConnect {
			d.props = slices.DeleteFunc(d.props, func(o *fakeProperty) bool { return o.Name == prop.Name })
			s.broadcast(fmt.Sprintf("<delProperty device=\"%s\" name=\"%s\"/>\n", escape(d.device), escape(prop.Name)))
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
			w.servers[server].broadcast(p.set())
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
