package main

// These tests cover request-reboot: renderRequest reports the path
// the machine's rebootPolicy puts it on. disruptions_test.go drives
// the whole command against a fake API server.

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

var requestBootTime = time.Date(2026, 7, 6, 9, 30, 0, 0, time.UTC)

func requestedMachine(policy machine.RebootPolicy) *machine.Machine {
	m := &machine.Machine{Metadata: api.ObjectMeta{Name: "node-5"}}
	m.Spec.RebootPolicy = policy
	m.Status.Boot.Time = &requestBootTime
	return m
}

func TestRenderRequestSaysAnAutoMachineNeedsNothingMore(t *testing.T) {
	got := renderRequest(requestedMachine(machine.RebootAuto), "mycluster", "node-5.reboot-3943abfa6adf")
	for _, want := range []string{
		"created: Disruption node-5.reboot-3943abfa6adf",
		"waits for a reboot turn from the cluster, drains its workloads, and reboots",
		"No change is staged",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("the report is missing %q, want it in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "approve-reboot") {
		t.Fatalf("the report for an Auto machine names approve-reboot, want no second step:\n%s", got)
	}
}

func TestRenderRequestListsTheStagedChangesTheRebootApplies(t *testing.T) {
	m := requestedMachine(machine.RebootAuto)
	m.Status.Pending = []machine.PendingDisruption{
		{Condition: "RebootRequestHonored", Kind: machine.DisruptionReboot, Hash: "3943abfa6adf0000", Summary: "a person requested a reboot"},
		{Condition: "SpecConverged", Kind: machine.DisruptionReboot, Hash: "a1b2c3d4e5f60000", Summary: "kernel arguments change"},
	}
	got := renderRequest(m, "mycluster", "node-5.reboot-3943abfa6adf")
	for _, want := range []string{
		"The reboot also applies every staged change",
		"kernel arguments change (a1b2c3d4e5f6)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("the report is missing %q, want it in:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"No change is staged", "a person requested a reboot"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("the report contains %q, want it left out:\n%s", unwanted, got)
		}
	}
}

func TestRenderRequestNamesTheApprovalCommandForAManualMachine(t *testing.T) {
	got := renderRequest(requestedMachine(machine.RebootManual), "mycluster", "node-5.reboot-3943abfa6adf")
	for _, want := range []string{
		"rebootPolicy: Manual",
		"liken approve-reboot mycluster node-5",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("the report is missing %q, want it in:\n%s", want, got)
		}
	}
}

func TestRenderRequestUsesTheDefaultPolicyWhenNoneIsDeclared(t *testing.T) {
	m := &machine.Machine{Metadata: api.ObjectMeta{Name: "node-5"}}
	m.Status.Boot.Time = &requestBootTime
	if got := renderRequest(m, "mycluster", "node-5.reboot-3943abfa6adf"); !strings.Contains(got, "approve-reboot") {
		t.Fatalf("the report for a machine with no rebootPolicy lacks approve-reboot, want it because Manual is the default:\n%s", got)
	}
}

// requestServer stands in for the API server: it answers the GET
// with one Machine document and records the PATCH the command sends.
func requestServer(t *testing.T, dir, document string, patch *[]byte) *httptest.Server {
	t.Helper()
	serverCert, clientCAs := issueServerCertForTest(t, dir)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			fmt.Fprint(w, document)
		case http.MethodPatch:
			*patch, _ = io.ReadAll(r.Body)
			fmt.Fprint(w, `{}`)
		}
	}))
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAs,
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

func TestRequestRebootRefusesAMachineThatReportsNoBoot(t *testing.T) {
	dir := deploymentDirForTest(t)
	var gotPatch []byte
	server := requestServer(t, dir, `{"metadata":{"name":"node-5"},"status":{}}`, &gotPatch)

	var out bytes.Buffer
	err := requestReboot([]string{"-server", server.URL, dir, "node-5"}, &out)
	if err == nil {
		t.Fatal("requestReboot returned no error for a machine with no boot record, want an error")
	}
	if !strings.Contains(err.Error(), "boot time") {
		t.Fatalf("the error is %q, want it to mention the boot time", err)
	}
	if gotPatch != nil {
		t.Fatalf("the command sent the patch %s, want no write", gotPatch)
	}
}

func TestRequestRebootChecksItsArguments(t *testing.T) {
	if err := requestReboot([]string{"mycluster"}, io.Discard); err == nil {
		t.Fatal("requestReboot with one argument returned no error, want an error because it takes a deployment directory and a machine")
	}
}
