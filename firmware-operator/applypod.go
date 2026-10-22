package main

// An apply is one bare pod on a drained machine that writes an
// approved Secure Boot database update to the machine's real variable
// (scan/apply.go). It runs the scan image as `scan apply`, and starts
// only when machine-operator holds the machine's turn after the drain
// (disruption.go).
//
// The pod is not privileged. It mounts the machine's /sys/firmware
// read-write, so the efivarfs mount under it takes the write. It drops
// every capability and adds two: DAC_READ_SEARCH, which reading the
// variables needs, and LINUX_IMMUTABLE, which clearing the immutable
// flag that efivarfs sets on the variable needs. Before it writes, it
// rehearses the install again in memory, and it writes nothing unless
// the append has the SHA-256 that the rehearsal recorded.

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// The label and the annotations of an apply pod. The label selects the
// pods the operator watches, and the NetworkPolicy in deploy/apply.yaml.
// The annotations record the attempt, so an operator that restarts
// during an apply reads them from the pod. The other keys are the
// rehearsal pod's.
const (
	applyLabel      = "firmware.liken.sh/apply"
	applyLabelValue = "true"
	appendKey       = "firmware.liken.sh/append-sha256"
	applyContainer  = "apply"
)

// appendSHA256Variable is the environment of `scan apply` beside the
// rehearsal's: the SHA-256 of the append that the rehearsal recorded.
const appendSHA256Variable = "APPEND_SHA256"

// applyAttempt is one write of one approval on one machine: the boot
// the rehearsal passed on, the approved version and cabinet, and the
// append. Each attempt runs once, and a person retries a failed one by
// withdrawing the approval and approving it again.
type applyAttempt struct {
	firmware string
	machine  string
	boot     string
	version  string
	cabinet  string
	sha256   string
}

func (a applyAttempt) is(r applyRecord) bool {
	return r.Machine == a.machine && r.Boot == a.boot && r.Version == a.version &&
		r.CabinetSHA256 == a.cabinet && r.SHA256 == a.sha256
}

func attemptOfApply(firmware string, r applyRecord) applyAttempt {
	return applyAttempt{firmware: firmware, machine: r.Machine, boot: r.Boot, version: r.Version, cabinet: r.CabinetSHA256, sha256: r.SHA256}
}

// applyPodName answers the one name of an attempt, for the reasons of
// rehearsalPodName.
func applyPodName(a applyAttempt) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{a.firmware, a.machine, a.boot, a.version, a.cabinet, a.sha256}, "\x00")))
	suffix := "-apply-" + hex.EncodeToString(sum[:6])
	prefix := a.firmware + "-" + a.machine
	if len(prefix) > 63-len(suffix) {
		prefix = prefix[:63-len(suffix)]
	}
	return strings.TrimRight(prefix, "-.") + suffix
}

// applyOf answers the attempt that an apply pod's annotations record.
func applyOf(p pod) applyAttempt {
	notes := p.Metadata.Annotations
	return applyAttempt{firmware: notes[firmwareKey], machine: notes[machineKey], boot: notes[bootKey],
		version: notes[versionKey], cabinet: notes[cabinetKey], sha256: notes[appendKey]}
}

// buildApplyPod answers the pod of one attempt. r is the machine's
// passed rehearsal, whose device the apply installs on, and c is its
// FirmwareCatalog, whose current file the apply loads. The rehearsal
// in the pod checks the append, so a catalog that moved since the
// rehearsal changes nothing that is written.
func buildApplyPod(f *firmware, a applyAttempt, r rehearsal, c *catalog, image, namespace string) pod {
	no := false
	deadline, grace := int64(scanDeadline.Seconds()), int64(5)
	return pod{
		APIVersion: "v1",
		Kind:       "Pod",
		Metadata: objectMeta{
			Name:      applyPodName(a),
			Namespace: namespace,
			Labels: map[string]string{
				applyLabel:                     applyLabelValue,
				"app.kubernetes.io/managed-by": component,
			},
			Annotations: map[string]string{
				firmwareKey: a.firmware,
				machineKey:  a.machine,
				bootKey:     a.boot,
				versionKey:  a.version,
				cabinetKey:  a.cabinet,
				appendKey:   a.sha256,
				deviceIDKey: r.DeviceID,
			},
			// A deleted Firmware deletes its apply pods.
			OwnerReferences: []ownerReference{{
				APIVersion: firmwareAPIVersion, Kind: firmwareKind, Name: f.Metadata.Name, UID: f.Metadata.UID,
			}},
		},
		Spec: podSpec{
			// The pod runs once, and stops at the deadline, for the
			// reasons of the scan pod (scanpod.go). It tolerates every
			// taint, which covers the cordon of the drain.
			RestartPolicy:                 "Never",
			ActiveDeadlineSeconds:         &deadline,
			TerminationGracePeriodSeconds: &grace,
			NodeName:                      a.machine,
			Tolerations:                   []toleration{{Operator: "Exists"}},
			AutomountServiceAccountToken:  &no,
			EnableServiceLinks:            &no,
			Volumes: []volume{
				{Name: "host-firmware", HostPath: &hostPath{Path: "/sys/firmware", Type: "Directory"}},
				// The copy of the rehearsal before the write, as in the
				// rehearsal pod.
				{Name: "rehearsal", EmptyDir: &emptyDir{Medium: "Memory", SizeLimit: "64Mi"}},
			},
			Containers: []container{{
				Name:  applyContainer,
				Image: image,
				Args:  []string{"apply"},
				Env: []envVar{
					{Name: catalogNameVariable, Value: c.Metadata.Name},
					{Name: catalogURLVariable, Value: c.Spec.URL},
					{Name: catalogFileVariable, Value: c.Status.File},
					{Name: catalogSHA256Variable, Value: c.Status.SHA256},
					{Name: cabinetURLVariable, Value: f.Spec.Update.Cabinet.URL},
					{Name: cabinetSHA256Variable, Value: a.cabinet},
					{Name: deviceIDVariable, Value: r.DeviceID},
					{Name: appendSHA256Variable, Value: a.sha256},
				},
				SecurityContext: &securityContext{
					AllowPrivilegeEscalation: &no,
					Capabilities:             &capabilities{Drop: []string{"ALL"}, Add: []string{"DAC_READ_SEARCH", "LINUX_IMMUTABLE"}},
				},
				VolumeMounts: []volumeMount{
					{Name: "host-firmware", MountPath: hostFirmwarePath},
					{Name: "rehearsal", MountPath: rehearsalPath},
				},
				Resources:                &resources{Requests: map[string]string{"cpu": "10m", "memory": "64Mi"}},
				TerminationMessagePolicy: "FallbackToLogsOnError",
			}},
		},
	}
}
