package main

// request-reboot and approve-reboot write a Disruption beside each
// annotation, against an API server that serves the kind.

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/disruption"
	"github.com/liken-sh/liken/liken/machine"
)

// disruptionsServer answers a Machine document, a SelfSubjectReview
// for kubernetes-admin, the Disruptions it holds, and records each
// Disruption the commands write.
type disruptionsServer struct {
	machine string
	held    []disruption.Disruption
	written []disruption.Disruption

	// conflicts answers this many writes with 409, as the API server
	// does for a write from a read that cluster-operator's status write
	// made stale.
	conflicts int
}

func (s *disruptionsServer) start(t *testing.T, dir string) *httptest.Server {
	t.Helper()
	serverCert, clientCAs := issueServerCertForTest(t, dir)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/selfsubjectreviews"):
			fmt.Fprint(w, `{"status":{"userInfo":{"username":"kubernetes-admin"}}}`)
		case strings.HasSuffix(r.URL.Path, "/disruptions") && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"items": s.held})
		case strings.Contains(r.URL.Path, "/disruptions/") && r.Method == http.MethodGet:
			name := path.Base(r.URL.Path)
			at := slices.IndexFunc(s.held, func(d disruption.Disruption) bool { return d.Metadata.Name == name })
			_ = json.NewEncoder(w).Encode(s.held[at])
		case strings.Contains(r.URL.Path, "/disruptions") && s.conflicts > 0:
			s.conflicts--
			w.WriteHeader(http.StatusConflict)
			fmt.Fprint(w, `{"kind":"Status","code":409}`)
		case strings.Contains(r.URL.Path, "/disruptions"):
			var d disruption.Disruption
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &d)
			s.written = append(s.written, d)
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{}`)
		case r.Method == http.MethodGet:
			fmt.Fprint(w, s.machine)
		default:
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

func TestRequestRebootFilesADisruptionForTheRunningBoot(t *testing.T) {
	dir := deploymentDirForTest(t)
	s := &disruptionsServer{machine: `{"metadata":{"name":"node-5","uid":"uid-node-5"},
		"spec":{"rebootPolicy":"Auto"},"status":{"boot":{"time":"2026-07-06T09:30:00Z"}}}`}
	server := s.start(t, dir)

	var out bytes.Buffer
	if err := requestReboot([]string{"-server", server.URL, dir, "node-5"}, &out); err != nil {
		t.Fatal(err)
	}

	boot := machine.BootID(machine.BootStatus{Time: &requestBootTime})
	if len(s.written) != 1 {
		t.Fatalf("wrote %d Disruptions, want one", len(s.written))
	}
	d := s.written[0]
	if d.Metadata.Name != "node-5.reboot-"+boot[:12] || d.Spec.Machine != "node-5" || d.Spec.Hash != boot ||
		d.Spec.Kind != disruption.KindReboot || d.Spec.Requester != "kubernetes-admin" {
		t.Errorf("the Disruption = %s %+v", d.Metadata.Name, d.Spec)
	}
	if !strings.Contains(out.String(), "filed: Disruption node-5.reboot-"+boot[:12]) {
		t.Errorf("the output does not name the Disruption:\n%s", out.String())
	}
}

func TestApproveRebootApprovesTheDisruptionThatCoversTheChange(t *testing.T) {
	cases := []struct {
		name      string
		condition string
		hash      string
		conflicts int
		approve   string
	}{
		{"a staged document", "SpecConverged", "aaaabbbbcccc0000", 0, "node-5.machine-operator"},
		{"a person's reboot", "RebootRequestHonored", "3943abfa6adf0000", 0, "node-5.reboot-3943abfa6adf"},
		{"a write that a status write made stale", "SpecConverged", "aaaabbbbcccc0000", 2, "node-5.machine-operator"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := deploymentDirForTest(t)
			m := &machine.Machine{}
			m.Metadata.Name = "node-5"
			own := disruption.New("node-5.machine-operator", m, disruption.Spec{Machine: "node-5",
				Requester: disruption.MachineOperator, Kind: disruption.KindReboot, Hash: "6f2a91c04b7e5d"})
			person := disruption.New("node-5.reboot-3943abfa6adf", m, disruption.Spec{Machine: "node-5",
				Requester: "kubernetes-admin", Kind: disruption.KindReboot, Hash: "3943abfa6adf0000"})
			s := &disruptionsServer{
				machine: fmt.Sprintf(`{"metadata":{"name":"node-5"},"status":{"pending":[
					{"condition":%q,"kind":"Reboot","hash":%q,"summary":"what waits"}]}}`, c.condition, c.hash),
				held:      []disruption.Disruption{*own, *person},
				conflicts: c.conflicts,
			}
			server := s.start(t, dir)

			var out bytes.Buffer
			if err := approveReboot([]string{"-server", server.URL, dir, "node-5"}, &out); err != nil {
				t.Fatal(err)
			}

			if len(s.written) != 1 || s.written[0].Metadata.Name != c.approve || s.written[0].Spec.ApprovedHash != s.written[0].Spec.Hash {
				t.Errorf("wrote %+v, want %s with approvedHash set to its hash", s.written, c.approve)
			}
			if !strings.Contains(out.String(), "approved: Disruption "+c.approve) {
				t.Errorf("the output does not name the Disruption:\n%s", out.String())
			}
		})
	}
}
