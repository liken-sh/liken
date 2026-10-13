package inditest

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"testing/synctest"
)

func park() *Property {
	return &Property{Type: "Switch", Device: "Telescope Simulator", Name: "TELESCOPE_PARK", State: "Ok", Perm: "rw", Rule: "OneOfMany",
		Members: []Member{{Name: "PARK", Value: "On"}, {Name: "UNPARK", Value: "Off"}}}
}

func TestSwitchTurnsOneMemberOn(t *testing.T) {
	p := park()
	p.Switch("UNPARK")
	parked, _ := p.Value("PARK")
	unparked, _ := p.Value("UNPARK")
	if parked != "Off" || unparked != "On" {
		t.Errorf("PARK = %s, UNPARK = %s", parked, unparked)
	}
	if _, ok := p.Value("NONESUCH"); ok {
		t.Error("a member that the property lacks has a value")
	}
}

// A definition and an update name the device, the property, and each
// member, with the text escaped.
func TestDefAndSetWriteTheElements(t *testing.T) {
	p := park()
	p.Device = `A & B`
	if got := p.Def(); !strings.HasPrefix(got, `<defSwitchVector device="A &amp; B" name="TELESCOPE_PARK"`) || !strings.Contains(got, `rule="OneOfMany"`) {
		t.Errorf("def = %s", got)
	}
	if got := p.Set(); got != `<setSwitchVector device="A &amp; B" name="TELESCOPE_PARK" state="Ok"><oneSwitch name="PARK">On</oneSwitch><oneSwitch name="UNPARK">Off</oneSwitch></setSwitchVector>`+"\n" {
		t.Errorf("set = %s", got)
	}
}

// A getProperties that names a device answers that device alone, and a
// ping gets its reply, as indiserver answers both.
func TestGetPropertiesOfOneDeviceAndAPing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := NewServer(t, "ccd", "telescope")
		conn, err := s.DialContext(t.Context(), "tcp", "indiserver.test:7624")
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		lines := read(conn)
		_, _ = conn.Write([]byte(`<getProperties version="1.7" device="Telescope Simulator"/><pingRequest uid="7"/>`))
		synctest.Wait()
		got := lines()
		if strings.Contains(got, "CCD Simulator") || !strings.Contains(got, `device="Telescope Simulator"`) {
			t.Errorf("answer names another device: %.200s", got)
		}
		if !strings.HasSuffix(got, `<pingReply uid="7"/>`+"\n") {
			t.Errorf("no ping reply at the end: %.200s", got[max(0, len(got)-200):])
		}
	})
}

// read reads what the server sends in the background, and answers a
// function that returns what arrived so far.
func read(conn net.Conn) func() string {
	var b strings.Builder
	done := make(chan struct{})
	go func() {
		defer close(done)
		lines := bufio.NewScanner(conn)
		lines.Buffer(make([]byte, 1<<20), 1<<20)
		for lines.Scan() {
			b.WriteString(lines.Text() + "\n")
		}
	}()
	return func() string {
		conn.Close()
		<-done
		return b.String()
	}
}
