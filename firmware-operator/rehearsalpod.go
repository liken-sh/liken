package main

// A rehearsal is one bare pod on a machine that runs an approved
// install against a copy of the machine's firmware variables, and
// writes what the install would write to its log (scan/rehearse.go).
// It runs the scan image as `scan rehearse`.
//
// The pod is not privileged. It mounts the machine's /sys/firmware
// read-only, recursively, so the efivarfs mount under it is read-only
// too, and a rehearsal that goes wrong cannot write the real variable
// store. It drops every capability and adds DAC_READ_SEARCH, which
// reading the variables needs. fwupd's writes go to a copy in memory.

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// The label and the annotations of a rehearsal pod. The label selects
// the pods the operator watches, and the NetworkPolicy in
// deploy/rehearsal.yaml. The annotations record the attempt, so an
// operator that restarts during a rehearsal reads them from the pod.
// firmwareKey is the upload pod's, and machineKey, bootKey, catalogKey,
// and fileKey are the scan pod's.
const (
	rehearsalLabel      = "firmware.liken.sh/rehearsal"
	rehearsalLabelValue = "true"
	deviceIDKey         = "firmware.liken.sh/device-id"
	versionKey          = "firmware.liken.sh/version"
	cabinetKey          = "firmware.liken.sh/cabinet"
	rehearsalContainer  = "rehearse"
)

// The environment of `scan rehearse` beside the catalog's.
const (
	cabinetURLVariable    = "CABINET_URL"
	cabinetSHA256Variable = "CABINET_SHA256"
	deviceIDVariable      = "DEVICE_ID"
)

// Where the rehearsal pod mounts the machine's /sys/firmware and its
// directory in memory. scan/main.go names the same two paths.
const (
	hostFirmwarePath = "/host/firmware"
	rehearsalPath    = "/run/rehearsal"
)

// rehearsalAttempt is one rehearsal of one approval on one machine's
// device: the boot, the device, the approved version and cabinet, and
// the catalog file of the machine's last scan. Each attempt runs once,
// so a failed rehearsal waits for one of them to change.
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

// rehearsalPodName answers the one name of an attempt, for the reasons
// of scanPodName. The name starts with the Firmware and the machine,
// for a person who reads kubectl get pods.
func rehearsalPodName(a rehearsalAttempt) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{a.firmware, a.machine, a.boot, a.device, a.version, a.cabinet, a.catalog, a.file}, "\x00")))
	suffix := "-rehearsal-" + hex.EncodeToString(sum[:6])
	prefix := a.firmware + "-" + a.machine
	if len(prefix) > 63-len(suffix) {
		prefix = prefix[:63-len(suffix)]
	}
	return strings.TrimRight(prefix, "-.") + suffix
}

// rehearsalOf answers the attempt that a rehearsal pod's annotations
// record.
func rehearsalOf(p pod) rehearsalAttempt {
	notes := p.Metadata.Annotations
	return rehearsalAttempt{firmware: notes[firmwareKey], machine: notes[machineKey], boot: notes[bootKey],
		device: notes[deviceIDKey], version: notes[versionKey], cabinet: notes[cabinetKey],
		catalog: notes[catalogKey], file: notes[fileKey]}
}

// buildRehearsalPod answers the pod of one attempt. c is the
// FirmwareCatalog of the machine's last scan, whose file the
// rehearsal reads, and cabinetURL is the approval's.
func buildRehearsalPod(f *firmware, a rehearsalAttempt, c *catalog, cabinetURL, image, namespace string) pod {
	no := false
	deadline, grace := int64(scanDeadline.Seconds()), int64(5)
	return pod{
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
		Spec: podSpec{
			// The pod runs once, and stops at the deadline, for the
			// reasons of the scan pod (scanpod.go).
			RestartPolicy:                 "Never",
			ActiveDeadlineSeconds:         &deadline,
			TerminationGracePeriodSeconds: &grace,
			NodeName:                      a.machine,
			Tolerations:                   []toleration{{Operator: "Exists"}},
			AutomountServiceAccountToken:  &no,
			EnableServiceLinks:            &no,
			Volumes: []volume{
				{Name: "host-firmware", HostPath: &hostPath{Path: "/sys/firmware", Type: "Directory"}},
				// The copy of the variable store is under a megabyte. The
				// rest is the catalog, the cabinet, and fwupd's cache of
				// the catalog.
				{Name: "rehearsal", EmptyDir: &emptyDir{Medium: "Memory", SizeLimit: "64Mi"}},
			},
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
				VolumeMounts: []volumeMount{
					{Name: "host-firmware", MountPath: hostFirmwarePath, ReadOnly: true, RecursiveReadOnly: "Enabled"},
					{Name: "rehearsal", MountPath: rehearsalPath},
				},
				Resources:                &resources{Requests: map[string]string{"cpu": "10m", "memory": "64Mi"}},
				TerminationMessagePolicy: "FallbackToLogsOnError",
			}},
		},
	}
}
