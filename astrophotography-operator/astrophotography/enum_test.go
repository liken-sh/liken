package astrophotography

// A field with a fixed set of values has a named string type in Go,
// with one constant for each value, and an enum in the CRD. The API
// server rejects a value outside the enum, so the enum must list the
// same values as the constants.

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

func strs[T ~string](values ...T) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = string(v)
	}
	return out
}

var conditionStatuses = strs(observatory.ConditionTrue, observatory.ConditionFalse, observatory.ConditionUnknown)

func TestEachCRDEnumListsTheGoConstants(t *testing.T) {
	cases := []struct {
		kind Kind
		path string
		want []string
	}{
		{TargetKind, "spec.archive.promotion", strs(PromotionContinuous, PromotionAfterReservation)},
		{TargetKind, "status.conditions[].status", conditionStatuses},
		{SolverKind, "status.conditions[].status", conditionStatuses},
	}
	for _, c := range cases {
		t.Run(c.kind.Name+"."+c.path, func(t *testing.T) {
			field := resolve(schemaOf(t, c.kind), strings.Split(c.path, "."))
			if got := enumOf(t, field); !slices.Equal(got, c.want) {
				t.Errorf("enum = %v, want %v", got, c.want)
			}
		})
	}
}

func enumOf(t *testing.T, field *apiextensionsv1.JSONSchemaProps) []string {
	t.Helper()
	var values []string
	for _, raw := range field.Enum {
		var value string
		if err := json.Unmarshal(raw.Raw, &value); err != nil {
			t.Fatalf("reading an enum value of the CRD: %v", err)
		}
		values = append(values, value)
	}
	return values
}
