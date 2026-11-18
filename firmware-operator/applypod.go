package main

// An apply pod writes an approved Secure Boot database update to the
// machine's variable store (scan/apply.go). It is a bare pod on the
// machine that runs the scan image as `scan apply`.
//
// The drain before the machine's reboot can evict the cluster's DNS, so
// the operator creates the pod as soon as the machine's rehearsal
// passes, and the pod does all its network work first: it loads the
// catalog, downloads the cabinet, and rehearses the install again. When
// that work is done, the readiness probe passes. The operator requests
// the reboot turn only after the pod is Ready, because on a machine
// with little to drain, machine-operator can hold the turn within a
// second of the request.
//
// When machine-operator holds the turn after the drain (disruption.go),
// the operator sets the pod's annotation firmware.liken.sh/write to go.
// The pod reads its annotations from a downward API volume, so it needs
// no API credentials, and the write itself needs no network.
//
// The pod stays on the machine through the drain, because the liken
// drain does not evict a pod that tolerates the cordon taint by its
// key. The pod has no activeDeadlineSeconds, because the turn can come
// much later. The operator deletes a pod that has no signal when its
// approval is withdrawn or its machine is no longer pending, and
// deletes a pod that has ended after it writes the pod's result to
// status.
//
// The pod is not privileged. It mounts the machine's /sys/firmware
// read-write, so the efivarfs mount under it accepts the write. It
// drops every capability and adds DAC_READ_SEARCH, to read the
// variables, and LINUX_IMMUTABLE, to clear the immutable flag that
// efivarfs sets on a variable.

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// The operator's watch and the NetworkPolicy in deploy/apply.yaml
// select apply pods by applyLabel. The annotations record the attempt,
// so an operator that restarts during an apply can read it back from
// the pod. Its other annotation keys are the ones that the scan,
// rehearsal, and upload pods use.
const (
	applyLabel      = "firmware.liken.sh/apply"
	applyLabelValue = "true"
	appendKey       = "firmware.liken.sh/append-sha256"
	applyContainer  = "apply"
)

// The operator signals the pod to write by setting writeKey to writeGo
// and writeAtKey to the time. The deadline of the apply counts from
// that time. The pod reads its annotations at signalPath.
const (
	writeKey   = "firmware.liken.sh/write"
	writeAtKey = "firmware.liken.sh/write-at"
	writeGo    = "go"
	signalPath = "/run/signal"
)

// The readiness probe of an apply pod runs `scan prepared`, which
// checks for the marker file that `scan apply` creates in preparedPath
// when its network work has finished. scanBinary is the path of the
// program in the scan image (scan/Dockerfile).
const (
	preparedPath = "/run/apply"
	scanBinary   = "/scan"
)

const cordonTaint = "node.kubernetes.io/unschedulable"

// signalOf returns when the operator signaled the apply pod to write,
// or the zero time when it has not. A signal annotation with no time,
// which only a person can write, counts from the pod's creation.
func signalOf(p pod) time.Time {
	notes := p.Metadata.Annotations
	if notes[writeKey] != writeGo {
		return time.Time{}
	}
	if sent := parseTime(notes[writeAtKey]); !sent.IsZero() {
		return sent
	}
	return parseTime(p.Metadata.CreationTimestamp)
}

func signalAnnotations(sent time.Time) map[string]string {
	return map[string]string{writeKey: writeGo, writeAtKey: stamp(sent)}
}

// appendSHA256Variable passes the pod the SHA-256 of the append that
// the rehearsal recorded.
const appendSHA256Variable = "APPEND_SHA256"

// applyAttempt is one write of one approval on one machine: the boot
// the rehearsal passed on, the approved version and cabinet, and the
// append. An attempt runs once. To retry a failed one, a person
// withdraws the approval and approves it again.
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

// applyPodName returns the pod name of an attempt. The name is derived
// from the attempt for the reasons given on rehearsalPodName.
func applyPodName(a applyAttempt) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{a.firmware, a.machine, a.boot, a.version, a.cabinet, a.sha256}, "\x00")))
	suffix := "-apply-" + hex.EncodeToString(sum[:6])
	prefix := a.firmware + "-" + a.machine
	if len(prefix) > 63-len(suffix) {
		prefix = prefix[:63-len(suffix)]
	}
	return strings.TrimRight(prefix, "-.") + suffix
}

