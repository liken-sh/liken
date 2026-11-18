package main

import (
	"fmt"
	"slices"

	"github.com/liken-sh/liken/observatory-operator/indi/inditest"
)

// preset makes each driver of a device define a switch property with
// member On and the other members Off, in place of the state in the
// simulator's transcript. The simulators start unparked and open, so a
// test that needs a device to unpark or open presets it parked or
// closed.
func (w *indiWorld) preset(device, property, member string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.presets[device+"."+property] = member
}

// presetText sets the value of one member of a text property that each
// driver of a device defines, in place of the transcript's value.
func (w *indiWorld) presetText(device, property, member, value string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.texts[device+"."+property+"."+member] = value
}

// applyPreset applies the presets to a property that a driver defines.
// The caller must hold w.mu.
func (w *indiWorld) applyPreset(p *inditest.Property) {
	if member, ok := w.presets[p.Device+"."+p.Name]; ok {
		p.Switch(member)
	}
	for i, m := range p.Members {
		if text, ok := w.texts[p.Device+"."+p.Name+"."+m.Name]; ok {
			p.Members[i].Value = text
		}
	}
}

// withhold makes each driver of a device leave out a property when it
// connects, as a real driver does that defines a property later.
func (w *indiWorld) withhold(device, property string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.withheld[device+"."+property] = true
}

// reveal makes each connected driver of a device define a property
// that withhold or conceal left out.
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
					added := prop.Clone()
					w.applyPreset(added)
					d.props = append(d.props, added)
					s.broadcast(added.Def())
				}
			}
		}
	}
}

// conceal makes each connected driver of a device delete a property,
// and keeps it out until reveal defines it again.
func (w *indiWorld) conceal(device, property string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.withheld[device+"."+property] = true
	for _, s := range w.servers {
		for _, d := range s.drivers {
			if d.device != device || d.find(property) == nil {
				continue
			}
			d.props = slices.DeleteFunc(d.props, func(p *inditest.Property) bool { return p.Name == property })
			s.broadcast(fmt.Sprintf("<delProperty device=\"%s\" name=\"%s\"/>\n", inditest.Escape(device), inditest.Escape(property)))
		}
	}
}
