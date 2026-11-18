package main

// A rehearsal is a bare pod that runs `scan rehearse` on the machine. It
// performs the approved install against a copy of the machine's firmware
// variables in memory, and logs what the install would write
// (scan/rehearse.go).
//
// The pod is not privileged. It mounts the machine's /sys/firmware
// recursively read-only, so the efivarfs mount under it is read-only
// too, and a rehearsal that goes wrong cannot write the real variable
// store. It drops every capability except DAC_READ_SEARCH, which it
// needs to read the variables.

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// The operator and the NetworkPolicy in deploy/rehearsal.yaml select
// rehearsal pods by rehearsalLabel. The annotations record the attempt,
// so an operator that restarts during a rehearsal reads the attempt from
// the pod. The pod also has firmwareKey and machineKey (uploadpod.go),
// and bootKey, catalogKey, and fileKey (scanpod.go).
const (
	rehearsalLabel      = "firmware.liken.sh/rehearsal"
	rehearsalLabelValue = "true"
	deviceIDKey         = "firmware.liken.sh/device-id"
	versionKey          = "firmware.liken.sh/version"
	cabinetKey          = "firmware.liken.sh/cabinet"
	rehearsalContainer  = "rehearse"
)

// The environment variables of `scan rehearse`, in addition to the
// catalog's.
const (
	cabinetURLVariable    = "CABINET_URL"
	cabinetSHA256Variable = "CABINET_SHA256"
	deviceIDVariable      = "DEVICE_ID"
)

// The mount paths of the rehearsal pod's /sys/firmware and of its
// directory in memory. scan/main.go has the same paths as hostFirmware
// and rehearsalDir.
const (
	hostFirmwarePath = "/host/firmware"
	rehearsalPath    = "/run/rehearsal"

	// provenESPLink is a link that `liken`'s init points at the ESP of
	// the machine's next boot. A rehearsal of a capsule copies the EFI
	// directory of that ESP, and an apply stages the capsule there.
	provenESPLink = "/run/liken/proven-esp"
	nextESPVolume = "next-esp"

	// copyVariablesVolume is a pod's copy of the variable store, in
	// memory. fwupd's uefi_capsule plugin finds devices only in a
	// variable store that is a writable mount point, so each pod that
	// gives fwupd a copy mounts this volume at the copy's efivars path.
	copyVariablesVolume = "copy-efivars"

	// firmwareCopyPath is where each pod copies the machine's firmware
	// tables for fwupd (firmwareCopy in scan/main.go). fwupd derives a
	// capsule device's ID from the path of its ESRT entry, so the scan,
	// the rehearsal, and the apply must copy to the same path to see the
	// device ID that the scan recorded.
	firmwareCopyPath   = "/run/fw"
	firmwareCopyVolume = "firmware-copy"
	// nextESPPath is where a pod mounts the next boot's ESP. The scan
	// program uses the same path (nextESPPath in scan/main.go).
	nextESPPath = "/run/esp-next"
)

// rehearsalAttempt identifies a rehearsal by the machine's boot, the
// device, the approved version and cabinet, and the catalog file of the
// machine's last scan. Each attempt runs once, so a failed rehearsal is
// not retried until one of these fields changes.
type rehearsalAttempt struct {
	firmware string
	machine  string
	boot     string
	device   string
	version  string
	cabinet  string
	catalog  string
	file     string
}

func (a rehearsalAttempt) is(r rehearsal) bool {
	return r.Machine == a.machine && r.Boot == a.boot && r.DeviceID == a.device &&
		r.Version == a.version && r.CabinetSHA256 == a.cabinet && r.Catalog == a.catalog && r.File == a.file
}

// rehearsalPodName returns the pod name of an attempt: the Firmware and
// the machine, cut to fit the 63-character limit, and a hash of the
// whole attempt, for the reasons given at scanPodName.
func rehearsalPodName(a rehearsalAttempt) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{a.firmware, a.machine, a.boot, a.device, a.version, a.cabinet, a.catalog, a.file}, "\x00")))
	suffix := "-rehearsal-" + hex.EncodeToString(sum[:6])
	prefix := a.firmware + "-" + a.machine
	if len(prefix) > 63-len(suffix) {
		prefix = prefix[:63-len(suffix)]
	}
	return strings.TrimRight(prefix, "-.") + suffix
}

func rehearsalOf(p pod) rehearsalAttempt {
	notes := p.Metadata.Annotations
	return rehearsalAttempt{firmware: notes[firmwareKey], machine: notes[machineKey], boot: notes[bootKey],
		device: notes[deviceIDKey], version: notes[versionKey], cabinet: notes[cabinetKey],
		catalog: notes[catalogKey], file: notes[fileKey]}
}

