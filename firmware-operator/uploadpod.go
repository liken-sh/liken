package main

// An upload is one short pod on the device's node, with a claim on
// that one device. The pod runs the vendor's image with the rule's
// command, and the command finds the device's node in LIKEN_DEVICE.
// The operator itself touches no device.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// The labels and annotations of an upload pod. The label selects the
// pods the operator watches. The annotations record the upload's rule
// and appearance, so an operator that restarts during an upload reads
// them from the pod and follows it. A label value holds at most 63
// characters, and a rule's name can be longer, so the record is in
// annotations.
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

// uploadPodName answers the one name of the upload for an appearance
// and a generation of a rule. One name means a second create answers
// 409, so a pass that runs before the watch shows the first pod starts
// no second one. The name starts with the rule's name, for a person
// who reads kubectl get pods, and ends in a hash of the whole
// identity. The name is also the claim's name, and both stay within
// the 63 characters of a DNS label.
func uploadPodName(rule string, generation int64, a appearance) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s\x00%s\x00%d", rule, generation, a.machine, a.address, a.deviceNumber)))
	suffix := "-" + hex.EncodeToString(sum[:6])
	prefix := rule
	if len(prefix) > 63-len(suffix) {
		prefix = prefix[:63-len(suffix)]
	}
	return strings.TrimRight(prefix, "-.") + suffix
}

// uploadOf answers the rule, the generation, and the appearance that an
// upload pod's annotations record.
func uploadOf(p pod) (string, int64, appearance) {
	notes := p.Metadata.Annotations
	generation, _ := strconv.ParseInt(notes[generationKey], 10, 64)
	number, _ := strconv.ParseInt(notes[deviceNumberKey], 10, 64)
	return notes[firmwareKey], generation, appearance{machine: notes[machineKey], address: notes[addressKey], deviceNumber: number}
}

// buildUploadPod answers the pod that uploads a rule's firmware into
// one device.
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
			// A deleted rule deletes its pods. A cluster-scoped owner
			// may own a namespaced object.
			OwnerReferences: []ownerReference{{
				APIVersion: firmwareAPIVersion, Kind: firmwareKind, Name: f.Metadata.Name, UID: f.Metadata.UID,
			}},
		},
		Spec: podSpec{
			// Each appearance gets one attempt, so the pod runs once.
			// activeDeadlineSeconds stops a loader that hangs, and
			// counts from the pod's start; the operator also measures
			// five minutes from the pod's creation (follow.go).
			RestartPolicy:         "Never",
			ActiveDeadlineSeconds: &deadline,
			NodeSelector:          map[string]string{"kubernetes.io/hostname": d.machine},
			// The loader writes to a USB device and needs nothing from
			// the API server or from a Service.
			AutomountServiceAccountToken: &no,
			EnableServiceLinks:           &no,
			// A load is a series of USB control transfers on the
			// device's usbfs node, and liken gives a claimed node to
			// the pod's user. So the pod meets Pod Security's
			// restricted level.
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
				// the arguments from this variable.
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
				// A loader that fails writes why to its log. With this
				// policy the kubelet copies the end of the log into the
				// container's terminated state, so the operator reads
				// the reason from the pod with no request for the log.
				TerminationMessagePolicy: "FallbackToLogsOnError",
			}},
		},
	}
}
