package main

// An apply is one bare pod on a machine that writes an approved Secure
// Boot database update to the machine's real variable (scan/apply.go).
// It runs the scan image as `scan apply`. The drain of the machine's
// turn can evict the cluster's DNS, so the pod starts when the
// machine's rehearsal passes, while the cluster is whole, and does all
// its network work then: it loads the catalog, downloads the cabinet,
// and rehearses the install again. When that prepare is done, its
// readiness probe turns the pod Ready, and only then does the operator
// ask for the turn, because on a machine with little to drain the turn
// can hold within a second. The pod waits on the machine. When
// machine-operator holds the turn after the drain (disruption.go), the
// operator sends the signal: it sets the pod's annotation
// firmware.liken.sh/write to go. The pod reads its own annotations
// through a downward API volume, so it needs no credentials, and only
// then writes, with no network.
//
// The pod stays on the machine through the drain. liken's drain skips
// a pod that tolerates the cordon, node.kubernetes.io/unschedulable, by
// its key, because the cordon does not keep such a pod off the machine,
// and an eviction would move nothing. The pod has no deadline, because
// the turn can come much later; the operator deletes the pod when its
// record leaves.
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
	"time"
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

// The annotations that carry the operator's signal to write, the value
// of the first, and where the pod reads them. write-at is the time the
// operator sent the signal, from which the apply's deadline counts.
const (
	writeKey   = "firmware.liken.sh/write"
	writeAtKey = "firmware.liken.sh/write-at"
	writeGo    = "go"
	signalPath = "/run/signal"
)

// The readiness probe of an apply pod runs `scan prepared`, which
// reads the marker that `scan apply` creates in preparedPath when its
// prepare passed. scanBinary is the program's path in the scan image
// (scan/Dockerfile), because a probe names its whole command.
const (
	preparedPath = "/run/apply"
	scanBinary   = "/usr/local/bin/scan"
)

// cordonTaint is the taint that a cordon sets on a node.
const cordonTaint = "node.kubernetes.io/unschedulable"

// signalOf answers when the operator sent an apply pod the signal to
// write, or the zero time when it has not. A signal with no time, which
// only a person writes, counts from the pod's creation.
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

// signalAnnotations answers the annotations of the signal sent at a
// time.
func signalAnnotations(sent time.Time) map[string]string {
	return map[string]string{writeKey: writeGo, writeAtKey: stamp(sent)}
}

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
// rehearsal changes nothing that is written. A pod built with a signal
// time carries the signal, so it writes as soon as it has prepared.
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
			// A deleted Firmware deletes its apply pods.
			OwnerReferences: []ownerReference{{
				APIVersion: firmwareAPIVersion, Kind: firmwareKind, Name: f.Metadata.Name, UID: f.Metadata.UID,
			}},
		},
		Spec: withTrust(podSpec{
			// The pod runs once. It tolerates every taint, so a taint
			// on the machine does not stop it, and the cordon by its
			// key, so the drain skips it.
			RestartPolicy:                 "Never",
			TerminationGracePeriodSeconds: &grace,
			NodeName:                      a.machine,
			Tolerations:                   []toleration{{Operator: "Exists"}, {Key: cordonTaint, Operator: "Exists", Effect: "NoSchedule"}},
			AutomountServiceAccountToken:  &no,
			EnableServiceLinks:            &no,
			Volumes: []volume{
				{Name: "host-firmware", HostPath: &hostPath{Path: "/sys/firmware", Type: "Directory"}},
				// The copy of the rehearsal before the write, as in the
				// rehearsal pod.
				{Name: "rehearsal", EmptyDir: &emptyDir{Medium: "Memory", SizeLimit: "64Mi"}},
				// The marker of the finished prepare.
				{Name: "prepared", EmptyDir: &emptyDir{Medium: "Memory", SizeLimit: "1Mi"}},
				// The pod's own annotations, which carry the signal.
				{Name: "signal", DownwardAPI: &downwardAPIVolume{Items: []downwardAPIFile{
					{Path: "annotations", FieldRef: fieldRef{FieldPath: "metadata.annotations"}},
				}}},
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
					{Name: "prepared", MountPath: preparedPath},
					{Name: "signal", MountPath: signalPath},
				},
				// The probe is how the operator learns that the prepare
				// is done: the kubelet sets Ready, and the watch of the
				// pods delivers it, so the operator reads no log.
				ReadinessProbe:           &probe{Exec: &execAction{Command: []string{scanBinary, "prepared"}}, PeriodSeconds: 2},
				Resources:                &resources{Requests: map[string]string{"cpu": "10m", "memory": "64Mi"}},
				TerminationMessagePolicy: "FallbackToLogsOnError",
			}},
		}, c),
	}
	if !signal.IsZero() {
		for key, value := range signalAnnotations(signal) {
			p.Metadata.Annotations[key] = value
		}
	}
	return p
}
