package kubernetes

// The one thing this package reads Services for: which of them are
// of type LoadBalancer.

import (
	"encoding/json"
	"net/http"
	"testing"
)

// serviceList answers a list request with the given Services, in the
// envelope the API server wraps every collection in.
func serviceList(t *testing.T, services ...map[string]any) *Client {
	t.Helper()
	return testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"kind":  "ServiceList",
			"items": services,
		})
	}))
}

func serviceOfType(namespace, name, serviceType string) map[string]any {
	return map[string]any{
		"metadata": map[string]any{"namespace": namespace, "name": name},
		"spec":     map[string]any{"type": serviceType},
	}
}

func TestListLoadBalancerServicesKeepsOnlyTheOnesAControllerAnswers(t *testing.T) {
	client := serviceList(t,
		serviceOfType("default", "kubernetes", "ClusterIP"),
		serviceOfType("default", "whoami", "LoadBalancer"),
		serviceOfType("kube-system", "traefik", "NodePort"),
	)

	services, err := ListLoadBalancerServices(client)
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 || services[0].Metadata.Name != "whoami" {
		t.Errorf("only a LoadBalancer Service waits on a cleanup finalizer: %+v", services)
	}
	if services[0].Metadata.Namespace != "default" {
		t.Errorf("the namespace names which Service to act on: %+v", services[0])
	}
}

func TestListLoadBalancerServicesReadsAnEmptyClusterAsEmpty(t *testing.T) {
	services, err := ListLoadBalancerServices(serviceList(t))
	if err != nil || len(services) != 0 {
		t.Errorf("got %+v, %v", services, err)
	}
}

func TestListLoadBalancerServicesReportsAFailedRead(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "the server is currently unable to handle the request", http.StatusServiceUnavailable)
	}))

	if _, err := ListLoadBalancerServices(client); err == nil {
		t.Error("a failed read must never read as an empty cluster")
	}
}
