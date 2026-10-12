package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// The CLI copies the kinds it accepts, because it shares no code with
// the operator: each kind with procedures, whose CRD has
// status.setUpUntil.
func TestTheKindsAreTheOperatorsKindsWithProcedures(t *testing.T) {
	var want []confirmable
	for _, kind := range append([]observatory.Kind{observatory.ObservatoryKind, observatory.TelescopeKind}, observatory.DeviceKinds...) {
		want = append(want, confirmable{kind.Name, kind.Plural})
	}
	if !slices.Equal(confirmables, want) {
		t.Errorf("confirmables = %v, want %v", confirmables, want)
	}
}

func TestKindNamed(t *testing.T) {
	cases := []struct {
		word, want string
	}{
		{"telescope", "Telescope"},
		{"Telescope", "Telescope"},
		{"telescopes", "Telescope"},
		{"weatherstation", "WeatherStation"},
		{"gpses", "GPS"},
	}
	for _, c := range cases {
		t.Run(c.word, func(t *testing.T) {
			if got, err := kindNamed(c.word); err != nil || got.kind != c.want {
				t.Errorf("kindNamed(%q) = %v, %v, want %s", c.word, got, err, c.want)
			}
		})
	}
}

func TestParseUntil(t *testing.T) {
	now := time.Date(2026, 10, 12, 21, 30, 0, 0, time.UTC)
	cases := []struct {
		text, want string
	}{
		{"", ""},
		{"2026-10-13T04:30:00Z", "2026-10-13T04:30:00Z"},
		{"2026-10-13T00:30:00-04:00", "2026-10-13T04:30:00Z"},
		{"6h", "2026-10-13T03:30:00Z"},
	}
	for _, c := range cases {
		t.Run(c.text, func(t *testing.T) {
			until, err := parseUntil(c.text, now)
			got := ""
			if until != nil {
				got = until.Format(time.RFC3339)
			}
			if err != nil || got != c.want {
				t.Errorf("parseUntil(%q) = %q, %v, want %q", c.text, got, err, c.want)
			}
		})
	}
}

// The transition time moves when the status or the reason changes, and
// stays when a person says the same thing again.
func TestSetUpConditionKeepsItsTimeUntilItChanges(t *testing.T) {
	now := time.Date(2026, 10, 12, 21, 30, 0, 0, time.UTC)
	earlier := "2026-10-12T19:00:00Z"
	held := func(status, reason string) map[string]any {
		return map[string]any{"type": "SetUp", "status": status, "reason": reason, "lastTransitionTime": earlier}
	}
	cases := []struct {
		name string
		up   bool
		held map[string]any
		want string
	}{
		{"a first set-up", true, nil, "True SetUp Set up by stargazer 2026-10-12T21:30:00Z"},
		{"a second set-up", true, held("True", "SetUp"), "True SetUp Set up by stargazer " + earlier},
		{"a set-up after a take-down", true, held("False", "TakenDown"), "True SetUp Set up by stargazer 2026-10-12T21:30:00Z"},
		{"a take-down", false, held("True", "SetUp"), "False TakenDown Taken down by stargazer 2026-10-12T21:30:00Z"},
		{"a take-down after an expiry", false, held("False", "Expired"), "False TakenDown Taken down by stargazer 2026-10-12T21:30:00Z"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := setUpCondition(c.up, "stargazer", c.held, now)
			if text := strings.Join([]string{got["status"].(string), got["reason"].(string), got["message"].(string), got["lastTransitionTime"].(string)}, " "); text != c.want {
				t.Errorf("setUpCondition = %q, want %q", text, c.want)
			}
		})
	}
}

// fakeCluster answers the requests a verb sends: the operator's
// Deployment, the person's review, the resource, and the apply, which
// it records.
type fakeCluster struct {
	mu      sync.Mutex
	applied []*http.Request
	bodies  []map[string]any
	// held is the resource the cluster holds.
	held map[string]any
	// operator is the version of the operator's image, or the CLI's own
	// version when it is empty.
	operator string
}

