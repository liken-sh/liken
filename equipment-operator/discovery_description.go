package main

// Discovery judges each LinkPlay device by its UPnP description before
// it creates a Receiver. Other brands build on the LinkPlay platform
// and advertise the same mDNS service, and the mDNS answer names no
// model. The description does, and every LinkPlay device serves it
// over plain HTTP. An Arylic amplifier serves its description while it
// refuses the HTTPS control API to the operator, so the driver's
// getStatusEx check alone never judges it.

import (
	"context"
	"fmt"

	"github.com/liken-sh/equipment-operator/upnp"
	"github.com/liken-sh/equipment-operator/wiim"
)

// judge reads the description of each device in one search that has no
// verdict yet, with one GET each. A description that names a model is
// the verdict for the operator's lifetime, and a device that is not a
// WiiM gets one log line. A description that cannot be read is not a
// verdict: the device gets a Receiver as it would with no description,
// the getStatusEx check judges it, and the next search that finds it
// reads the description again. The failure logs once until a read
// succeeds.
func (d *discovery) judge(ctx context.Context, found []wiim.Device) {
	for _, device := range found {
		if device.Description == "" {
			continue
		}
		if _, held := d.described(device.UUID); held {
			continue
		}
		description, err := upnp.Fetch(ctx, device.Description)
		if err != nil {
			if d.markUnread(device.UUID) {
				fmt.Fprintf(d.log, "discovery could not read the UPnP description of the LinkPlay device %s at %s, so its getStatusEx project decides whether it is a WiiM: %v\n",
					device.UUID, device.Address, err)
			}
			continue
		}
		d.mutex.Lock()
		d.descriptions[device.UUID] = description
		delete(d.unread, device.UUID)
		d.mutex.Unlock()
		if !wiim.IsWiiM(description.Model) {
			fmt.Fprintf(d.log, "discovery skipped the LinkPlay device %s at %s: its UPnP description names the manufacturer %s and the model %s, which is not a WiiM\n",
				device.UUID, device.Address, description.Manufacturer, description.Model)
		}
	}
}

// described answers the description discovery read for one identity,
// and whether it read one.
func (d *discovery) described(uuid string) (upnp.Description, bool) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	description, held := d.descriptions[wiim.NormalizeUUID(uuid)]
	return description, held
}

// markUnread records a failed read of one device's description, and
// answers whether it is the first failure since the last read that
// succeeded.
func (d *discovery) markUnread(uuid string) bool {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	first := !d.unread[uuid]
	d.unread[uuid] = true
	return first
}

// notWiiM answers why a device is not a WiiM, and an empty string for a
// WiiM or a device with no verdict. Either check is enough: the model
// in the description, which discovery reads before it creates a
// Receiver, or the project in getStatusEx, which the driver reads on
// every poll and which also covers a Receiver that a person declared.
func (d *discovery) notWiiM(uuid string) string {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	if description, held := d.descriptions[uuid]; held && !wiim.IsWiiM(description.Model) {
		return fmt.Sprintf("the UPnP description of the device %s names the model %s, which is not a WiiM", uuid, description.Model)
	}
	if project := d.foreign[uuid]; project != "" {
		return fmt.Sprintf("the device %s reports the project %s, which is not a WiiM", uuid, project)
	}
	return ""
}
