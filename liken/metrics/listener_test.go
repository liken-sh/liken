package metrics

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestTheListenerAnswersOnItsOwnPort(t *testing.T) {
	o := NewOperator("liken-machine-operator", "2026.09.10-001", nil, nil)
	// Port 0 asks the kernel for a free port, so the test never
	// competes with anything else on the machine for 9200.
	addr, err := o.Serve("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get("http://" + addr.String() + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `liken_build_info{component="liken-machine-operator",version="2026.09.10-001"} 1`) {
		t.Errorf("the listener served no build info:\n%s", body)
	}
}

func TestAnEmptyAddressServesNothing(t *testing.T) {
	addr, err := NewOperator("liken-cluster-operator", "dev", nil, nil).Serve("")
	if err != nil {
		t.Fatalf("an empty address is not a failure: %v", err)
	}
	if addr != nil {
		t.Errorf("an empty address opened %s", addr)
	}
}

func TestAPortAlreadyInUseIsAnError(t *testing.T) {
	// The machine operator runs on the host's network, so its port
	// belongs to the whole machine. Another program that already
	// holds the port must fail the bind here, where the caller can
	// report it, and never leave the operator serving nothing while
	// it claims to serve.
	held, err := NewOperator("liken-machine-operator", "dev", nil, nil).Serve("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewOperator("liken-cluster-operator", "dev", nil, nil).Serve(held.String()); err == nil {
		t.Errorf("binding %s twice reported no error", held)
	}
}
