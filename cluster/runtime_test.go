package cluster

import (
	"strings"
	"testing"
)

func gogcPtr(n int) *int { return &n }

// An unset section resolves to today's defaults, byte for byte: a
// quarter of memory, seven sixteenths with helm, and GOGC 50.
func TestRuntimeDefaultsMatchTodaysDerivation(t *testing.T) {
	gib := uint64(1 << 30)
	spec := K3sRuntimeSpec{}
	if spec.GoGCPercent() != 50 {
		t.Errorf("default GoGC: got %d, want 50", spec.GoGCPercent())
	}
	lean, off, err := spec.GoMemoryLimitBytes(gib, false)
	if err != nil || off || lean != gib/4 {
		t.Errorf("lean default: got %d off=%v err=%v, want %d", lean, off, err, gib/4)
	}
	helm, _, _ := spec.GoMemoryLimitBytes(gib, true)
	if helm != gib/16*7 {
		t.Errorf("helm default: got %d, want %d", helm, gib/16*7)
	}
}

func TestRuntimeMemoryLimitForms(t *testing.T) {
	gib := uint64(1 << 30)
	cases := map[string]struct {
		limit     string
		wantBytes uint64
		wantOff   bool
	}{
		"percent":  {"25%", gib / 4, false},
		"absolute": {"448Mi", 448 << 20, false},
		"plainGi":  {"2Gi", 2 << 30, false},
		"off":      {"off", 0, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			bytes, off, err := K3sRuntimeSpec{GoMemoryLimit: tc.limit}.GoMemoryLimitBytes(gib, false)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if bytes != tc.wantBytes || off != tc.wantOff {
				t.Errorf("got %d off=%v, want %d off=%v", bytes, off, tc.wantBytes, tc.wantOff)
			}
		})
	}
}

func TestRuntimeGoGCResolves(t *testing.T) {
	if got := (K3sRuntimeSpec{GoGC: gogcPtr(80)}).GoGCPercent(); got != 80 {
		t.Errorf("custom GoGC: got %d, want 80", got)
	}
}

// Validation refuses garbage loudly, so a bad value never reaches the
// fleet as staged bytes. Each error names the offending field.
func TestRuntimeValidationRejectsGarbage(t *testing.T) {
	cases := map[string]struct {
		spec K3sRuntimeSpec
		want string
	}{
		"unparseable limit": {K3sRuntimeSpec{GoMemoryLimit: "lots"}, "goMemoryLimit"},
		"decimal quantity":  {K3sRuntimeSpec{GoMemoryLimit: "1.5Gi"}, "goMemoryLimit"},
		"percent over 100":  {K3sRuntimeSpec{GoMemoryLimit: "150%"}, "between 1% and 100%"},
		"percent zero":      {K3sRuntimeSpec{GoMemoryLimit: "0%"}, "between 1% and 100%"},
		"zero ceiling":      {K3sRuntimeSpec{GoMemoryLimit: "0Mi"}, "can't be zero"},
		"wrapping quantity": {K3sRuntimeSpec{GoMemoryLimit: "99999999999999999999Ti"}, "goMemoryLimit"},
		"overflow quantity": {K3sRuntimeSpec{GoMemoryLimit: "18446744073709551615Ti"}, "too large"},
		"gogc below one":    {K3sRuntimeSpec{GoGC: gogcPtr(0)}, "at least 1"},
		"gogc negative":     {K3sRuntimeSpec{GoGC: gogcPtr(-5)}, "at least 1"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := tc.spec.Validate()
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q should mention %q", err, tc.want)
			}
		})
	}
}

func TestRuntimeValidationAcceptsGoodValues(t *testing.T) {
	good := []K3sRuntimeSpec{
		{},
		{GoMemoryLimit: "off"},
		{GoMemoryLimit: "25%"},
		{GoMemoryLimit: "100%"},
		{GoMemoryLimit: "448Mi"},
		{GoGC: gogcPtr(1)},
		{GoMemoryLimit: "512Mi", GoGC: gogcPtr(200)},
	}
	for _, spec := range good {
		if err := spec.Validate(); err != nil {
			t.Errorf("%+v should validate, got %v", spec, err)
		}
	}
}

// A bad runtime section fails the whole parse, at the same door that
// refuses a null feature, so init and the operator reject it alike.
func TestParseClusterRejectsBadRuntime(t *testing.T) {
	_, err := ParseCluster([]byte(`
apiVersion: liken.sh/v1alpha1
kind: Cluster
metadata:
  name: lab
spec:
  runtime:
    k3s:
      goMemoryLimit: "150%"
`))
	if err == nil {
		t.Fatal("expected an error for an out-of-range percent")
	}
	if !strings.Contains(err.Error(), "spec.runtime.k3s") {
		t.Errorf("the error should name the field, got: %v", err)
	}
}

func TestParseClusterAcceptsRuntime(t *testing.T) {
	c, err := ParseCluster([]byte(`
apiVersion: liken.sh/v1alpha1
kind: Cluster
metadata:
  name: lab
spec:
  runtime:
    k3s:
      goMemoryLimit: "448Mi"
      goGC: 80
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Spec.Runtime.K3s.GoMemoryLimit != "448Mi" || c.RuntimeSpec().GoGCPercent() != 80 {
		t.Errorf("runtime not parsed: %+v", c.Spec.Runtime)
	}
}
