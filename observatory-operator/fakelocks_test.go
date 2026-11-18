package main

// The lock policies of the fake drivers, which follow INDI's mount and
// dome base classes (libs/indibase/inditelescope.cpp and indidome.cpp
// in indilib/indi). A connected mount snoops DOME_PARK on the dome in
// its ACTIVE_DOME, and a dome snoops TELESCOPE_PARK on the mount in its
// ACTIVE_TELESCOPE. Each driver ignores a snooped update whose state is
// not Ok. indiserver passes a set*Vector that a client sends on to the
// drivers that snoop it, and the fake does the same.

import (
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/liken-sh/liken/observatory-operator/indi/inditest"
)

// relay records a set*Vector that a client sent, and passes it to each
// driver on the server that snoops it.
func (w *indiWorld) relay(server string, s *fakeServer, v inditest.Vector) {
	values := map[string]string{}
	var members []string
	for _, m := range v.Members {
		value := strings.TrimSpace(m.Value)
		values[m.Name] = value
		members = append(members, m.Name+"="+value)
	}
	w.relayed = append(w.relayed, fmt.Sprintf("%s %s.%s %s", server, v.Device, v.Name, strings.Join(members, " ")))
	if v.State != "Ok" {
		return
	}
	for _, d := range s.drivers {
		if d.snoops(v.Device, v.Name) {
			if d.snooped == nil {
				d.snooped = map[string]map[string]string{}
			}
			d.snooped[v.Device+"."+v.Name] = values
		}
	}
}

func (d *fakeDriver) snoops(device, property string) bool {
	switch {
	case d.find("DOME_POLICY") != nil:
		return d.connected && property == "DOME_PARK" && d.value("ACTIVE_DEVICES", "ACTIVE_DOME") == device
	case d.find("MOUNT_POLICY") != nil:
		return property == "TELESCOPE_PARK" && d.value("ACTIVE_DEVICES", "ACTIVE_TELESCOPE") == device
	}
	return false
}

// lockRefuses reports whether a lock refuses a park change. When it
// does, it sets the property as the driver reports a refusal: Alert,
// with the switch still on the current position.
func (d *fakeDriver) lockRefuses(p *inditest.Property, v inditest.Vector) bool {
	asks := func(member string) bool {
		for _, m := range v.Members {
			if m.Name == member && strings.TrimSpace(m.Value) == "On" {
				return true
			}
		}
		return false
	}
	switch {
	case v.Name == "TELESCOPE_PARK" && asks("UNPARK") && d.value("DOME_POLICY", "DOME_LOCKS") == "On" &&
		d.snooped[d.value("ACTIVE_DEVICES", "ACTIVE_DOME")+".DOME_PARK"]["PARK"] == "On":
		p.Switch("PARK")
	case v.Name == "DOME_PARK" && asks("PARK") && d.value("MOUNT_POLICY", "MOUNT_LOCKS") == "On" &&
		d.snooped[d.value("ACTIVE_DEVICES", "ACTIVE_TELESCOPE")+".TELESCOPE_PARK"]["UNPARK"] == "On":
		p.Switch("UNPARK")
	default:
		return false
	}
	p.State = "Alert"
	return true
}

func (d *fakeDriver) value(property, member string) string {
	if p := d.find(property); p != nil {
		for _, m := range p.Members {
			if m.Name == member {
				return m.Value
			}
		}
	}
	return ""
}

// ask sends a switch change to a server as a second client, such as a
// person's KStars, would.
func (w *indiWorld) ask(server, device, property string, members ...string) {
	w.t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, `<newSwitchVector device="%s" name="%s">`, inditest.Escape(device), inditest.Escape(property))
	for _, m := range members {
		name, value, _ := strings.Cut(m, "=")
		fmt.Fprintf(&b, `<oneSwitch name="%s">%s</oneSwitch>`, name, value)
	}
	b.WriteString("</newSwitchVector>")
	var v inditest.Vector
	if err := xml.Unmarshal([]byte(b.String()), &v); err != nil {
		w.t.Fatalf("parsing the switch vector: %v", err)
	}
	w.mu.Lock()
	s := w.servers[server]
	w.mu.Unlock()
	w.respond(server, s, nil, v)
}

func (w *indiWorld) relays() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.relayed...)
}

// parked returns a park property on one server as "<state> <member>",
// where member is the one that is On, such as "Alert PARK". It returns
// only the state when no member is On, and "" when the server has no
// such property.
func (w *indiWorld) parked(server, device, property string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, d := range w.servers[server].drivers {
		if p := d.find(property); d.device == device && p != nil {
			for _, m := range p.Members {
				if m.Value == "On" {
					return p.State + " " + m.Name
				}
			}
			return p.State
		}
	}
	return ""
}

// shutterFollowsPark moves a dome's shutter when its park state
// changes, as DOME_SHUTTER_PARK_POLICY tells INDI's dome to
// (Dome::Park and Dome::UnPark in libs/indibase/indidome.cpp). The
// real driver applies the policy only in those handlers, so a park
// request for a dome that is already parked moves no shutter. The fake
// sends the shutter's update before the park's update, so a client
// that reads the shutter after the park settles sees the new position.
// The caller holds w.mu.
func (d *fakeDriver) shutterFollowsPark(s *fakeServer) {
	shutter := d.find("DOME_SHUTTER")
	if shutter == nil {
		return
	}
	switch {
	case d.value("DOME_PARK", "PARK") == "On" && d.value("DOME_SHUTTER_PARK_POLICY", "SHUTTER_CLOSE_ON_PARK") == "On":
		shutter.Switch("SHUTTER_CLOSE")
	case d.value("DOME_PARK", "UNPARK") == "On" && d.value("DOME_SHUTTER_PARK_POLICY", "SHUTTER_OPEN_ON_UNPARK") == "On":
		shutter.Switch("SHUTTER_OPEN")
	default:
		return
	}
	shutter.State = "Ok"
	s.broadcast(shutter.Set())
}
