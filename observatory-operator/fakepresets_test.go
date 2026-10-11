package main

// A preset is the state that a fake driver starts in, in place of the
// transcript's. Every simulator starts unparked and open, so a test
// that must see a park or a cover move presets the other side.

import (
	"fmt"
	"slices"
)

// preset makes each driver of a device turn one member of a switch
// property On, and the others Off, when it defines the property.
func (w *indiWorld) preset(device, property, member string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.presets[device+"."+property] = member
}

// presetText makes each driver of a device give one member of a text
// property a value when it defines the property, in place of the
// transcript's.
func (w *indiWorld) presetText(device, property, member, value string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.texts[device+"."+property+"."+member] = value
}

// applyPreset sets a property that a driver defines to its preset. The
// caller holds w.mu.
func (w *indiWorld) applyPreset(p *fakeProperty) {
	if member, ok := w.presets[p.Device+"."+p.Name]; ok {
		p.setSwitch(member)
	}
	for i, m := range p.Members {
		if text, ok := w.texts[p.Device+"."+p.Name+"."+m.Name]; ok {
			p.Members[i].Value = text
		}
	}
}

// withhold makes each driver of a device leave one property out when it
// connects, as a driver that defines it only later does.
func (w *indiWorld) withhold(device, property string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.withheld[device+"."+property] = true
}

// reveal makes each connected driver of a device define a property that
// it withheld.
func (w *indiWorld) reveal(device, property string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.withheld, device+"."+property)
	for _, s := range w.servers {
		for _, d := range s.drivers {
			if d.device != device || !d.connected || d.find(property) != nil {
				continue
			}
			for _, prop := range d.onConnect {
				if prop.Name == property {
					added := prop.copy()
					w.applyPreset(added)
					d.props = append(d.props, added)
					s.broadcast(added.def())
				}
			}
		}
	}
}

// conceal makes each connected driver of a device delete a property and
// leave it out until reveal defines it again.
func (w *indiWorld) conceal(device, property string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.withheld[device+"."+property] = true
	for _, s := range w.servers {
		for _, d := range s.drivers {
			if d.device != device || d.find(property) == nil {
				continue
			}
			d.props = slices.DeleteFunc(d.props, func(p *fakeProperty) bool { return p.Name == property })
			s.broadcast(fmt.Sprintf("<delProperty device=\"%s\" name=\"%s\"/>\n", escape(device), escape(property)))
		}
	}
}
