package observatory

// The CRDs are written by hand, so this test holds each one equal to
// its Go type. A field that one side has and the other does not, or a
// field with another type, fails here. Without it, the API server
// prunes a status field that the CRD lacks, and the operator's next
// write of that field does nothing.

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

// kindTypes pairs each kind with its Go type.
var kindTypes = map[string]any{
	"Observatory": Observatory{}, "Telescope": Telescope{},
	"OpticalTube": OpticalTube{}, "OpticalTrain": OpticalTrain{},
	"Mount": Mount{}, "Camera": Camera{}, "FilterWheel": FilterWheel{},
	"Focuser": Focuser{}, "Rotator": Rotator{}, "DustCap": DustCap{},
	"FlatPanel": FlatPanel{}, "PolarAligner": PolarAligner{}, "GPS": GPS{},
	"Dome": Dome{}, "WeatherStation": WeatherStation{},
	"SkyQualityMeter": SkyQualityMeter{}, "Switch": Switch{},
	"Receiver": Receiver{}, "Guider": Guider{}, "Reservation": Reservation{},
}

func TestEachGoTypeMatchesItsCRD(t *testing.T) {
	for _, kind := range Kinds {
		t.Run(kind.Name, func(t *testing.T) {
			object := reflect.TypeOf(kindTypes[kind.Name])
			schema := schemaOf(t, kind)
			for _, half := range []string{"Spec", "Status"} {
				field, _ := object.FieldByName(half)
				property := schema.Properties[strings.ToLower(half)]
				for _, problem := range compare(field.Type, &property, strings.ToLower(half)) {
					t.Error(problem)
				}
			}
		})
	}
}

var (
	rawJSON  = reflect.TypeFor[json.RawMessage]()
	timeType = reflect.TypeFor[time.Time]()
)

// compare answers each difference between a Go type and a schema, by
// the path of the field.
func compare(goType reflect.Type, schema *apiextensionsv1.JSONSchemaProps, path string) []string {
	for goType.Kind() == reflect.Pointer {
		goType = goType.Elem()
	}
	want, format := schemaType(goType)
	var problems []string
	if schema.Type != want {
		return []string{path + ": Go has " + want + ", the CRD has " + schema.Type}
	}
	if format != "" && schema.Format != format {
		problems = append(problems, path+": Go has format "+format+", the CRD has "+schema.Format)
	}
	switch {
	case goType == rawJSON:
		if schema.XPreserveUnknownFields == nil || !*schema.XPreserveUnknownFields {
			problems = append(problems, path+": raw JSON needs x-kubernetes-preserve-unknown-fields")
		}
	case goType.Kind() == reflect.Slice:
		problems = append(problems, compare(goType.Elem(), schema.Items.Schema, path+"[]")...)
	case goType.Kind() == reflect.Struct && goType != timeType:
		fields := jsonFields(goType)
		for _, name := range sortedKeys(fields) {
			property, held := schema.Properties[name]
			if !held {
				problems = append(problems, path+"."+name+": in Go, not in the CRD")
				continue
			}
			problems = append(problems, compare(fields[name], &property, path+"."+name)...)
		}
		for name := range schema.Properties {
			if _, held := fields[name]; !held {
				problems = append(problems, path+"."+name+": in the CRD, not in Go")
			}
		}
	}
	return problems
}

// schemaType answers the OpenAPI type and format of a Go type.
func schemaType(goType reflect.Type) (string, string) {
	switch {
	case goType == rawJSON:
		return "object", ""
	case goType == timeType:
		return "string", "date-time"
	}
	switch goType.Kind() {
	case reflect.String:
		return "string", ""
	case reflect.Bool:
		return "boolean", ""
	case reflect.Int32:
		return "integer", "int32"
	case reflect.Int64:
		return "integer", "int64"
	case reflect.Float64:
		return "number", ""
	case reflect.Slice:
		return "array", ""
	case reflect.Struct:
		return "object", ""
	}
	return goType.Kind().String(), ""
}

// jsonFields answers the fields that encoding/json writes for a struct,
// by their JSON names, with the fields of an embedded struct at the
// level of the struct that embeds it.
func jsonFields(goType reflect.Type) map[string]reflect.Type {
	fields := map[string]reflect.Type{}
	for field := range goType.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if field.Anonymous && name == "" {
			for inner, innerType := range jsonFields(field.Type) {
				fields[inner] = innerType
			}
			continue
		}
		fields[name] = field.Type
	}
	return fields
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// The walk above must see a difference, or a passing test proves
// nothing. Each case changes one thing in a schema that matches.
func TestCompareFindsEachKindOfDifference(t *testing.T) {
	type inner struct {
		Count int32 `json:"count"`
	}
	type sample struct {
		Name  string          `json:"name"`
		Items []inner         `json:"items"`
		Raw   json.RawMessage `json:"raw"`
		When  *time.Time      `json:"when"`
	}
	preserve := true
	matching := func() *apiextensionsv1.JSONSchemaProps {
		return &apiextensionsv1.JSONSchemaProps{Type: "object", Properties: map[string]apiextensionsv1.JSONSchemaProps{
			"name": {Type: "string"},
			"items": {Type: "array", Items: &apiextensionsv1.JSONSchemaPropsOrArray{Schema: &apiextensionsv1.JSONSchemaProps{
				Type: "object", Properties: map[string]apiextensionsv1.JSONSchemaProps{
					"count": {Type: "integer", Format: "int32"},
				}}}},
			"raw":  {Type: "object", XPreserveUnknownFields: &preserve},
			"when": {Type: "string", Format: "date-time"},
		}}
	}
	cases := []struct {
		name   string
		change func(*apiextensionsv1.JSONSchemaProps)
		want   string
	}{
		{"nothing", func(*apiextensionsv1.JSONSchemaProps) {}, ""},
		{"a field missing from the CRD", func(s *apiextensionsv1.JSONSchemaProps) {
			delete(s.Properties, "name")
		}, "x.name: in Go, not in the CRD"},
		{"a field missing from Go", func(s *apiextensionsv1.JSONSchemaProps) {
			s.Properties["extra"] = apiextensionsv1.JSONSchemaProps{Type: "string"}
		}, "x.extra: in the CRD, not in Go"},
		{"another type", func(s *apiextensionsv1.JSONSchemaProps) {
			s.Properties["name"] = apiextensionsv1.JSONSchemaProps{Type: "integer"}
		}, "x.name: Go has string, the CRD has integer"},
		{"another format in an item", func(s *apiextensionsv1.JSONSchemaProps) {
			s.Properties["items"].Items.Schema.Properties["count"] = apiextensionsv1.JSONSchemaProps{Type: "integer", Format: "int64"}
		}, "x.items[].count: Go has format int32, the CRD has int64"},
		{"raw JSON that the API server prunes", func(s *apiextensionsv1.JSONSchemaProps) {
			s.Properties["raw"] = apiextensionsv1.JSONSchemaProps{Type: "object"}
		}, "x.raw: raw JSON needs x-kubernetes-preserve-unknown-fields"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			schema := matching()
			c.change(schema)
			got := strings.Join(compare(reflect.TypeFor[sample](), schema, "x"), "; ")
			if got != c.want {
				t.Errorf("compare = %q, want %q", got, c.want)
			}
		})
	}
}
