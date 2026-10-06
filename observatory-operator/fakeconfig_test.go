package main

// The fake drivers' configuration files, as INDI's base classes keep
// them (libs/indibase/defaultdevice.cpp and inditelescope.cpp in
// indilib/indi). A driver saves its whole configuration when a client
// sends CONFIG_PROCESS CONFIG_SAVE. A mount also saves the whole
// configuration the first time a client writes ACTIVE_DEVICES or
// GEOGRAPHIC_COORD, because saveConfig of one property writes every
// property when no file exists yet. A mount reads DOME_POLICY from the
// file again on each getProperties, so a client that connects later
// turns the policy back to the saved value.

import "slices"

// saveConfig saves the configuration after a client changed p, as the
// driver does.
func (d *fakeDriver) saveConfig(p *fakeProperty) {
	mount := d.find("DOME_POLICY") != nil
	switch {
	case p.Name == "CONFIG_PROCESS":
		save := d.value("CONFIG_PROCESS", "CONFIG_SAVE") == "On"
		// The driver turns every member Off after it acts.
		p.setSwitch("")
		if save {
			d.save()
		}
	case mount && d.saved == nil && (p.Name == "ACTIVE_DEVICES" || p.Name == "GEOGRAPHIC_COORD"):
		d.save()
	}
}

func (d *fakeDriver) save() {
	d.saved = map[string][]fakeMember{}
	for _, p := range d.props {
		d.saved[p.Name] = slices.Clone(p.Members)
	}
}

// reloadDomePolicy sets a mount's DOME_POLICY to the saved value, as
// Telescope::ISGetProperties does before it defines the property.
func (d *fakeDriver) reloadDomePolicy() {
	p := d.find("DOME_POLICY")
	if p == nil || d.saved["DOME_POLICY"] == nil {
		return
	}
	p.Members = slices.Clone(d.saved["DOME_POLICY"])
}
