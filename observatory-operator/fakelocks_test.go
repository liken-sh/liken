package main

// The fake drivers' lock policies, as INDI's mount and dome base
// classes enforce them (libs/indibase/inditelescope.cpp and
// indidome.cpp in indilib/indi). A mount snoops the DOME_PARK of the
// dome its ACTIVE_DOME names, and only while it is connected. A dome
// snoops the TELESCOPE_PARK of the mount its ACTIVE_TELESCOPE names.
// Each reads a snooped report only when its state is Ok. indiserver
// hands a driver a report that a client relays as a set*Vector, and
// so does the fake.

import (
	"encoding/xml"
	"fmt"
	"strings"
)

// relay records a report that a client relayed, and hands it to each
// driver of the server that snoops it.
func (w *indiWorld) relay(server string, s *fakeServer, v xmlVector) {
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

// snoops answers whether a driver snoops one property of one device.
func (d *fakeDriver) snoops(device, property string) bool {
	switch {
	case d.find("DOME_POLICY") != nil:
		return d.connected && property == "DOME_PARK" && d.value("ACTIVE_DEVICES", "ACTIVE_DOME") == device
	case d.find("MOUNT_POLICY") != nil:
		return property == "TELESCOPE_PARK" && d.value("ACTIVE_DEVICES", "ACTIVE_TELESCOPE") == device
	}
	return false
}

// lockRefuses answers whether a lock refuses a change, and sets the
// property as the driver reports a refusal: Alert, with the position it
// keeps.
func (d *fakeDriver) lockRefuses(p *fakeProperty, v xmlVector) bool {
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
		p.setSwitch("PARK")
	case v.Name == "DOME_PARK" && asks("PARK") && d.value("MOUNT_POLICY", "MOUNT_LOCKS") == "On" &&
		d.snooped[d.value("ACTIVE_DEVICES", "ACTIVE_TELESCOPE")+".TELESCOPE_PARK"]["UNPARK"] == "On":
		p.setSwitch("UNPARK")
	default:
		return false
	}
	p.State = "Alert"
	return true
}

// value answers one member's value of one property, or "".
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

// setSwitch turns one member On and the others Off.
func (p *fakeProperty) setSwitch(on string) {
	for i := range p.Members {
		p.Members[i].Value = "Off"
		if p.Members[i].Name == on {
			p.Members[i].Value = "On"
		}
	}
}

// ask sends one change to a server as another client does, such as a
// person's KStars, and the server's driver answers it.
func (w *indiWorld) ask(server, device, property string, members ...string) {
	w.t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, `<newSwitchVector device="%s" name="%s">`, escape(device), escape(property))
	for _, m := range members {
		name, value, _ := strings.Cut(m, "=")
		fmt.Fprintf(&b, `<oneSwitch name="%s">%s</oneSwitch>`, name, value)
	}
	b.WriteString("</newSwitchVector>")
	var v xmlVector
	if err := xml.Unmarshal([]byte(b.String()), &v); err != nil {
		w.t.Fatal(err)
	}
	w.mu.Lock()
	s := w.servers[server]
	w.mu.Unlock()
	w.answer(server, s, nil, v)
}

// relays answers the reports that clients relayed, in order.
func (w *indiWorld) relays() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.relayed...)
}

// parked answers a park property on one server as "<state> <member>",
// with the member that is On, such as "Alert PARK".
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

// shutterFollowsPark moves a dome's shutter when a client changes its
// park state, as DOME_SHUTTER_PARK_POLICY tells INDI's dome to
// (Dome::Park and Dome::UnPark in libs/indibase/indidome.cpp). The
// driver acts on the policy only in those handlers, so a park that
// finds the dome there already moves no shutter. The fake sends the
// shutter's update before the park's, so a client that reads the
// shutter after the park settles reads where the policy put it. The
// caller holds w.mu.
func (d *fakeDriver) shutterFollowsPark(s *fakeServer) {
	shutter := d.find("DOME_SHUTTER")
	if shutter == nil {
		return
	}
	switch {
	case d.value("DOME_PARK", "PARK") == "On" && d.value("DOME_SHUTTER_PARK_POLICY", "SHUTTER_CLOSE_ON_PARK") == "On":
		shutter.setSwitch("SHUTTER_CLOSE")
	case d.value("DOME_PARK", "UNPARK") == "On" && d.value("DOME_SHUTTER_PARK_POLICY", "SHUTTER_OPEN_ON_UNPARK") == "On":
		shutter.setSwitch("SHUTTER_OPEN")
	default:
		return
	}
	shutter.State = "Ok"
	s.broadcast(shutter.set())
}
