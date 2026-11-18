package main

// Discovery reads the UPnP description of each LinkPlay device before it
// creates a Receiver, because other brands built on LinkPlay advertise
// the same mDNS service as a WiiM, and only the description gives the
// model. The driver's getStatusEx check cannot replace it: an Arylic
// amplifier refuses the operator's requests to its HTTPS control API.

import (
	"context"
	"fmt"

	"github.com/liken-sh/liken/equipment-operator/upnp"
	"github.com/liken-sh/liken/equipment-operator/wiim"
)

// judge reads and caches the description of each found device that has
// none cached. When a read fails, the device is treated as if it had no
// description, so the getStatusEx check alone determines whether it is a
// WiiM, and the next search
// reads it again. Each failure streak is logged once.
func (d *discovery) judge(ctx context.Context, found []wiim.Device) {
	for _, device := range found {
		if device.Description == "" {
			continue
		}
		if _, held := d.described(device.UUID); held {
			continue
		}
		description, err := upnp.FetchVia(ctx, d.dial, device.Description)
		if err != nil {
			if d.markUnread(device.UUID) {
				fmt.Fprintf(d.log, "discovery read no UPnP description from the LinkPlay device %s at %s, so its getStatusEx project decides whether it is a WiiM: %v\n",
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

func (d *discovery) described(uuid string) (upnp.Description, bool) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	description, held := d.descriptions[wiim.NormalizeUUID(uuid)]
	return description, held
}

// markUnread records a failed read, and reports whether it is the
// first failure since the last successful read.
func (d *discovery) markUnread(uuid string) bool {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	first := !d.unread[uuid]
	d.unread[uuid] = true
	return first
}

// notWiiM returns the reason a device is not a WiiM, or "" when neither
// the description nor getStatusEx has shown that. The getStatusEx
// project is needed too, because the driver reads it on every poll, so
// it also covers a Receiver that a person declared.
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
