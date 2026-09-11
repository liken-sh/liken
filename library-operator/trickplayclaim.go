package main

// The ResourceClaimTemplate one Library keeps for the render node its trickplay
// Job decodes on: the DRA objects the operator writes, the request it builds
// out of spec.trickplay.render, and the requests it makes for them.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

// The group and version the dynamic resource allocation objects are written
// under, which is the one media-operator writes its claims under.
const deviceAPIVersion = "resource.k8s.io/v1"

// The name of the one device request the trickplay pod holds, which is the name
// its container repeats under resources.claims.
const renderRequestName = "render"

// The template the kubelet mints one ResourceClaim from for each pod that names
// it. The operator writes the spec and reads nothing back.
type ResourceClaimTemplate struct {
	APIVersion string                    `json:"apiVersion,omitempty"`
	Kind       string                    `json:"kind,omitempty"`
	Metadata   ObjectMeta                `json:"metadata"`
	Spec       ResourceClaimTemplateSpec `json:"spec"`
}

// The template wrapper: one ResourceClaim spec, which the API server refuses to
// change after the create.
type ResourceClaimTemplateSpec struct {
	Spec ResourceClaimSpec `json:"spec"`
}

// The devices one claim asks for. The trickplay claim asks for one.
type ResourceClaimSpec struct {
	Devices DeviceClaim `json:"devices"`
}

type DeviceClaim struct {
	Requests []DeviceRequest `json:"requests,omitempty"`
}

// The request name is the role a container refers to, and exactly is the one-of
// that holds a plain request.
type DeviceRequest struct {
	Name    string              `json:"name"`
	Exactly *ExactDeviceRequest `json:"exactly,omitempty"`
}

// ExactCount with a count of one asks for one device of the class, and the
// selector list is omitted where the class alone chooses it.
type ExactDeviceRequest struct {
	DeviceClassName string           `json:"deviceClassName"`
	AllocationMode  string           `json:"allocationMode,omitempty"`
	Count           int              `json:"count,omitempty"`
	Selectors       []DeviceSelector `json:"selectors,omitempty"`
}

// A selector is a CEL expression over device.attributes, the same expression a
// hand-written claim would carry.
type DeviceSelector struct {
	CEL *CELDeviceSelector `json:"cel,omitempty"`
}

type CELDeviceSelector struct {
	Expression string `json:"expression"`
}

// The template of one Library, named from the Job that holds it, so a person
// reading either object finds the other.
func trickplayTemplateName(library string) string {
	return trickplayJobName(library)
}

// The template one Library's render block becomes: one request named render,
// one device of the class it names, and the CEL selector where it states one.
// It is owned by the Library, so the garbage collector takes it.
func buildTrickplayTemplate(library *Library) *ResourceClaimTemplate {
	device := library.Spec.Trickplay.Render
	request := DeviceRequest{
		Name: renderRequestName,
		Exactly: &ExactDeviceRequest{
			DeviceClassName: device.Class,
			AllocationMode:  "ExactCount",
			Count:           1,
		},
	}
	// An empty selector omits the list rather than sending an empty expression,
	// which the API server refuses.
	if device.Selector != "" {
		request.Exactly.Selectors = []DeviceSelector{
			{CEL: &CELDeviceSelector{Expression: device.Selector}},
		}
	}
	return &ResourceClaimTemplate{
		APIVersion: deviceAPIVersion,
		Kind:       "ResourceClaimTemplate",
		Metadata: ObjectMeta{
			Name:            trickplayTemplateName(library.Metadata.Name),
			Namespace:       library.Metadata.Namespace,
			Labels:          libraryLabels(library.Metadata.Name),
			OwnerReferences: []OwnerReference{libraryOwner(library)},
		},
		Spec: ResourceClaimTemplateSpec{
			Spec: ResourceClaimSpec{Devices: DeviceClaim{Requests: []DeviceRequest{request}}},
		},
	}
}

// The template is brought into line on every pass: created where the Library
// states a render block and none stands, written again where the block changed,
// and deleted where the Library states none. A template's spec is immutable, so
// a changed block is a delete and a create and never a patch.
func (o *operator) standTrickplayTemplate(ctx context.Context, library *Library) error {
	namespace := library.Metadata.Namespace
	name := trickplayTemplateName(library.Metadata.Name)

	live, err := GetResourceClaimTemplate(ctx, o.client, namespace, name)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	stands := err == nil

	if library.Spec.Trickplay.Render == nil {
		if !stands {
			return nil
		}
		return DeleteResourceClaimTemplate(ctx, o.client, namespace, name)
	}

	desired := buildTrickplayTemplate(library)
	if stands {
		same, err := sameTemplateSpec(live.Spec, desired.Spec)
		if err != nil || same {
			return err
		}
		if err := DeleteResourceClaimTemplate(ctx, o.client, namespace, name); err != nil {
			return err
		}
	}
	_, err = CreateResourceClaimTemplate(ctx, o.client, desired)
	if errors.Is(err, ErrConflict) {
		// Another pass, or another copy of this operator, created the template
		// first, which is success.
		return nil
	}
	return err
}

// Two template specs compared by their marshaled form, because the client drops
// every field it does not model when it reads the stored template, so a field
// the API server added does not read as a change.
func sameTemplateSpec(current, desired ResourceClaimTemplateSpec) (bool, error) {
	was, err := json.Marshal(current)
	if err != nil {
		return false, err
	}
	wants, err := json.Marshal(desired)
	if err != nil {
		return false, err
	}
	return string(was) == string(wants), nil
}

// The templates of one namespace.
func claimTemplatesPath(namespace string) string {
	return "/apis/" + deviceAPIVersion + "/namespaces/" + namespace + "/resourceclaimtemplates"
}

func GetResourceClaimTemplate(ctx context.Context, c *Client, namespace, name string) (*ResourceClaimTemplate, error) {
	held := &ResourceClaimTemplate{}
	if err := c.RequestJSON(ctx, http.MethodGet, claimTemplatesPath(namespace)+"/"+name, nil, held); err != nil {
		return nil, err
	}
	return held, nil
}

func CreateResourceClaimTemplate(ctx context.Context, c *Client, template *ResourceClaimTemplate) (*ResourceClaimTemplate, error) {
	body, err := json.Marshal(template)
	if err != nil {
		return nil, err
	}
	created := &ResourceClaimTemplate{}
	if err := c.RequestJSON(ctx, http.MethodPost, claimTemplatesPath(template.Metadata.Namespace), body, created); err != nil {
		return nil, err
	}
	return created, nil
}

// An already-absent template is success, the rule every other delete here
// follows.
func DeleteResourceClaimTemplate(ctx context.Context, c *Client, namespace, name string) error {
	err := c.RequestJSON(ctx, http.MethodDelete, claimTemplatesPath(namespace)+"/"+name, nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}
