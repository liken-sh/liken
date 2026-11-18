package main

// A scan pod runs fwupd on one machine against one FirmwareCatalog. It
// reads the machine's firmware devices and the releases that the
// catalog offers for them, and writes the result to its log. It
// changes nothing on the machine.
//
// The pod is privileged, because fwupd reads SMBIOS tables, efivarfs,
// sysfs, and device nodes. It claims no device, because fwupd reads
// many devices at once. It runs in the operator's namespace,
// liken-system, which has no Pod Security label, so the API server
// admits a privileged pod there. A cluster that labels liken-system
// with a Pod Security level below privileged refuses scan pods.

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// The operator's watch and the NetworkPolicy in deploy/scan.yaml
// select scan pods by scanLabel. The annotations record the attempt, so
// an operator that restarts during a scan can read it back from the pod
// and keep following it.
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

// The environment variables of the scan program. The operator passes
// the catalog file and its SHA-256 from the jcat that the operator
// read, so the scan uses that file. When the jcat that the scan reads
// lists a different file, the scan reports CatalogMoved.
const (
	catalogNameVariable   = "CATALOG_NAME"
	catalogURLVariable    = "CATALOG_URL"
	catalogFileVariable   = "CATALOG_FILE"
	catalogSHA256Variable = "CATALOG_SHA256"
)

// scanDeadline bounds a scan pod and a rehearsal pod from their
// creation, and an apply pod from its signal to write. A scan pod's
// activeDeadlineSeconds has the same value but counts from when the pod
// starts, and a pod whose image never pulls never starts, so the
// operator checks this deadline too.
const scanDeadline = 10 * time.Minute

// scanAttempt is one scan of one machine against one FirmwareCatalog,
// identified by the boot, the device set, the catalog file, and the
// scan image. An attempt runs once.
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

// scanPodName returns the pod name of an attempt: the catalog and the
// machine, so that kubectl get pods is readable, and a hash of the
// whole attempt. A second create of the same attempt fails with 409
// Conflict, so neither a pass that runs before the watch reports the
// first pod nor an operator that restarted can start a second pod.
func scanPodName(a scanAttempt) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{a.catalog, a.machine, a.boot, a.devices, a.file, a.image}, "\x00")))
	suffix := "-" + hex.EncodeToString(sum[:6])
	prefix := a.catalog + "-" + a.machine
	if len(prefix) > 63-len(suffix) {
		prefix = prefix[:63-len(suffix)]
	}
	return strings.TrimRight(prefix, "-.") + suffix
}

// scanOf returns the attempt that a scan pod's annotations record.
func scanOf(p pod) scanAttempt {
	notes := p.Metadata.Annotations
	return scanAttempt{catalog: notes[catalogKey], machine: notes[machineKey], boot: notes[bootKey], devices: notes[devicesKey], file: notes[fileKey], image: notes[imageKey]}
}

// buildScanPod returns the pod for one attempt. sum is the SHA-256 of
// the attempt's catalog file.
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
			// Deleting the FirmwareCatalog deletes its scan pods. A
			// cluster-scoped owner can own a namespaced object.
			OwnerReferences: []ownerReference{{
				APIVersion: firmwareAPIVersion, Kind: catalogKind, Name: c.Metadata.Name, UID: c.Metadata.UID,
			}},
		},
		Spec: withTrust(podSpec{
			// An attempt runs once, so the pod never restarts.
			RestartPolicy:         "Never",
			ActiveDeadlineSeconds: &deadline,
			// The scan keeps no state, so it needs no time to shut down.
			TerminationGracePeriodSeconds: &grace,
			NodeName:                      a.machine,
			// The scheduler ignores taints for a pod with spec.nodeName,
			// but the kubelet evicts a pod that does not tolerate a
			// NoExecute taint. A machine with such a taint still has
			// firmware to scan, so the pod tolerates every taint.
			Tolerations: []toleration{{Operator: "Exists"}},
			// The scan does not use the API server or any Service.
			AutomountServiceAccountToken: &no,
			EnableServiceLinks:           &no,
			// fwupd reads a copy of the machine's firmware tables, for
			// the reason given on copyVariablesVolume. The pod mounts
			// the host's tables read-only and keeps the copy, with its
			// variable store, in memory.
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
				// fwupd parses the whole catalog, and a limit set too low
				// would kill every scan, so the pod has a small request
				// and no memory limit.
				Resources: &resources{Requests: map[string]string{"cpu": "10m", "memory": "64Mi"}},
				// A scan that crashes before it writes its result line
				// leaves the end of its log in the container's terminated
				// state, where the operator can read it.
				TerminationMessagePolicy: "FallbackToLogsOnError",
			}},
		}, c),
	}
}
