package informer

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"
)

// An object that does not convert to the operator's struct returns an
// error that names the object. A DeletedFinalStateUnknown tombstone,
// which the informer gives a handler for an object that was deleted
// while the watch was disconnected, converts as the object inside it. A
// tombstone with no object returns an error that names its key.
func TestAnObjectThatDoesNotConvertReturnsAnErrorThatNamesIt(t *testing.T) {
	good := newThing("a", "7", 2)
	good.Metadata.Namespace = "den"
	mistyped := asObject(t, good)
	if err := unstructured.SetNestedField(mistyped.Object, "two", "spec", "size"); err != nil {
		t.Fatalf("setting spec.size to a string: %v", err)
	}
	cases := []struct {
		name    string
		object  any
		wantErr string
	}{
		{name: "an object", object: asObject(t, good)},
		{name: "a tombstone", object: cache.DeletedFinalStateUnknown{Key: "den/a", Obj: asObject(t, good)}},
		{name: "an empty tombstone", object: cache.DeletedFinalStateUnknown{Key: "den/a"}, wantErr: "tombstone for den/a holds no copy"},
		{name: "a field of the wrong type", object: mistyped, wantErr: "Thing den/a does not convert"},
		{name: "something that is not an object", object: "a", wantErr: "not an object"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Convert[thing](c.object)
			if c.wantErr == "" && (err != nil || got.Metadata.Generation != 2) {
				t.Fatalf("Convert = %+v, %v; want generation 2 and no error", got.Metadata, err)
			}
			if c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)) {
				t.Fatalf("Convert error = %v, want one that contains %q", err, c.wantErr)
			}
		})
	}
}

// targetPort holds a Service's targetPort, which is a port name or a
// port number, as text. Only its UnmarshalJSON accepts a number.
type targetPort string

func (p *targetPort) UnmarshalJSON(data []byte) error {
	var name string
	if json.Unmarshal(data, &name) == nil {
		*p = targetPort(name)
		return nil
	}
	var number int64
	if err := json.Unmarshal(data, &number); err != nil {
		return err
	}
	*p = targetPort(strconv.FormatInt(number, 10))
	return nil
}

type service struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		Ports []struct {
			TargetPort targetPort `json:"targetPort"`
		} `json:"ports"`
	} `json:"spec"`
}

// When the unstructured converter refuses a field but the struct's
// UnmarshalJSON accepts its JSON, Convert decodes the JSON instead.
func TestConvertFallsBackToJSONWhenTheConverterRefuses(t *testing.T) {
	cases := []struct {
		name       string
		targetPort any
		want       targetPort
	}{
		{"a port name", "http", "http"},
		{"a port number", int64(8080), "8080"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			object := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "v1", "kind": "Service",
				"metadata": map[string]any{"name": "catalog"},
				"spec":     map[string]any{"ports": []any{map[string]any{"targetPort": c.targetPort}}},
			}}

			got, err := Convert[service](object)

			if err != nil || len(got.Spec.Ports) != 1 || got.Spec.Ports[0].TargetPort != c.want {
				t.Errorf("Convert = %+v, %v; want the port %+v", got.Spec, err, c.want)
			}
		})
	}
}
