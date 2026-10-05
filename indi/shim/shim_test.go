package main

import (
	"bytes"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTheTargetIsTheProgramsOwnName(t *testing.T) {
	cases := map[string]struct {
		argv0, want string
	}{
		"a path":           {"/run/indi/drivers/ccd.observatory:7625", "ccd.observatory:7625"},
		"a bare name":      {"mount:7625", "mount:7625"},
		"a service's name": {"focuser.observatory.svc.cluster.local:7625", "focuser.observatory.svc.cluster.local:7625"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := target(c.argv0)
			if err != nil || got != c.want {
				t.Fatalf("target(%q) = %q, %v; want %q", c.argv0, got, err, c.want)
			}
		})
	}
}

func TestANameWithNoPortIsRefused(t *testing.T) {
	for _, argv0 := range []string{"/usr/bin/indi-shim", "ccd.observatory", ":7625"} {
		if got, err := target(argv0); err == nil {
			t.Errorf("target(%q) = %q, want an error", argv0, got)
		}
	}
}

// freeAddress is an address on which nothing listens yet.
func freeAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := l.Addr().String()
	l.Close()
	return address
}

func TestTheShimWaitsForADeviceThatIsNotUpYet(t *testing.T) {
	address := freeAddress(t)
	go func() {
		time.Sleep(300 * time.Millisecond)
		l, err := net.Listen("tcp", address)
		if err != nil {
			return
		}
		defer l.Close()
		c, err := l.Accept()
		if err == nil {
			c.Close()
		}
	}()
	conn, err := dial(address, 50*time.Millisecond, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
}

func TestTheShimGivesUpAtItsDeadline(t *testing.T) {
	start := time.Now()
	if _, err := dial(freeAddress(t), 50*time.Millisecond, 300*time.Millisecond); err == nil {
		t.Fatal("dial connected to an address where nothing listens")
	}
	if waited := time.Since(start); waited < 300*time.Millisecond {
		t.Errorf("gave up after %v, before its deadline", waited)
	}
}

// device is one end of a connected pair of sockets, for the device
// pod's side of the shim.
func device(t *testing.T) (shimSide, deviceSide net.Conn) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, _ := l.Accept()
		accepted <- c
	}()
	shimSide, err = net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return shimSide, <-accepted
}

func TestTheShimCopiesBothWaysUntilTheDeviceCloses(t *testing.T) {
	shimSide, deviceSide := device(t)
	fromServer := bytes.NewBufferString(`<getProperties version="1.7"/>`)
	toServer := &bytes.Buffer{}
	serverIn, serverInWriter := io.Pipe()
	go func() {
		io.Copy(serverInWriter, fromServer)
	}()
	done := make(chan struct{})
	go func() {
		relay(serverIn, toServer, shimSide)
		close(done)
	}()

	got := make([]byte, len(`<getProperties version="1.7"/>`))
	if _, err := io.ReadFull(deviceSide, got); err != nil || string(got) != `<getProperties version="1.7"/>` {
		t.Fatalf("the device read %q, %v", got, err)
	}
	deviceSide.Write([]byte(`<defSwitchVector device="CCD Simulator"/>`))
	deviceSide.Close()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the shim kept running after the device closed")
	}
	if toServer.String() != `<defSwitchVector device="CCD Simulator"/>` {
		t.Errorf("the server read %q", toServer.String())
	}
}

func TestTheShimEndsWhenTheServerCloses(t *testing.T) {
	shimSide, deviceSide := device(t)
	defer deviceSide.Close()
	done := make(chan struct{})
	go func() {
		relay(bytes.NewReader(nil), io.Discard, shimSide)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the shim kept running after the server closed its stdin")
	}
}

// The server's pod has no shell to make the links, so the shim makes
// them itself, in an init container.
func TestLinkMakesOneLinkForEachDevice(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(t.TempDir(), "indi-shim")
	if err := os.WriteFile(self, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := link(self, dir, []string{"mount.observatory:7625", "ccd.observatory:7625"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"mount.observatory:7625", "ccd.observatory:7625"} {
		got, err := os.Readlink(filepath.Join(dir, name))
		if err != nil || got != self {
			t.Errorf("%s links to %q, %v; want %q", name, got, err, self)
		}
		if address, err := target(filepath.Join(dir, name)); err != nil || address != name {
			t.Errorf("the link %s reads as %q, %v", name, address, err)
		}
	}
}

func TestLinkRefusesANameWithNoPort(t *testing.T) {
	if err := link("/usr/bin/indi-shim", t.TempDir(), []string{"mount.observatory"}); err == nil {
		t.Error("link made a link that the shim could not read an address from")
	}
}
