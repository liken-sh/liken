package main

// Each upload pod names one ResourceClaim, with the pod's own name. The
// claim asks for one device of the DeviceClass firmware-upload, and its
// selector names the device's address and device number. The address
// is the port path, unique on the machine even when two bare chips of
// one model are connected. The device number limits the claim to this
// appearance, so a claim that the scheduler did not allocate before a
// replug cannot take the next appearance.
//
// The claim's owner is its pod, so the garbage collector deletes the
// claim with the pod. That needs the pod's UID, so the operator creates
// the pod first. The pod stays unscheduled until its claim exists.

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
	DeviceClassName string           `json:"deviceClassName"`
	Selectors       []deviceSelector `json:"selectors"`
}

type deviceSelector struct {
	CEL struct {
		Expression string `json:"expression"`
	} `json:"cel"`
}

func claimsPathIn(namespace string) string {
	return "/apis/resource.k8s.io/v1/namespaces/" + namespace + "/resourceclaims"
}

// claimSelector selects one appearance of a device. strconv.Quote
// writes the address as a string literal that CEL reads the same way.
func claimSelector(a appearance) string {
	return fmt.Sprintf(`device.attributes["%s"].address == %s && device.attributes["%s"].deviceNumber == %d`,
		driverName, strconv.Quote(a.address), driverName, a.deviceNumber)
}

// buildUploadClaim answers the claim of an upload pod that the API
// server holds, with its UID.
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
		Name:    uploadClaim,
		Exactly: exactRequest{DeviceClassName: uploadClass, Selectors: []deviceSelector{selector}},
	}}
	return claim
}