func (f *fakeCluster) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	operator := f.operator
	if operator == "" {
		operator = version
	}
	switch {
	case r.URL.Path == "/apis/apps/v1/deployments":
		_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "apps/v1", "kind": "DeploymentList", "items": []any{map[string]any{
			"metadata": map[string]any{"name": "observatory-operator", "namespace": "liken-system", "labels": map[string]any{pluginLabel: pluginDomain}},
			"spec": map[string]any{"template": map[string]any{"spec": map[string]any{"containers": []any{map[string]any{
				"name": "operator", "image": "ghcr.io/liken-sh/observatory-operator:" + operator}}}}},
		}}})
	case r.URL.Path == "/apis/authentication.k8s.io/v1/selfsubjectreviews":
		_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "authentication.k8s.io/v1", "kind": "SelfSubjectReview",
			"status": map[string]any{"userInfo": map[string]any{"username": "stargazer"}}})
	case r.Method == http.MethodGet && r.URL.Path == "/apis/observatory.liken.sh/v1alpha1/namespaces/backyard/telescopes/east":
		f.mu.Lock()
		defer f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(f.held)
	case r.Method == http.MethodPatch && r.URL.Path == "/apis/observatory.liken.sh/v1alpha1/namespaces/backyard/telescopes/east/status":
		body, _ := io.ReadAll(r.Body)
		var applied map[string]any
		_ = json.Unmarshal(body, &applied)
		f.mu.Lock()
		f.applied, f.bodies = append(f.applied, r), append(f.bodies, applied)
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(f.held)
	default:
		http.NotFound(w, r)
	}
}

func telescopeEast() map[string]any {
	return map[string]any{"apiVersion": apiVersion, "kind": "Telescope",
		"metadata": map[string]any{"name": "east", "namespace": "backyard"},
		"spec":     map[string]any{"observatory": "lab"},
		"status": map[string]any{"conditions": []any{
			map[string]any{"type": "Ready", "status": "False", "reason": "Idle", "message": "Not reserved", "lastTransitionTime": "2026-10-12T12:00:00Z"},
		}}}
}

func clusterClients(t *testing.T, f *fakeCluster) clients {
	server := apiservertest.Start(t, f)
	typed, err := kubernetes.NewForConfig(server.Config())
	if err != nil {
		t.Fatal(err)
	}
	dyn, err := dynamic.NewForConfig(server.Config())
	if err != nil {
		t.Fatal(err)
	}
	return clients{typed: typed, dynamic: dyn, namespace: "backyard", kubeUser: "admin"}
}

// set-up applies the SetUp condition, with the name that the API
// server gives the person and the --until, to the status subresource
// as the CLI's own field manager, and touches no other condition.
func TestSetUpAppliesTheConditionToTheStatus(t *testing.T) {
	f := &fakeCluster{held: telescopeEast()}
	c := clusterClients(t, f)
	now := time.Date(2026, 10, 12, 21, 30, 0, 0, time.UTC)
	until := now.Add(6 * time.Hour)
	var stdout, stderr strings.Builder
	err := writeSetUp(context.Background(), c, confirmable{"Telescope", "telescopes"},
		setUpOptions{Up: true, Name: "east", Now: now}, &until, &stdout, &stderr)
	if err != nil {
		t.Fatalf("writeSetUp: %v", err)
	}
	if len(f.applied) != 1 {
		t.Fatalf("the cluster received %d applies", len(f.applied))
	}
	r := f.applied[0]
	if got := r.Header.Get("Content-Type"); got != "application/apply-patch+yaml" {
		t.Errorf("Content-Type = %q", got)
	}
	if q := r.URL.Query(); q.Get("fieldManager") != fieldManager || q.Get("force") != "true" {
		t.Errorf("query = %v", q)
	}
	want := map[string]any{"apiVersion": apiVersion, "kind": "Telescope",
		"metadata": map[string]any{"name": "east", "namespace": "backyard"},
		"status": map[string]any{
			"setUpUntil": "2026-10-13T03:30:00Z",
			"conditions": []any{map[string]any{"type": "SetUp", "status": "True", "reason": "SetUp",
				"message": "Set up by stargazer", "lastTransitionTime": "2026-10-12T21:30:00Z"}},
		}}
	if got, _ := json.Marshal(f.bodies[0]); string(got) != mustJSON(t, want) {
		t.Errorf("applied\n%s\nwant\n%s", got, mustJSON(t, want))
	}
	if got := stdout.String(); got != "Telescope east is set up until 2026-10-13T03:30:00Z\n" {
		t.Errorf("stdout = %q", got)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q", stderr.String())
	}
}

