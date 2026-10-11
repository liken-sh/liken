package main

// What the prepared claims on this node hold. The record is the CDI
// spec files this driver wrote: prepare writes one for each claim, and
// unprepare removes it, so this node's own disk answers which devices a
// claim holds and which nodes each consumer received.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// claimedDevices names the devices the prepared claims on this node
// hold right now, by their published device names. The media bus can
// be among them. The one caller looks up controllers by their own
// names, and a controller's name never equals the bus's.
//
// The record is the CDI spec files this driver wrote. The kubelet
// prepares a claim before the consumer's container starts and
// unprepares it after the container is gone. Prepare creates those
// files and unprepare removes them, so a file that names a device is a
// claim that still holds it. The alternative is to list
// every ResourceClaim in the cluster and read its allocation, which
// widens this operator's grant to every workload's claims for a fact
// that is already on this node's disk.
//
// A claim that the scheduler allocated but the kubelet has not
// prepared yet is not in the result. That claim's pod is not running,
// so nothing is using the controller, and the device leaves the slice
// before the bond is removed, which keeps the allocation from
// reaching prepare afterwards.
func claimedDevices() map[string]bool {
	held := map[string]bool{}
	eachDeliveredDevice(func(prepared deliveredDevice) {
		held[prepared.device] = true
	})
	return held
}

// movedControllers names the controllers whose consumers hold nodes
// that are not the nodes the operator delivers now. The CDI spec file
// of a prepared claim records what its container received, and
// virtual is what the relay holds for each controller at this pass. A
// restart of this operator creates every virtual device again, and the
// kernel numbers them from the free minors, so a container prepared
// before the restart can hold a node that now belongs to a different
// controller. The media bus is skipped, because its delivery is a
// mount and no node. A directory this pass cannot read names nothing,
// so a read failure taints nothing.
func movedControllers(virtual map[string][]string) map[string]bool {
	moved := map[string]bool{}
	eachDeliveredDevice(func(prepared deliveredDevice) {
		mac, ok := prepared.controller()
		if !ok {
			return
		}
		if !sameNodes(prepared.nodes, virtual[mac]) {
			moved[mac] = true
		}
	})
	return moved
}

// sameNodes compares two lists of node paths as sets. The prepare
// call and the relay list a controller's nodes in the order they
// found them, and the order carries no meaning.
func sameNodes(delivered, current []string) bool {
	if len(delivered) != len(current) {
		return false
	}
	a, b := slices.Clone(delivered), slices.Clone(current)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// deliveredDevice is one prepared claim's grant of one device: which
// claim holds it, which published device it is, the node paths the
// container received, and what the claim asked for: the input classes
// and the axis values, each as this driver recorded them.
type deliveredDevice struct {
	claimUID string
	device   string
	nodes    []string
	inputs   []string
	axes     string
}

// controller answers with the MAC of the controller a grant names,
// and whether the grant names one at all. The media bus and a name
// that is not an address are not controllers.
func (d deliveredDevice) controller() (string, bool) {
	if isMediaBusName(d.device) {
		return "", false
	}
	mac := macFromDeviceName(d.device)
	if !validMAC(mac) {
		return "", false
	}
	return mac, true
}

// eachDeliveredDevice visits every device a prepared claim holds, with
// the node paths its CDI spec delivered. The kubelet prepares a claim
// before the consumer's container starts and unprepares it after the
// container is gone, and prepare writes the file while unprepare
// removes it, so a file that names a device is a claim that still
// holds it. A file this cannot parse is skipped, and a claim the
// kubelet unprepared between the listing and the read is gone by then.
func eachDeliveredDevice(visit func(prepared deliveredDevice)) {
	entries, err := os.ReadDir(cdiDir)
	if err != nil {
		// No directory means no claim has been prepared on this boot.
		return
	}
	for _, entry := range entries {
		claimUID, ok := claimUIDFromSpecName(entry.Name())
		if !ok {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(cdiDir, entry.Name()))
		if err != nil {
			continue
		}
		var spec cdiSpec
		if err := json.Unmarshal(raw, &spec); err != nil {
			continue
		}
		for _, device := range spec.Devices {
			// prepare names each CDI device for the claim and the
			// allocated device together, so the allocated name is in the
			// file and this needs no call to the API server.
			allocated, ok := strings.CutPrefix(device.Name, claimUID+"-")
			if !ok {
				continue
			}
			nodes := make([]string, 0, len(device.ContainerEdits.DeviceNodes))
			for _, node := range device.ContainerEdits.DeviceNodes {
				nodes = append(nodes, node.Path)
			}
			visit(deliveredDevice{
				claimUID: claimUID,
				device:   allocated,
				nodes:    nodes,
				inputs:   device.inputs(),
				axes:     device.axes(),
			})
		}
	}
}
