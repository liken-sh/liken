package main

// A scan is one bare pod on a machine that runs fwupd against one
// FirmwareCatalog. It reads the machine's firmware devices and the releases the
// catalog offers for them, and writes the answer to its log. The pod is
// privileged, because fwupd reads SMBIOS tables, efivarfs, sysfs, and
// device nodes, and it holds no claim, because fwupd reads many
// devices. It only reads.
//
// The pod runs in the operator's own namespace, liken-system, which
// carries no Pod Security label, so the API server admits a privileged
// pod there. A cluster that labels liken-system with a Pod Security
// level below privileged refuses the scan pods.

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// The labels and annotations of a scan pod. The label selects the pods
// the operator watches, and the NetworkPolicy in deploy/scan.yaml. The
// annotations record the attempt, so an operator that restarts during
// a scan reads them from the pod and follows it.
const (
	scanLabel      = "firmware.liken.sh/scan"
	scanLabelValue = "true"
	catalogKey     = "firmware.liken.sh/catalog"
	bootKey        = "firmware.liken.sh/boot"
	devicesKey     = "firmware.liken.sh/devices"
	fileKey        = "firmware.liken.sh/file"
	imageKey       = "firmware.liken.sh/scan-image"
	scanContainer  = "scan"
)

// The environment of the scan program. The operator names the file and
// its SHA-256 that it read from the jcat, so every scan of one attempt
// matches the same catalog, and a pod that finds another file answers
// CatalogMoved.
const (
	catalogNameVariable   = "CATALOG_NAME"
	catalogURLVariable    = "CATALOG_URL"
	catalogFileVariable   = "CATALOG_FILE"
	catalogSHA256Variable = "CATALOG_SHA256"
)

// scanDeadline bounds a scan, from its pod's creation. The pod's own
// activeDeadlineSeconds has the same value, but counts only from the
// start of a pod that runs, and a pod whose image never pulls never
// starts.
const scanDeadline = 10 * time.Minute

// scanAttempt is one scan of one machine against one FirmwareCatalog: the boot,
// the device set, the catalog file, and the scan image it ran with.
// Each attempt runs once.
type scanAttempt struct {
	catalog string
	machine string
	boot    string
	devices string
	file    string
	image   string
}

func (a scanAttempt) is(r machineRecord) bool {
	return r.Machine == a.machine && r.Boot == a.boot && r.Devices == a.devices && r.File == a.file && r.Image == a.image
}

func attemptOf(catalog string, r machineRecord) scanAttempt {
	return scanAttempt{catalog: catalog, machine: r.Machine, boot: r.Boot, devices: r.Devices, file: r.File, image: r.Image}
}

// scanPodName answers the one name of an attempt. One name means a
// second create answers 409, so a pass that runs before the watch shows
// the first pod starts no second one, and neither does an operator that
// restarted. The name starts with the catalog and the machine, for a
// person who reads kubectl get pods, and ends in a hash of the whole
// attempt.
func scanPodName(a scanAttempt) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{a.catalog, a.machine, a.boot, a.devices, a.file, a.image}, "\x00")))
	suffix := "-" + hex.EncodeToString(sum[:6])
	prefix := a.catalog + "-" + a.machine
	if len(prefix) > 63-len(suffix) {
		prefix = prefix[:63-len(suffix)]
	}
	return strings.TrimRight(prefix, "-.") + suffix
}

// scanOf answers the attempt that a scan pod's annotations record.
func scanOf(p pod) scanAttempt {
	notes := p.Metadata.Annotations
	return scanAttempt{catalog: notes[catalogKey], machine: notes[machineKey], boot: notes[bootKey], devices: notes[devicesKey], file: notes[fileKey], image: notes[imageKey]}
}

// buildScanPod answers the pod of one attempt, which runs the attempt's
// scan image, with the SHA-256 of the attempt's file.
func buildScanPod(c *catalog, a scanAttempt, sum, namespace string) pod {
	no, yes := false, true
	deadline, grace := int64(scanDeadline.Seconds()), int64(5)
	return pod{
		APIVersion: "v1",
		Kind:       "Pod",
		Metadata: objectMeta{
			Name:      scanPodName(a),
			Namespace: namespace,
			Labels: map[string]string{
				scanLabel:                      scanLabelValue,
				"app.kubernetes.io/managed-by": component,
			},
			Annotations: map[string]string{
				catalogKey: a.catalog,
				machineKey: a.machine,
				bootKey:    a.boot,
				devicesKey: a.devices,
				fileKey:    a.file,
				imageKey:   a.image,
			},
			// A deleted FirmwareCatalog deletes its scan pods. A cluster-scoped
			// owner may own a namespaced object.
			OwnerReferences: []ownerReference{{
				APIVersion: firmwareAPIVersion, Kind: catalogKind, Name: c.Metadata.Name, UID: c.Metadata.UID,
			}},
		},
		Spec: withTrust(podSpec{
			// Each attempt runs once, so the pod runs once.
			// activeDeadlineSeconds stops a scan that hangs, and counts
			// from the pod's start; the operator also measures the same
			// time from the pod's creation (scans.go).
			RestartPolicy:         "Never",
			ActiveDeadlineSeconds: &deadline,
			// The scan holds no state, so a stop needs no time to save
			// any.
			TerminationGracePeriodSeconds: &grace,
			// The scan reads the devices of one machine, so the pod names
			// the node, and the scheduler does not place it.
			NodeName: a.machine,
			// The scheduler checks no taint on a pod that names its node,
			// but the kubelet evicts a pod that does not tolerate a
			// NoExecute taint. A machine with such a taint still keeps
			// firmware, so the pod tolerates every taint.
			Tolerations: []toleration{{Operator: "Exists"}},
			// The scan reads the catalog over HTTPS and its devices from
			// the host, and needs nothing from the API server or from a
			// Service.
			AutomountServiceAccountToken: &no,
			EnableServiceLinks:           &no,
			// fwupd reads a copy of the machine's firmware tables, for
			// the reason that copyVariablesVolume gives, so the pod
			// mounts the tables read-only and the copy's variable store
			// in memory.
			Volumes: append([]volume{
				{Name: "host-firmware", HostPath: &hostPath{Path: "/sys/firmware", Type: "Directory"}},
			}, firmwareCopyVolumes()...),
			Containers: []container{{
				Name:  scanContainer,
				Image: a.image,
				Env: []envVar{
					{Name: catalogNameVariable, Value: c.Metadata.Name},
					{Name: catalogURLVariable, Value: c.Spec.URL},
					{Name: catalogFileVariable, Value: a.file},
					{Name: catalogSHA256Variable, Value: sum},
				},
				SecurityContext: &securityContext{Privileged: &yes},
				VolumeMounts: append([]volumeMount{
					{Name: "host-firmware", MountPath: hostFirmwarePath, ReadOnly: true, RecursiveReadOnly: "Enabled"},
				}, firmwareCopyMounts()...),
				// The scan's memory has not been measured, so the request
				// is small and there is no limit: fwupd parses the whole
				// catalog, and a limit set too low would kill every scan.
				Resources: &resources{Requests: map[string]string{"cpu": "10m", "memory": "64Mi"}},
				// A scan that crashes before it writes its result line
				// leaves the end of its log in the container's terminated
				// state too.
				TerminationMessagePolicy: "FallbackToLogsOnError",
			}},
		}, c),
	}
}
