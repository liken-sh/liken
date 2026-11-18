package main

// Checks of deploy/monitoring against the Deployment and the metrics
// that the operator serves.

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/yaml"
)

// metricsContainer holds the fields of the operator's Deployment that
// the tests read.
type metricsContainer struct {
	Spec struct {
		Template struct {
			Metadata struct {
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Spec struct {
				Containers []struct {
					Env   []envVar        `json:"env"`
					Ports []containerPort `json:"ports"`
				} `json:"containers"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
}

type podMonitor struct {
	Spec struct {
		Selector struct {
			MatchLabels map[string]string `json:"matchLabels"`
		} `json:"selector"`
		PodMetricsEndpoints []struct {
			Port        string `json:"port"`
			Relabelings []struct {
				SourceLabels []string `json:"sourceLabels"`
				TargetLabel  string   `json:"targetLabel"`
			} `json:"relabelings"`
		} `json:"podMetricsEndpoints"`
	} `json:"spec"`
}

func TestThePodMonitorScrapesTheOperatorsListener(t *testing.T) {
	t.Parallel()
	var d metricsContainer
	manifest(t, "operator.yaml", "Deployment", &d)
	var m podMonitor
	manifest(t, "monitoring/podmonitor.yaml", "PodMonitor", &m)

	for key, value := range m.Spec.Selector.MatchLabels {
		if d.Spec.Template.Metadata.Labels[key] != value {
			t.Errorf("the PodMonitor selects %s=%s, which the pod does not have", key, value)
		}
	}
	operator := d.Spec.Template.Spec.Containers[0]
	endpoint := m.Spec.PodMetricsEndpoints[0]
	if !slices.Contains(operator.Ports, containerPort{Name: endpoint.Port, ContainerPort: 9200}) {
		t.Errorf("the PodMonitor scrapes port %q, and the container declares %+v", endpoint.Port, operator.Ports)
	}
	if !slices.Contains(operator.Env, envVar{Name: metricsAddressVariable, Value: ":9200"}) {
		t.Errorf("the container does not set %s to :9200: %+v", metricsAddressVariable, operator.Env)
	}
	relabel := endpoint.Relabelings[0]
	if !slices.Equal(relabel.SourceLabels, []string{"__meta_kubernetes_pod_node_name"}) || relabel.TargetLabel != "node" {
		t.Errorf("the PodMonitor relabelings are %+v, want pod_node_name copied to node", endpoint.Relabelings)
	}
}

// servedNames returns the name of every series that the operator's
// collectors describe, with the extra series of a histogram.
func servedNames(t *testing.T) map[string]bool {
	t.Helper()
	m := newMetrics("dev")
	descriptions := make(chan *prometheus.Desc)
	go func() {
		for _, c := range []prometheus.Collector{
			m.reconcileDuration, m.reconcileErrors, m.watchRestarts,
			m.deviceConnected, m.deviceClaimed, m.deviceDisconnects,
			m.observationValid, m.observationSuccess,
			m.telescopePhase, m.activationDuration, m.stepFailures,
			m.cameraTemperature, m.cameraCoolerPower, m.guidingError, m.guiderState,
		} {
			c.Describe(descriptions)
		}
		close(descriptions)
	}()
	fqName := regexp.MustCompile(`fqName: "([a-z_]+)"`)
	names := map[string]bool{"liken_build_info": true}
	for d := range descriptions {
		name := fqName.FindStringSubmatch(d.String())[1]
		names[name] = true
		for _, suffix := range []string{"_bucket", "_sum", "_count"} {
			names[name+suffix] = true
		}
	}
	return names
}

// A provisioned dashboard must take its datasource from a template
// variable.
func TestTheDashboardReadsOnlyServedSeries(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("deploy/monitoring/dashboards/observatory-operator.json")
	if err != nil {
		t.Fatal(err)
	}
	var dashboard struct {
		Inputs     []any `json:"__inputs"`
		Templating struct {
			List []struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"list"`
		} `json:"templating"`
		Panels []struct {
			Title      string `json:"title"`
			Datasource struct {
				UID string `json:"uid"`
			} `json:"datasource"`
			Targets []struct {
				Expr string `json:"expr"`
			} `json:"targets"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(raw, &dashboard); err != nil {
		t.Fatal(err)
	}
	if dashboard.Inputs != nil || len(dashboard.Templating.List) != 1 || dashboard.Templating.List[0].Type != "datasource" || dashboard.Templating.List[0].Name != "datasource" {
		t.Errorf("the dashboard templating is %+v and its inputs are %v, want one datasource variable and no inputs", dashboard.Templating, dashboard.Inputs)
	}
	served := servedNames(t)
	series := regexp.MustCompile(`\b(?:observatory|liken)_[a-z_]+\b`)
	for _, panel := range dashboard.Panels {
		if panel.Datasource.UID != "${datasource}" {
			t.Errorf("%s reads the datasource %q", panel.Title, panel.Datasource.UID)
		}
		for _, target := range panel.Targets {
			for _, name := range series.FindAllString(target.Expr, -1) {
				if !served[name] {
					t.Errorf("%s reads %s, which the operator does not serve", panel.Title, name)
				}
			}
		}
	}
}

// The kustomize component sets no namespace, because that would also
// move the dashboard's ConfigMap out of the monitoring namespace.
func TestTheMonitoringComponentAddsItsFiles(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("deploy/monitoring/kustomization.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var component struct {
		Kind      string   `json:"kind"`
		Namespace string   `json:"namespace"`
		Resources []string `json:"resources"`
		Generator []struct {
			Namespace string   `json:"namespace"`
			Files     []string `json:"files"`
		} `json:"configMapGenerator"`
	}
	if err := yaml.Unmarshal(raw, &component); err != nil {
		t.Fatal(err)
	}
	if component.Kind != "Component" || component.Namespace != "" || component.Generator[0].Namespace != "monitoring" {
		t.Errorf("the component is %+v, want kind Component, no namespace, and a generator in monitoring", component)
	}
	for _, file := range append(component.Resources, component.Generator[0].Files...) {
		if _, err := os.Stat("deploy/monitoring/" + strings.TrimSpace(file)); err != nil {
			t.Errorf("the component names %s: %v", file, err)
		}
	}
}
