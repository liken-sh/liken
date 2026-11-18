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
		t.Errorf("after Switch(UNPARK), PARK is %s and UNPARK is %s, want Off and On", parked, unparked)
	}
	if _, ok := p.Value("NONESUCH"); ok {
		t.Error("Value(NONESUCH) reports a value for a member the property lacks")
	}
}

// Def and Set escape XML special characters in the names they write.
func TestDefAndSetWriteTheElements(t *testing.T) {
	p := park()
	p.Device = `A & B`
	if got := p.Def(); !strings.HasPrefix(got, `<defSwitchVector device="A &amp; B" name="TELESCOPE_PARK"`) || !strings.Contains(got, `rule="OneOfMany"`) {
		t.Errorf("Def() = %s, want a defSwitchVector with the escaped device name and rule OneOfMany", got)
	}
	if got := p.Set(); got != `<setSwitchVector device="A &amp; B" name="TELESCOPE_PARK" state="Ok"><oneSwitch name="PARK">On</oneSwitch><oneSwitch name="UNPARK">Off</oneSwitch></setSwitchVector>`+"\n" {
		t.Errorf("Set() = %s, want a setSwitchVector with the escaped device name", got)
	}
}

// A getProperties for one device returns only that device's
// properties, and a ping gets a reply, as with indiserver.
func TestGetPropertiesOfOneDeviceAndAPing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := NewServer(t, "ccd", "telescope")
		conn, err := s.DialContext(t.Context(), "tcp", "indiserver.test:7624")
		if err != nil {
			t.Fatalf("dialing the fake INDI server: %v", err)
		}
		defer conn.Close()
		lines := read(conn)
		_, _ = conn.Write([]byte(`<getProperties version="1.7" device="Telescope Simulator"/><pingRequest uid="7"/>`))
		synctest.Wait()
		got := lines()
		if strings.Contains(got, "CCD Simulator") || !strings.Contains(got, `device="Telescope Simulator"`) {
			t.Errorf("the reply to getProperties names another device or none: %.200s, want only Telescope Simulator", got)
		}
		if !strings.HasSuffix(got, `<pingReply uid="7"/>`+"\n") {
			t.Errorf("the reply ends with %.200s, want a pingReply for uid 7", got[max(0, len(got)-200):])
		}
	})
}

// read collects what the server sends, in a goroutine. It returns a
// function that closes the connection and returns everything that
// arrived.
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