// --by names the person in place of the user the API server reports,
// and take-down writes SetUp False.
func TestTakeDownNamesThePersonGiven(t *testing.T) {
	f := &fakeCluster{held: telescopeEast()}
	c := clusterClients(t, f)
	var stdout strings.Builder
	err := writeSetUp(context.Background(), c, confirmable{"Telescope", "telescopes"},
		setUpOptions{Up: false, Name: "east", By: "Ada", Now: time.Now()}, nil, &stdout, io.Discard)
	if err != nil {
		t.Fatalf("writeSetUp: %v", err)
	}
	status := f.bodies[0]["status"].(map[string]any)
	condition := status["conditions"].([]any)[0].(map[string]any)
	if condition["status"] != "False" || condition["reason"] != "TakenDown" || condition["message"] != "Taken down by Ada" {
		t.Errorf("condition = %v", condition)
	}
	if _, has := status["setUpUntil"]; has {
		t.Errorf("a take-down wrote setUpUntil: %v", status)
	}
	if got := stdout.String(); got != "Telescope east is taken down\n" {
		t.Errorf("stdout = %q", got)
	}
}

// flagsGetter stands for the standard kube flags: it answers the
// fake cluster's config, and a kubeconfig whose current context names
// a namespace and a user. The verbs read nothing else of it.
type flagsGetter struct {
	genericclioptions.RESTClientGetter
	config *rest.Config
}

func (g flagsGetter) ToRESTConfig() (*rest.Config, error) { return g.config, nil }

func (g flagsGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	return clientcmd.NewDefaultClientConfig(clientcmdapi.Config{
		CurrentContext: "backyard",
		Contexts:       map[string]*clientcmdapi.Context{"backyard": {Cluster: "home", AuthInfo: "admin", Namespace: "backyard"}},
		Clusters:       map[string]*clientcmdapi.Cluster{"home": {Server: apiservertest.Host}},
		AuthInfos:      map[string]*clientcmdapi.AuthInfo{"admin": {}},
	}, &clientcmd.ConfigOverrides{})
}

// The verb reads the namespace from the kube flags, and an operator of
// another version gets a warning on stderr, which --force silences.
func TestRunSetUpReadsTheKubeFlags(t *testing.T) {
	cases := []struct {
		name, warning string
		force         bool
	}{
		{"a warning", "this CLI is dev and the operator is 2026.09.03-007; run kubectl liken plugins sync\n", false},
		{"a forced run", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeCluster{held: telescopeEast(), operator: "2026.09.03-007"}
			getter := flagsGetter{config: apiservertest.Start(t, f).Config()}
			var stdout, stderr strings.Builder
			err := runSetUp(context.Background(), getter, setUpOptions{Up: true, Kind: "telescope", Name: "east", Until: "6h", Force: c.force, Now: time.Now()}, &stdout, &stderr)
			if err != nil {
				t.Fatalf("runSetUp: %v", err)
			}
			if len(f.applied) != 1 || stderr.String() != c.warning {
				t.Errorf("applies = %d, stderr = %q", len(f.applied), stderr.String())
			}
		})
	}
}

// A resource that does not exist gets no apply, and the error names
// it.
func TestSetUpOfAMissingResourceFails(t *testing.T) {
	f := &fakeCluster{held: telescopeEast()}
	c := clusterClients(t, f)
	err := writeSetUp(context.Background(), c, confirmable{"Telescope", "telescopes"},
		setUpOptions{Up: true, Name: "west", Now: time.Now()}, nil, io.Discard, io.Discard)
	if err == nil || len(f.applied) != 0 {
		t.Fatalf("writeSetUp of a missing Telescope = %v, with %d applies", err, len(f.applied))
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
