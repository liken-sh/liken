package cluster

import (
	"strings"
	"testing"
)

func gogcPtr(n int) *int { return &n }

func percentPtr(n int) *int { return &n }

// An unset section imposes nothing: the memory limit resolves to off,
// and the collector pace reports itself unset. So init hands k3s no
// GOMEMLIMIT and no GOGC, and k3s runs on Go's own runtime defaults.
func TestRuntimeDefaultsImposeNothing(t *testing.T) {
	gib := uint64(1 << 30)
	spec := K3sRuntimeSpec{}
	if _, ok := spec.GoGCPercent(); ok {
		t.Error("an unset section must report no collector pace")
	}
	bytes, off, err := spec.GoMemoryLimitBytes(gib)
	if err != nil || !off || bytes != 0 {
		t.Errorf("unset limit: got %d off=%v err=%v, want off with no ceiling", bytes, off, err)
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
			bytes, off, err := K3sRuntimeSpec{GoMemoryLimit: tc.limit}.GoMemoryLimitBytes(gib)
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
	got, ok := (K3sRuntimeSpec{GoGC: gogcPtr(80)}).GoGCPercent()
	if !ok || got != 80 {
		t.Errorf("custom GoGC: got %d ok=%v, want 80 set", got, ok)
	}
}

// Validation refuses a malformed value, so it never reaches the fleet
// as staged bytes. Each error names the offending field.
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

// The file doors refuse every section the kubelet would refuse when it
// starts. A machine that boots a rejected configuration serves no
// shell, so the refusal has to happen before the bytes reach the fleet.
func TestImageGCValidationRejectsGarbage(t *testing.T) {
	cases := map[string]struct {
		spec ImageGCSpec
		want string
	}{
		"high above 100": {
			ImageGCSpec{HighThresholdPercent: percentPtr(101)}, "between 1 and 100",
		},
		"high below 1": {
			ImageGCSpec{HighThresholdPercent: percentPtr(0)}, "between 1 and 100",
		},
		"low above 100": {
			ImageGCSpec{LowThresholdPercent: percentPtr(120)}, "between 1 and 100",
		},
		"low below 1": {
			ImageGCSpec{LowThresholdPercent: percentPtr(-5)}, "between 1 and 100",
		},
		"low equal to high": {
			ImageGCSpec{HighThresholdPercent: percentPtr(70), LowThresholdPercent: percentPtr(70)},
			"lowThresholdPercent",
		},
		"low above high": {
			ImageGCSpec{HighThresholdPercent: percentPtr(70), LowThresholdPercent: percentPtr(80)},
			"lowThresholdPercent",
		},
		"lone low above the default high": {
			ImageGCSpec{LowThresholdPercent: percentPtr(90)}, "lowThresholdPercent",
		},
		"lone high below the default low": {
			ImageGCSpec{HighThresholdPercent: percentPtr(70)}, "lowThresholdPercent",
		},
		"unparseable minimum": {
			ImageGCSpec{MinimumAge: "soon"}, "minimumAge",
		},
		"unparseable maximum": {
			ImageGCSpec{MaximumAge: "7 days"}, "maximumAge",
		},
		"zero minimum": {
			ImageGCSpec{MinimumAge: "0s"}, "greater than zero",
		},
		"negative maximum": {
			ImageGCSpec{MaximumAge: "-1h"}, "greater than zero",
		},
		"maximum at the default minimum": {
			ImageGCSpec{MaximumAge: "2m"}, "maximumAge",
		},
		"maximum below the set minimum": {
			ImageGCSpec{MinimumAge: "1h", MaximumAge: "30m"}, "maximumAge",
		},
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

func TestImageGCValidationAcceptsGoodValues(t *testing.T) {
	good := []ImageGCSpec{
		{},
		{HighThresholdPercent: percentPtr(70), LowThresholdPercent: percentPtr(60)},
		{HighThresholdPercent: percentPtr(100), LowThresholdPercent: percentPtr(1)},
		{HighThresholdPercent: percentPtr(90)},
		{LowThresholdPercent: percentPtr(50)},
		{MaximumAge: "168h"},
		{MinimumAge: "1h", MaximumAge: "168h"},
		{MinimumAge: "30s"},
	}
	for _, spec := range good {
		if err := spec.Validate(); err != nil {
			t.Errorf("%+v should validate, got %v", spec, err)
		}
	}
}

// containerd exits when it reads a level it does not know, and k3s
// does not start without containerd, so the door here refuses
// everything that containerd's own reader would.
func TestContainerdValidationRejectsUnknownLevels(t *testing.T) {
	for _, level := range []string{"verbose", "DEBUG", "warning", "trace", "fatal", "panic", "0", " info"} {
		t.Run(level, func(t *testing.T) {
			err := ContainerdRuntimeSpec{LogLevel: level}.Validate()
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), "logLevel") {
				t.Errorf("the error should name the field, got: %v", err)
			}
		})
	}
}

func TestContainerdValidationAcceptsTheLevelsLikenOffers(t *testing.T) {
	for _, level := range []string{"", "debug", "info", "warn", "error"} {
		if err := (ContainerdRuntimeSpec{LogLevel: level}).Validate(); err != nil {
			t.Errorf("level %q should validate, got %v", level, err)
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
	if !strings.Contains(err.Error(), "spec.runtime") || !strings.Contains(err.Error(), "k3s") {
		t.Errorf("the error should name the field, got: %v", err)
	}
}

func TestParseClusterRejectsBadImageGC(t *testing.T) {
	_, err := ParseCluster([]byte(`
apiVersion: liken.sh/v1alpha1
kind: Cluster
metadata:
  name: lab
spec:
  runtime:
    kubelet:
      imageGC:
        highThresholdPercent: 60
        lowThresholdPercent: 70
`))
	if err == nil {
		t.Fatal("expected an error for a low threshold above the high one")
	}
	if !strings.Contains(err.Error(), "spec.runtime") || !strings.Contains(err.Error(), "kubelet") {
		t.Errorf("the error should name the field, got: %v", err)
	}
}

func TestParseClusterAcceptsImageGC(t *testing.T) {
	c, err := ParseCluster([]byte(`
apiVersion: liken.sh/v1alpha1
kind: Cluster
metadata:
  name: lab
spec:
  runtime:
    kubelet:
      imageGC:
        highThresholdPercent: 70
        lowThresholdPercent: 60
        minimumAge: 5m
        maximumAge: 168h
`))
	if err != nil {
		t.Fatal(err)
	}
	gc := c.KubeletSpec().ImageGC
	if *gc.HighThresholdPercent != 70 || *gc.LowThresholdPercent != 60 {
		t.Errorf("thresholds not parsed: %+v", gc)
	}
	if gc.MinimumAge != "5m" || gc.MaximumAge != "168h" {
		t.Errorf("ages not parsed: %+v", gc)
	}
}

// A machine with no cluster document gets the zero section, so the
// kubelet keeps its own policy rather than crashing the boot.
func TestKubeletSpecOnANilCluster(t *testing.T) {
	var c *Cluster
	if c.KubeletSpec().ImageGC != (ImageGCSpec{}) {
		t.Error("a machine alone names no image collection policy")
	}
}

func TestContainerdSpecOnANilCluster(t *testing.T) {
	var c *Cluster
	if c.ContainerdSpec() != (ContainerdRuntimeSpec{}) {
		t.Error("a machine alone names no containerd log level")
	}
}

func TestParseClusterRejectsAnUnknownContainerdLevel(t *testing.T) {
	_, err := ParseCluster([]byte(`
apiVersion: liken.sh/v1alpha1
kind: Cluster
metadata:
  name: lab
spec:
  runtime:
    containerd:
      logLevel: verbose
`))
	if err == nil {
		t.Fatal("expected an error for a level containerd does not know")
	}
	if !strings.Contains(err.Error(), "spec.runtime") || !strings.Contains(err.Error(), "containerd") {
		t.Errorf("the error should name the field, got: %v", err)
	}
}

func TestParseClusterAcceptsTheLogLevels(t *testing.T) {
	c, err := ParseCluster([]byte(`
apiVersion: liken.sh/v1alpha1
kind: Cluster
metadata:
  name: lab
spec:
  runtime:
    k3s:
      debug: true
    containerd:
      logLevel: warn
`))
	if err != nil {
		t.Fatal(err)
	}
	if !c.RuntimeSpec().Debug {
		t.Error("the k3s debug flag did not parse")
	}
	if got := c.ContainerdSpec().LogLevel; got != "warn" {
		t.Errorf("containerd's level did not parse: %q", got)
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
	gc, ok := c.RuntimeSpec().GoGCPercent()
	if c.Spec.Runtime.K3s.GoMemoryLimit != "448Mi" || !ok || gc != 80 {
		t.Errorf("runtime not parsed: %+v", c.Spec.Runtime)
	}
}