// applyOf returns the attempt that an apply pod's annotations record.
func applyOf(p pod) applyAttempt {
	notes := p.Metadata.Annotations
	return applyAttempt{firmware: notes[firmwareKey], machine: notes[machineKey], boot: notes[bootKey],
		version: notes[versionKey], cabinet: notes[cabinetKey], sha256: notes[appendKey]}
}

// buildApplyPod returns the pod for one attempt. r is the machine's
// passed rehearsal, which names the device to install on, and c is the
// FirmwareCatalog whose current file the pod loads. The pod rehearses
// the install again and writes nothing unless the append has the
// SHA-256 that the first rehearsal recorded, so a catalog that changed
// since then cannot change what the pod writes. When signal is not
// zero, the pod is created with the signal, so it writes as soon as its
// network work is done.
func buildApplyPod(f *firmware, a applyAttempt, r rehearsal, c *catalog, image, namespace string, signal time.Time) pod {
	no := false
	grace := int64(5)
	p := pod{
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
			// Deleting the Firmware deletes its apply pods.
			OwnerReferences: []ownerReference{{
				APIVersion: firmwareAPIVersion, Kind: firmwareKind, Name: f.Metadata.Name, UID: f.Metadata.UID,
			}},
		},
		Spec: withTrust(podSpec{
			// The pod runs once. The first toleration keeps any taint
			// from evicting it. The second names the cordon taint by its
			// key, so the liken drain does not evict it.
			RestartPolicy:                 "Never",
			TerminationGracePeriodSeconds: &grace,
			NodeName:                      a.machine,
			Tolerations:                   []toleration{{Operator: "Exists"}, {Key: cordonTaint, Operator: "Exists", Effect: "NoSchedule"}},
			AutomountServiceAccountToken:  &no,
			EnableServiceLinks:            &no,
			Volumes: append([]volume{
				{Name: "host-firmware", HostPath: &hostPath{Path: "/sys/firmware", Type: "Directory"}},
				// The pod rehearses the install again in this copy of
				// the variables before it writes.
				{Name: "rehearsal", EmptyDir: &emptyDir{Medium: "Memory", SizeLimit: "256Mi"}},
				// The marker file that makes the readiness probe pass.
				{Name: "prepared", EmptyDir: &emptyDir{Medium: "Memory", SizeLimit: "1Mi"}},
				// The pod's own annotations, which include the signal.
				{Name: "signal", DownwardAPI: &downwardAPIVolume{Items: []downwardAPIFile{
					{Path: "annotations", FieldRef: fieldRef{FieldPath: "metadata.annotations"}},
				}}},
			}, firmwareCopyVolumes()...),
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
				VolumeMounts: append([]volumeMount{
					{Name: "host-firmware", MountPath: hostFirmwarePath},
					{Name: "rehearsal", MountPath: rehearsalPath},
					{Name: "prepared", MountPath: preparedPath},
					{Name: "signal", MountPath: signalPath},
				}, firmwareCopyMounts()...),
				ReadinessProbe:           &probe{Exec: &execAction{Command: []string{scanBinary, "prepared"}}, PeriodSeconds: 2},
				Resources:                &resources{Requests: map[string]string{"cpu": "10m", "memory": "64Mi"}},
				TerminationMessagePolicy: "FallbackToLogsOnError",
			}},
		}, c),
	}
	// A capsule apply stages fwupd's updater and the capsule on the ESP
	// of the next boot, so the pod mounts that ESP read-write.
	if r.Capsule != nil {
		p.Spec.Volumes = append(p.Spec.Volumes, volume{Name: nextESPVolume, HostPath: &hostPath{Path: provenESPLink, Type: "Directory"}})
		p.Spec.Containers[0].VolumeMounts = append(p.Spec.Containers[0].VolumeMounts, volumeMount{Name: nextESPVolume, MountPath: nextESPPath})
	}
	if !signal.IsZero() {
		for key, value := range signalAnnotations(signal) {
			p.Metadata.Annotations[key] = value
		}
	}
	return p
}
