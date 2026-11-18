package main

// Each upload pod gets a ResourceClaim with the same name as the pod,
// for one device of the DeviceClass firmware-upload. Its CEL selector
// matches the device's address and device number. The address is the
// USB port path, so two bare chips of the same model on one machine
// get separate claims. The kernel assigns a new device number each time
// the device enumerates, so a claim that is still unallocated when the
// chip is replugged does not match the chip's next appearance.
//
// The upload makes the chip enumerate again with its loaded IDs, so the
// claimed device leaves the bus while the pod runs. machine-operator
// then taints the device liken.sh/unplugged with the effect NoExecute,
// which evicts every pod that does not tolerate it. Nothing recreates
// an upload pod, and an eviction would record a failure for an upload
// that worked. So the claim tolerates the taint.
//
// The claim's owner reference points at its pod, so the garbage
// collector deletes the claim with the pod. The reference needs the
// pod's UID, so the operator creates the pod first, and the pod stays
// unscheduled until its claim exists.

import (
	"fmt"
	"strconv"
)

const (
	claimsAPIVersion = "resource.k8s.io/v1"
	uploadClass      = "firmware-upload"
)

type resourceClaim struct {
	APIVersion string     `json:"apiVersion"`
	Kind       string     `json:"kind"`
	Metadata   objectMeta `json:"metadata"`
	Spec       claimSpec  `json:"spec"`
}

type claimSpec struct {
	Devices struct {
		Requests []deviceRequest `json:"requests"`
	} `json:"devices"`
}

type deviceRequest struct {
	Name    string       `json:"name"`
	Exactly exactRequest `json:"exactly"`
}

type exactRequest struct {
	DeviceClassName string             `json:"deviceClassName"`
	Selectors       []deviceSelector   `json:"selectors"`
	Tolerations     []deviceToleration `json:"tolerations,omitempty"`
}

type deviceToleration struct {
	Key      string `json:"key"`
	Operator string `json:"operator"`
	Effect   string `json:"effect"`
}

// unpluggedTaint is the taint that machine-operator puts on a device
// when it leaves the bus while a prepared claim holds it.
const unpluggedTaint = "liken.sh/unplugged"

type deviceSelector struct {
	CEL struct {
		Expression string `json:"expression"`
	} `json:"cel"`
}

func claimsPathIn(namespace string) string {
	return "/apis/resource.k8s.io/v1/namespaces/" + namespace + "/resourceclaims"
}

// claimSelector returns a CEL expression that matches one appearance of
// a device. strconv.Quote writes the address as a string literal that
// CEL parses to the same value.
func claimSelector(a appearance) string {
	return fmt.Sprintf(`device.attributes["%s"].address == %s && device.attributes["%s"].deviceNumber == %d`,
		driverName, strconv.Quote(a.address), driverName, a.deviceNumber)
}

// buildUploadClaim returns the ResourceClaim for an upload pod. The pod
// must be the object that the API server returned, because the claim's
// owner reference needs its UID.
func buildUploadClaim(upload pod) resourceClaim {
	_, _, a := uploadOf(upload)
	claim := resourceClaim{
		APIVersion: claimsAPIVersion,
		Kind:       "ResourceClaim",
		Metadata: objectMeta{
			Name:      upload.Metadata.Name,
			Namespace: upload.Metadata.Namespace,
			Labels:    map[string]string{"app.kubernetes.io/managed-by": component},
			OwnerReferences: []ownerReference{{
				APIVersion: "v1", Kind: "Pod", Name: upload.Metadata.Name, UID: upload.Metadata.UID,
			}},
		},
	}
	selector := deviceSelector{}
	selector.CEL.Expression = claimSelector(a)
	claim.Spec.Devices.Requests = []deviceRequest{{
		Name: uploadClaim,
		Exactly: exactRequest{
			DeviceClassName: uploadClass,
			Selectors:       []deviceSelector{selector},
			Tolerations:     []deviceToleration{{Key: unpluggedTaint, Operator: "Exists", Effect: "NoExecute"}},
		},
	}}
	return claim
}
