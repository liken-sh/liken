package main

// A preset is the state that a fake driver starts in, in place of the
// transcript's. Every simulator starts unparked and open, so a test
// that must see a park or a cover move presets the other side.

// preset makes each driver of a device turn one member of a switch
// property On, and the others Off, when it defines the property.
func (w *indiWorld) preset(device, property, member string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.presets[device+"."+property] = member
}

// applyPreset sets a property that a driver defines to its preset. The
// caller holds w.mu.
func (w *indiWorld) applyPreset(p *fakeProperty) {
	if member, ok := w.presets[p.Device+"."+p.Name]; ok {
		p.setSwitch(member)
	}
}
