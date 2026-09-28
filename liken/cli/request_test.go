package main

// These tests cover request-reboot: renderRequest reports the path
// the machine's rebootPolicy puts it on, and
// TestRequestRebootNamesTheRunningBoot drives the whole command
// against a fake API server, the way approve_test.go drives
// approve-reboot.

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

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/machine"
)

var requestBootTime = time.Date(2026, 7, 6, 9, 30, 0, 0, time.UTC)

func requestedMachine(policy machine.RebootPolicy) *machine.Machine {
	m := &machine.Machine{Metadata: api.ObjectMeta{Name: "node-5"}}
	m.Spec.RebootPolicy = policy
	m.Status.Boot.Time = &requestBootTime
	return m
}

func TestRenderRequestSaysAnAutoMachineNeedsNothingMore(t *testing.T) {
	got := renderRequest(requestedMachine(machine.RebootAuto), "mycluster", "3943abfa6adf")
	for _, want := range []string{
		"requested: liken.sh/request-reboot=3943abfa6adf",
		"takes a reboot turn from the cluster, drains its workloads, and reboots",
		"Nothing is staged",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "approve-reboot") {
		t.Fatalf("an Auto machine takes no second step:\n%s", got)
	}
}

func TestRenderRequestNamesTheApprovalCommandForAManualMachine(t *testing.T) {
	got := renderRequest(requestedMachine(machine.RebootManual), "mycluster", "3943abfa6adf")
	for _, want := range []string{
		"rebootPolicy: Manual",
		"liken approve-reboot mycluster node-5",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestRenderRequestUsesTheDefaultPolicyWhenNoneIsDeclared(t *testing.T) {
	m := &machine.Machine{Metadata: api.ObjectMeta{Name: "node-5"}}
	m.Status.Boot.Time = &requestBootTime
	if got := renderRequest(m, "mycluster", "3943abfa6adf"); !strings.Contains(got, "approve-reboot") {
		t.Fatalf("Manual is the default, so the report must name the second step:\n%s", got)
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

func TestRequestRebootNamesTheRunningBoot(t *testing.T) {
	dir := deploymentDirForTest(t)
	var gotPatch []byte
	server := requestServer(t, dir, `{"metadata":{"name":"node-5"},
		"spec":{"rebootPolicy":"Auto"},
		"status":{"boot":{"time":"2026-07-06T09:30:00Z"}}}`, &gotPatch)

	var out bytes.Buffer
	if err := requestReboot([]string{"-server", server.URL, dir, "node-5"}, &out); err != nil {
		t.Fatal(err)
	}
	// The operator computes the same identity from the same boot
	// record, so this is the value that makes the request match.
	want := machine.BootID(machine.BootStatus{Time: &requestBootTime})[:12]
	if !strings.Contains(string(gotPatch), fmt.Sprintf(`"liken.sh/request-reboot":%q`, want)) {
		t.Fatalf("the patch must name this boot: %s", gotPatch)
	}
	if !strings.Contains(out.String(), "requested: liken.sh/request-reboot="+want) {
		t.Fatalf("the confirmation is missing from the output:\n%s", out.String())
	}
}

func TestRequestRebootRefusesAMachineThatReportsNoBoot(t *testing.T) {
	dir := deploymentDirForTest(t)
	var gotPatch []byte
	server := requestServer(t, dir, `{"metadata":{"name":"node-5"},"status":{}}`, &gotPatch)

	var out bytes.Buffer
	err := requestReboot([]string{"-server", server.URL, dir, "node-5"}, &out)
	if err == nil {
		t.Fatal("a machine with no boot record has no boot to name")
	}
	if !strings.Contains(err.Error(), "no boot time") {
		t.Fatalf("got %v", err)
	}
	if gotPatch != nil {
		t.Fatalf("nothing should be written: %s", gotPatch)
	}
}

func TestRequestRebootChecksItsArguments(t *testing.T) {
	if err := requestReboot([]string{"mycluster"}, io.Discard); err == nil {
		t.Fatal("the command takes a deployment directory and a machine")
	}
}
