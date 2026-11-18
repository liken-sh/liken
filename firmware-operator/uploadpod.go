package main

// An upload runs the Firmware's image and command in a pod on the
// device's node. The pod holds a claim on the device, and the command
// finds the device's usbfs path in LIKEN_DEVICE. The operator itself
// opens no device.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// The label selects the upload pods that the operator watches. The
// annotations record the pod's Firmware and device appearance, so an
// operator that restarts during an upload reads them back and follows
// the pod. They are annotations because a Firmware's name can be longer
// than the 63 characters that a label value allows.
const (
	uploadLabel      = "firmware.liken.sh/upload"
	uploadLabelValue = "true"
	firmwareKey      = "firmware.liken.sh/firmware"
	generationKey    = "firmware.liken.sh/generation"
	machineKey       = "firmware.liken.sh/machine"
	addressKey       = "firmware.liken.sh/address"
	deviceNumberKey  = "firmware.liken.sh/device-number"
	deviceKey        = "firmware.liken.sh/device"
	uploadContainer  = "upload"
	deviceVariable   = "LIKEN_DEVICE"
	uploadClaim      = "device"
)

// uploadPodName returns the same name for the same Firmware generation
// and appearance, so when a pass runs before the watch reports the first
// pod, its second create fails with 409 Conflict and starts no second
// upload. The name starts with the Firmware's name, for a person who
// reads kubectl get pods, and ends with a hash of all the inputs. The
// claim has the same name, and the name fits in a 63-character DNS
// label.
func uploadPodName(rule string, generation int64, a appearance) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s\x00%s\x00%d", rule, generation, a.machine, a.address, a.deviceNumber)))
	suffix := "-" + hex.EncodeToString(sum[:6])
	prefix := rule
	if len(prefix) > 63-len(suffix) {
		prefix = prefix[:63-len(suffix)]
	}
	return strings.TrimRight(prefix, "-.") + suffix
}

// uploadOf returns the rule, the generation, and the appearance that
// an upload pod's annotations record.
func uploadOf(p pod) (string, int64, appearance) {
	notes := p.Metadata.Annotations
	generation, _ := strconv.ParseInt(notes[generationKey], 10, 64)
	number, _ := strconv.ParseInt(notes[deviceNumberKey], 10, 64)
	return notes[firmwareKey], generation, appearance{machine: notes[machineKey], address: notes[addressKey], deviceNumber: number}
}

func buildUploadPod(f *firmware, d device, namespace string) pod {
	no, yes := false, true
	user, deadline := int64(1000), int64(uploadDeadline.Seconds())
	name := uploadPodName(f.Metadata.Name, f.Metadata.Generation, d.appearance)
	return pod{
		APIVersion: "v1",
		Kind:       "Pod",
		Metadata: objectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				uploadLabel:                    uploadLabelValue,
				"app.kubernetes.io/managed-by": component,
			},
			Annotations: map[string]string{
				firmwareKey:     f.Metadata.Name,
				generationKey:   strconv.FormatInt(f.Metadata.Generation, 10),
				machineKey:      d.machine,
				addressKey:      d.address,
				deviceNumberKey: strconv.FormatInt(d.deviceNumber, 10),
				deviceKey:       d.name,
			},
			// Deleting the Firmware garbage-collects its pods. A
			// cluster-scoped owner of a namespaced object is allowed.
			OwnerReferences: []ownerReference{{
				APIVersion: firmwareAPIVersion, Kind: firmwareKind, Name: f.Metadata.Name, UID: f.Metadata.UID,
			}},
		},
		Spec: podSpec{
			// Each appearance gets one attempt, so the pod never
			// restarts. activeDeadlineSeconds stops a loader that hangs,
			// counted from the pod's start. follow.go times the pod
			// from its creation.
			RestartPolicy:         "Never",
			ActiveDeadlineSeconds: &deadline,
			NodeSelector:          map[string]string{"kubernetes.io/hostname": d.machine},
			// The loader needs nothing from the API server or from a
			// Service.
			AutomountServiceAccountToken: &no,
			EnableServiceLinks:           &no,
			// The loader sends USB control transfers on the device's
			// usbfs node, and liken gives the pod's user access to a
			// claimed node. So the pod runs as non-root with no
			// capabilities, at Pod Security's restricted level.
			SecurityContext: &podSecurity{
				RunAsNonRoot:   &yes,
				RunAsUser:      &user,
				RunAsGroup:     &user,
				SeccompProfile: &seccompProfile{Type: "RuntimeDefault"},
			},
			ResourceClaims: []podResourceClaim{{Name: uploadClaim, ResourceClaimName: name}},
			Containers: []container{{
				Name:    uploadContainer,
				Image:   f.Spec.Upload.Image,
				Command: f.Spec.Upload.Command,
				Args:    f.Spec.Upload.Args,
				// The kubelet expands $(LIKEN_DEVICE) in the command and
				// the arguments.
				Env: []envVar{{Name: deviceVariable, Value: d.usbfsPath()}},
				SecurityContext: &securityContext{
					AllowPrivilegeEscalation: &no,
					ReadOnlyRootFilesystem:   &yes,
					Capabilities:             &capabilities{Drop: []string{"ALL"}},
				},
				Resources: &resources{
					Requests: map[string]string{"cpu": "10m", "memory": "16Mi"},
					Claims:   []containerClaim{{Name: uploadClaim}},
				},
				// When the loader fails, the kubelet copies the end of
				// its log into the container's terminated state, so the
				// operator reads the reason from the pod without
				// requesting the log.
				TerminationMessagePolicy: "FallbackToLogsOnError",
			}},
		},
	}
}