// buildRehearsalPod returns the pod for an attempt. c is the
// FirmwareCatalog of the machine's last scan. nextESP is true when the
// machine has an ESP for its next boot, and the pod then mounts it
// read-only for the rehearsal of a capsule.
func buildRehearsalPod(f *firmware, a rehearsalAttempt, c *catalog, cabinetURL, image, namespace string, nextESP bool) pod {
	no := false
	deadline, grace := int64(scanDeadline.Seconds()), int64(5)
	p := pod{
		APIVersion: "v1",
		Kind:       "Pod",
		Metadata: objectMeta{
			Name:      rehearsalPodName(a),
			Namespace: namespace,
			Labels: map[string]string{
				rehearsalLabel:                 rehearsalLabelValue,
				"app.kubernetes.io/managed-by": component,
			},
			Annotations: map[string]string{
				firmwareKey: a.firmware,
				machineKey:  a.machine,
				bootKey:     a.boot,
				deviceIDKey: a.device,
				versionKey:  a.version,
				cabinetKey:  a.cabinet,
				catalogKey:  a.catalog,
				fileKey:     a.file,
			},
			// A deleted Firmware deletes its rehearsal pods.
			OwnerReferences: []ownerReference{{
				APIVersion: firmwareAPIVersion, Kind: firmwareKind, Name: f.Metadata.Name, UID: f.Metadata.UID,
			}},
		},
		Spec: withTrust(podSpec{
			// The pod runs once and stops at the deadline, for the
			// reasons given in scanpod.go.
			RestartPolicy:                 "Never",
			ActiveDeadlineSeconds:         &deadline,
			TerminationGracePeriodSeconds: &grace,
			NodeName:                      a.machine,
			Tolerations:                   []toleration{{Operator: "Exists"}},
			AutomountServiceAccountToken:  &no,
			EnableServiceLinks:            &no,
			Volumes: append([]volume{
				{Name: "host-firmware", HostPath: &hostPath{Path: "/sys/firmware", Type: "Directory"}},
				// This volume stores the catalog, the cabinet, and fwupd's
				// cache of the catalog. For a capsule it also stores the
				// copy of the ESP's EFI directory and the capsule that
				// fwupd stages there. The size limit allows for a board's
				// capsule to be stored twice, in the cabinet and staged.
				{Name: "rehearsal", EmptyDir: &emptyDir{Medium: "Memory", SizeLimit: "256Mi"}},
			}, firmwareCopyVolumes()...),
			Containers: []container{{
				Name:  rehearsalContainer,
				Image: image,
				Args:  []string{"rehearse"},
				Env: []envVar{
					{Name: catalogNameVariable, Value: c.Metadata.Name},
					{Name: catalogURLVariable, Value: c.Spec.URL},
					{Name: catalogFileVariable, Value: a.file},
					{Name: catalogSHA256Variable, Value: c.Status.SHA256},
					{Name: cabinetURLVariable, Value: cabinetURL},
					{Name: cabinetSHA256Variable, Value: a.cabinet},
					{Name: deviceIDVariable, Value: a.device},
				},
				SecurityContext: &securityContext{
					AllowPrivilegeEscalation: &no,
					Capabilities:             &capabilities{Drop: []string{"ALL"}, Add: []string{"DAC_READ_SEARCH"}},
				},
				VolumeMounts: append([]volumeMount{
					{Name: "host-firmware", MountPath: hostFirmwarePath, ReadOnly: true, RecursiveReadOnly: "Enabled"},
					{Name: "rehearsal", MountPath: rehearsalPath},
				}, firmwareCopyMounts()...),
				Resources:                &resources{Requests: map[string]string{"cpu": "10m", "memory": "64Mi"}},
				TerminationMessagePolicy: "FallbackToLogsOnError",
			}},
		}, c),
	}
	if nextESP {
		p.Spec.Volumes = append(p.Spec.Volumes, volume{Name: nextESPVolume, HostPath: &hostPath{Path: provenESPLink, Type: "Directory"}})
		p.Spec.Containers[0].VolumeMounts = append(p.Spec.Containers[0].VolumeMounts,
			volumeMount{Name: nextESPVolume, MountPath: nextESPPath, ReadOnly: true})
	}
	return p
}

// firmwareCopyVolumes returns the volumes in memory for a pod's copy of
// the firmware tables and, as a separate mount point, the copy's
// variable store (copyVariablesVolume).
func firmwareCopyVolumes() []volume {
	return []volume{
		{Name: firmwareCopyVolume, EmptyDir: &emptyDir{Medium: "Memory", SizeLimit: "16Mi"}},
		{Name: copyVariablesVolume, EmptyDir: &emptyDir{Medium: "Memory", SizeLimit: "16Mi"}},
	}
}

func firmwareCopyMounts() []volumeMount {
	return []volumeMount{
		{Name: firmwareCopyVolume, MountPath: firmwareCopyPath},
		{Name: copyVariablesVolume, MountPath: firmwareCopyPath + "/efi/efivars"},
	}
}
