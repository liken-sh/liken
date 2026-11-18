package main

// This file reads which devices the prepared claims on this node hold,
// and which device nodes each container received. The source is the
// CDI spec files this driver writes: prepare writes one for each claim
// and unprepare removes it, so the files on this node's disk are the
// current record.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// claimedDevices returns the set of published device names that a
// prepared claim on this node holds. The media bus can be in the set.
// Callers look up controllers, and a controller's device name is never
// the bus's.
//
// The kubelet prepares a claim before the consumer's container starts
// and unprepares it after the container is gone, so a CDI spec file
// that names a device means a claim still holds it. Listing every
// ResourceClaim in the cluster would give the same answer, but it
// would need RBAC to read every workload's claims for a fact that is
// already on this node's disk.
//
// A claim that the scheduler allocated and the kubelet has not
// prepared is not in the result, because its pod is not running yet.
// Before unpair removes the bond, it takes the device out of the
// ResourceSlice and stops the device's relay, and prepare fails for a
// controller with no relay. So such a claim cannot reach the
// controller later.
func claimedDevices() map[string]bool {
	claimed := map[string]bool{}
	eachPreparedDevice(func(prepared preparedDevice) {
		claimed[prepared.device] = true
	})
	return claimed
}

// movedControllers returns the addresses of the controllers whose
// containers received device nodes other than the ones virtual lists
// for them now. When the operator restarts, it creates every virtual
// device again, and the kernel can give the new devices different
// minor numbers. A container prepared before the restart can then
// hold a node that now belongs to another controller. When cdiDir
// cannot be read, the result is empty.
func movedControllers(virtual map[string][]string) map[string]bool {
	moved := map[string]bool{}
	eachPreparedDevice(func(prepared preparedDevice) {
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

// sameNodes reports whether two lists hold the same node paths,
// ignoring order. Prepare and the relay each list a controller's nodes
// in the order they found them, which can differ.
func sameNodes(prepared, current []string) bool {
	if len(prepared) != len(current) {
		return false
	}
	a, b := slices.Clone(prepared), slices.Clone(current)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// preparedDevice is one device of one prepared claim, read back from
// the CDI spec file that prepare wrote.
type preparedDevice struct {
	claimUID string
	device   string
	nodes    []string
	inputs   []string
	axes     string
}

// controller returns the device's controller address, or false for
// the media bus and for a name that is not an address.
func (d preparedDevice) controller() (string, bool) {
	if isMediaBusName(d.device) {
		return "", false
	}
	mac := macFromDeviceName(d.device)
	if !validMAC(mac) {
		return "", false
	}
	return mac, true
}

// eachPreparedDevice calls visit for every device in every CDI spec
// file in cdiDir. A file that cannot be read or parsed is skipped. The
// kubelet can unprepare a claim between the directory listing and the
// read, which removes its file, so a read failure is expected.
func eachPreparedDevice(visit func(prepared preparedDevice)) {
	entries, err := os.ReadDir(cdiDir)
	if err != nil {
		// The directory does not exist until the first claim is prepared.
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
			// prepare names each CDI device <claim UID>-<device name>, so
			// stripping the prefix recovers the device name without a call
			// to the API server.
			allocated, ok := strings.CutPrefix(device.Name, claimUID+"-")
			if !ok {
				continue
			}
			nodes := make([]string, 0, len(device.ContainerEdits.DeviceNodes))
			for _, node := range device.ContainerEdits.DeviceNodes {
				nodes = append(nodes, node.Path)
			}
			visit(preparedDevice{
				claimUID: claimUID,
				device:   allocated,
				nodes:    nodes,
				inputs:   device.inputs(),
				axes:     device.axes(),
			})
		}
	}
}
