package main

// The fake drivers save their configuration the way INDI's base
// classes do (libs/indibase/defaultdevice.cpp and inditelescope.cpp in
// indilib/indi). A driver saves every property when a client sends
// CONFIG_PROCESS with CONFIG_SAVE. A mount also saves every property
// the first time a client writes ACTIVE_DEVICES or GEOGRAPHIC_COORD,
// because saving one property when no file exists writes all of them.
// A mount reads DOME_POLICY from the saved file on each getProperties,
// so a client that connects later gets the saved policy, not the value
// that an earlier client set.

import (
	"slices"

	"github.com/liken-sh/liken/observatory-operator/indi/inditest"
)

// saveConfig saves the configuration after a client changed p, when a
// real INDI driver would.
func (d *fakeDriver) saveConfig(p *inditest.Property) {
	mount := d.find("DOME_POLICY") != nil
	switch {
	case p.Name == "CONFIG_PROCESS":
		save := d.value("CONFIG_PROCESS", "CONFIG_SAVE") == "On"
		// A driver sets every member of CONFIG_PROCESS Off after it acts.
		p.Switch("")
		if save {
			d.save()
		}
	case mount && d.saved == nil && (p.Name == "ACTIVE_DEVICES" || p.Name == "GEOGRAPHIC_COORD"):
		d.save()
	}
}

func (d *fakeDriver) save() {
	d.saved = map[string][]inditest.Member{}
	for _, p := range d.props {
		d.saved[p.Name] = slices.Clone(p.Members)
	}
}

// reloadDomePolicy sets DOME_POLICY of a mount to the saved value, as
// Telescope::ISGetProperties does before it defines the property.
func (d *fakeDriver) reloadDomePolicy() {
	p := d.find("DOME_POLICY")
	if p == nil || d.saved["DOME_POLICY"] == nil {
		return
	}
	p.Members = slices.Clone(d.saved["DOME_POLICY"])
}
