package observatory

// The 16 CRDs with procedures repeat one schema for activation,
// deactivation, triggers, and status.procedures, because a CRD cannot
// refer to another. Only the fields of an action that some kinds lack
// differ by kind: state with its enum, cool, and warm, with the item's
// description and its rule that the action names one of them. This
// test holds every other part of each copy equal to the Mount's,
// descriptions and rules included, so an edit to one copy fails until
// every copy has it.

import (
	"reflect"
	"slices"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

// procedureKinds are the kinds whose spec embeds Procedures.
var procedureKinds = append(slices.Clone(DeviceKinds), TelescopeKind, ObservatoryKind)

// ownActionFields are the fields of an action that only some kinds
// have.
var ownActionFields = []string{"state", "cool", "warm"}

// sharedAction answers the schema of one action without the fields and
// the rule that differ by kind.
func sharedAction(items *apiextensionsv1.JSONSchemaPropsOrArray) apiextensionsv1.JSONSchemaProps {
	action := *items.Schema.DeepCopy()
	action.Description, action.XValidations = "", nil
	for _, name := range ownActionFields {
		delete(action.Properties, name)
	}
	return action
}

// sharedProcedureParts answers each part of a CRD's procedure schema
// that every kind shares, by its place in the schema. A list of
// actions appears with its own description and limits, and the action
// inside it appears again as its own part.
func sharedProcedureParts(schema *apiextensionsv1.JSONSchemaProps) map[string]any {
	spec := schema.Properties["spec"].Properties
	list := func(field apiextensionsv1.JSONSchemaProps) apiextensionsv1.JSONSchemaProps {
		out := *field.DeepCopy()
		out.Items = nil
		return out
	}
	trigger := *spec["triggers"].Items.Schema.DeepCopy()
	run := trigger.Properties["run"]
	trigger.Properties["run"] = list(run)
	return map[string]any{
		"spec.activation":       list(spec["activation"]),
		"spec.activation[]":     sharedAction(spec["activation"].Items),
		"spec.deactivation":     list(spec["deactivation"]),
		"spec.deactivation[]":   sharedAction(spec["deactivation"].Items),
		"spec.triggers":         list(spec["triggers"]),
		"spec.triggers[]":       trigger,
		"spec.triggers[].run[]": sharedAction(run.Items),
		"status.procedures":     schema.Properties["status"].Properties["procedures"],
	}
}

func TestTheCRDsShareTheirProcedureSchema(t *testing.T) {
	model := sharedProcedureParts(schemaOf(t, MountKind))
	for _, kind := range procedureKinds {
		parts := sharedProcedureParts(schemaOf(t, kind))
		for _, part := range sortedKeys(model) {
			t.Run(kind.Name+"/"+part, func(t *testing.T) {
				if !reflect.DeepEqual(parts[part], model[part]) {
					t.Errorf("%s differs from the Mount's", part)
				}
			})
		}
	}
}

// One CRD repeats the action's shared fields in activation,
// deactivation, and each trigger's run.
func TestEachCRDStatesOneActionInEachList(t *testing.T) {
	for _, kind := range procedureKinds {
		parts := sharedProcedureParts(schemaOf(t, kind))
		for _, place := range []string{"spec.deactivation[]", "spec.triggers[].run[]"} {
			t.Run(kind.Name+"/"+place, func(t *testing.T) {
				if !reflect.DeepEqual(parts[place], parts["spec.activation[]"]) {
					t.Errorf("the action in %s differs from the action in spec.activation[]", place)
				}
			})
		}
	}
}
