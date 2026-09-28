package main

// The watches hand the pass objects that the unstructured converter
// decoded, where a direct read decodes them with encoding/json. These
// tests hold the two decoders to the same answer for every struct the
// pass reads from a copy, with every field of the struct set.

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/machine"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utiljson "k8s.io/apimachinery/pkg/util/json"
)

// fill sets every exported field of v, to depth levels of nesting: a
// string, a number, a bool, a time, one element of a slice, and one
// entry of a map.
func fill(v reflect.Value, depth int) {
	if depth == 0 {
		return
	}
	switch v.Kind() {
	case reflect.String:
		v.SetString("s")
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(7)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(7)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1.5)
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		fill(v.Elem(), depth-1)
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		fill(v.Index(0), depth-1)
	case reflect.Map:
		v.Set(reflect.MakeMap(v.Type()))
		key := reflect.New(v.Type().Key()).Elem()
		fill(key, depth-1)
		value := reflect.New(v.Type().Elem()).Elem()
		fill(value, depth-1)
		v.SetMapIndex(key, value)
	case reflect.Struct:
		if v.Type() == reflect.TypeFor[time.Time]() {
			v.Set(reflect.ValueOf(time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)))
			return
		}
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				fill(v.Field(i), depth-1)
			}
		}
	}
}

// decodesAlike fills a T, encodes it, and decodes the bytes both ways:
// with encoding/json, the way a direct read does, and into the
// unstructured form a watch delivers and then through the converter.
func decodesAlike[T any](t *testing.T) {
	t.Helper()
	var filled T
	fill(reflect.ValueOf(&filled).Elem(), 12)
	encoded, err := json.Marshal(&filled)
	if err != nil {
		t.Fatal(err)
	}
	var direct T
	if err := json.Unmarshal(encoded, &direct); err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := utiljson.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	watched, err := informer.Convert[T](&unstructured.Unstructured{Object: object})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(direct, watched) {
		d, _ := json.Marshal(direct)
		w, _ := json.Marshal(watched)
		t.Errorf("the copy decoded\n%s\nwant the direct read's\n%s", w, d)
	}
}

func TestTheCopiesDecodeLikeADirectRead(t *testing.T) {
	cases := []struct {
		kind  string
		check func(*testing.T)
	}{
		{"Machine", decodesAlike[machine.Machine]},
		{"Cluster", decodesAlike[cluster.Cluster]},
		{"Node", decodesAlike[nodeObject]},
		{"Pod", decodesAlike[kubernetes.Pod]},
		{"Secret", decodesAlike[kubernetes.Secret]},
		{"ResourceSlice", decodesAlike[kubernetes.ResourceSlice]},
	}
	for _, c := range cases {
		t.Run(c.kind, c.check)
	}
}
