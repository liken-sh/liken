package inditest_test

// These tests drive the fake with the real client from package indi, so
// they check that the fake responds correctly to what that client
// sends.

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/indi/inditest"
)

const (
	ccd       = "CCD Simulator"
	telescope = "Telescope Simulator"
)

// connected returns a client of a server that runs the CCD and telescope
// simulators, with both devices connected.
func connected(t *testing.T) (*indi.Client, *inditest.Server) {
	s := inditest.NewServer(t, "ccd", "indi_simulator_telescope")
	c := indi.NewClient("indiserver.test:7624", indi.WithDialer(s))
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(t.Context())
	go func() { done <- c.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	for _, device := range []string{ccd, telescope} {
		if err := c.WaitFor(t.Context(), func(s *indi.Store) bool { _, ok := s.Property(device, "CONNECTION"); return ok }); err != nil {
			t.Fatalf("waiting for the CONNECTION property of %s: %v", device, err)
		}
		if err := c.ConnectDevice(t.Context(), device); err != nil {
			t.Fatalf("connecting %s: %v", device, err)
		}
	}
	// The server sends the connect transcript's definitions after the
	// CONNECTION update that ConnectDevice waits for, so wait for them.
	synctest.Wait()
	return c, s
}

func TestConnectingADeviceDefinesItsConnectTranscript(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, s := connected(t)
		if _, ok := c.Property(ccd, "CCD_EXPOSURE"); !ok {
			t.Error("CCD_EXPOSURE is not defined after connecting the CCD, want it defined")
		}
		if got := s.Connected(); !slices.Equal(got, []string{ccd, telescope}) {
			t.Errorf("connected devices = %v, want %v", got, []string{ccd, telescope})
		}
		if err := c.DisconnectDevice(t.Context(), ccd); err != nil {
			t.Fatalf("disconnecting the CCD: %v", err)
		}
		synctest.Wait()
		if _, ok := c.Property(ccd, "CCD_EXPOSURE"); ok || s.Property(ccd, "CCD_EXPOSURE") != nil {
			t.Error("CCD_EXPOSURE is still defined after disconnecting the CCD, want it removed")
		}
	})
}

func TestAChangeSettlesWithTheValuesSent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, s := connected(t)
		sent, err := c.SetTexts(ccd, "UPLOAD_SETTINGS", map[string]string{"UPLOAD_DIR": "/outbox/m42", "UPLOAD_PREFIX": "frame"})
		if err != nil {
			t.Fatalf("sending UPLOAD_SETTINGS: %v", err)
		}
		p, err := c.Settle(t.Context(), sent)
		if err != nil || p.State != indi.Ok {
			t.Fatalf("Settle = %+v, %v, want state Ok and no error", p, err)
		}
		if dir, _ := s.Property(ccd, "UPLOAD_SETTINGS").Value("UPLOAD_DIR"); dir != "/outbox/m42" {
			t.Errorf("UPLOAD_DIR = %q, want /outbox/m42", dir)
		}
		if got := s.Changes(); !slices.Contains(got, "CCD Simulator.UPLOAD_SETTINGS UPLOAD_DIR=/outbox/m42 UPLOAD_PREFIX=frame") {
			t.Errorf("the changes are %q, want one for UPLOAD_SETTINGS with UPLOAD_DIR=/outbox/m42", got)
		}
	})
}

// An exposure stays Busy until the test sets its state.
func TestAMotionEndsWhenTheTestSetsItsState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, s := connected(t)
		s.Move(ccd, "CCD_EXPOSURE")
		events := c.Subscribe(t.Context())
		sent, err := c.SetNumbers(ccd, "CCD_EXPOSURE", map[string]float64{"CCD_EXPOSURE_VALUE": 300})
		if err != nil {
			t.Fatalf("sending CCD_EXPOSURE: %v", err)
		}
		synctest.Wait()
		if p := s.Property(ccd, "CCD_EXPOSURE"); p.State != "Busy" {
			t.Errorf("CCD_EXPOSURE is %s during the exposure, want Busy", p.State)
		}
		s.Message(ccd, "Image saved to /outbox/m42/frame_001.fits")
		s.SetState(ccd, "CCD_EXPOSURE", "Ok")
		if p, err := c.Settle(t.Context(), sent); err != nil || p.State != indi.Ok {
			t.Errorf("Settle = %+v, %v, want state Ok and no error", p, err)
		}
		if got := firstMessage(t, events); !strings.Contains(got, "frame_001.fits") {
			t.Errorf("the first message is %q, want one that contains frame_001.fits", got)
		}
	})
}

// firstMessage returns the text of the first Message event, and fails
// the test when none arrives within a minute on the bubble's clock.
func firstMessage(t *testing.T, events <-chan indi.Event) string {
	timeout := time.After(time.Minute)
	for {
		select {
		case e := <-events:
			if e.Kind == indi.Message {
				return e.Message
			}
		case <-timeout:
			t.Fatal("no message arrived within a minute")
		}
	}
}

// An abort ends the slew with state Ok. A change during a slew
// interrupts it, and the state becomes Alert.
func TestAnAbortEndsTheSlew(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, s := connected(t)
		s.Move(telescope, "EQUATORIAL_EOD_COORD")
		if _, err := c.SetNumbers(telescope, "EQUATORIAL_EOD_COORD", map[string]float64{"RA": 5.6, "DEC": -5.4}); err != nil {
			t.Fatalf("sending EQUATORIAL_EOD_COORD: %v", err)
		}
		synctest.Wait()
		if _, err := c.SetSwitches(telescope, "TELESCOPE_ABORT_MOTION", map[string]bool{"ABORT": true}); err != nil {
			t.Fatalf("sending TELESCOPE_ABORT_MOTION: %v", err)
		}
		synctest.Wait()
		if p := s.Property(telescope, "EQUATORIAL_EOD_COORD"); p.State != "Ok" {
			t.Errorf("EQUATORIAL_EOD_COORD is %s after the abort, want Ok", p.State)
		}
		if _, err := c.SetNumbers(telescope, "EQUATORIAL_EOD_COORD", map[string]float64{"RA": 6, "DEC": 0}); err != nil {
			t.Fatalf("sending EQUATORIAL_EOD_COORD: %v", err)
		}
		synctest.Wait()
		if _, err := c.SetNumbers(telescope, "EQUATORIAL_EOD_COORD", map[string]float64{"RA": 7, "DEC": 0}); err != nil {
			t.Fatalf("sending EQUATORIAL_EOD_COORD: %v", err)
		}
		synctest.Wait()
		if p := s.Property(telescope, "EQUATORIAL_EOD_COORD"); p.State != "Alert" {
			t.Errorf("EQUATORIAL_EOD_COORD is %s after a change during the slew, want Alert", p.State)
		}
	})
}

// An abort during a park ends the park with Alert and every switch Off.
func TestAnAbortInterruptsAPark(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, s := connected(t)
		s.Move(telescope, "TELESCOPE_PARK")
		if _, err := c.SetSwitches(telescope, "TELESCOPE_PARK", map[string]bool{"PARK": true}); err != nil {
			t.Fatalf("sending TELESCOPE_PARK: %v", err)
		}
		synctest.Wait()
		if _, err := c.SetSwitches(telescope, "TELESCOPE_ABORT_MOTION", map[string]bool{"ABORT": true}); err != nil {
			t.Fatalf("sending TELESCOPE_ABORT_MOTION: %v", err)
		}
		synctest.Wait()
		park := s.Property(telescope, "TELESCOPE_PARK")
		if on, _ := park.Value("PARK"); park.State != "Alert" || on != "Off" {
			t.Errorf("TELESCOPE_PARK is %s with PARK %s after the abort, want Alert with PARK Off", park.State, on)
		}
	})
}

func TestARefusedChangeSettlesWithAlert(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, s := connected(t)
		s.Refuse(ccd, "UPLOAD_MODE")
		sent, err := c.SetSwitches(ccd, "UPLOAD_MODE", map[string]bool{"UPLOAD_LOCAL": true})
		if err != nil {
			t.Fatalf("sending UPLOAD_MODE: %v", err)
		}
		var alert *indi.AlertError
		if _, err := c.Settle(t.Context(), sent); !errors.As(err, &alert) {
			t.Errorf("Settle = %v, want an AlertError", err)
		}
		s.Accept(ccd, "UPLOAD_MODE")
		sent, _ = c.SetSwitches(ccd, "UPLOAD_MODE", map[string]bool{"UPLOAD_LOCAL": true})
		if _, err := c.Settle(t.Context(), sent); err != nil {
			t.Errorf("Settle after Accept = %v, want no error", err)
		}
	})
}

// A held property's driver never answers, so Settle returns when its
// context's deadline passes. After Release, the driver answers the next
// change.
func TestAHeldChangeGetsNoAnswer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, s := connected(t)
		s.Hold(ccd, "FITS_HEADER")
		sent, err := c.SetTexts(ccd, "FITS_HEADER", map[string]string{"KEYWORD_NAME": "OBJECT", "KEYWORD_VALUE": "M42"})
		if err != nil {
			t.Fatalf("sending FITS_HEADER: %v", err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		if _, err := c.Settle(ctx, sent); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Settle = %v, want context.DeadlineExceeded", err)
		}
		s.Release(ccd, "FITS_HEADER")
		sent, _ = c.SetTexts(ccd, "FITS_HEADER", map[string]string{"KEYWORD_NAME": "OBJECT", "KEYWORD_VALUE": "M42"})
		if _, err := c.Settle(t.Context(), sent); err != nil {
			t.Errorf("Settle after Release = %v, want no error", err)
		}
	})
}

func TestAReportReachesTheClient(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, s := connected(t)
		s.Report(telescope, "EQUATORIAL_EOD_COORD", "RA", "5.5")
		synctest.Wait()
		p, _ := c.Property(telescope, "EQUATORIAL_EOD_COORD")
		if got := p.Members[0].Number; got != 5.5 {
			t.Errorf("RA = %v, want 5.5", got)
		}
	})
}

// A cut closes each connection while the drivers keep running, and a
// closed server refuses a dial, as a closed port does.
func TestCutClosesConnectionsAndCloseRefusesDials(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, s := connected(t)
		if s.Clients() != 1 {
			t.Errorf("clients = %d, want 1", s.Clients())
		}
		s.Cut()
		synctest.Wait()
		if s.Clients() != 0 || len(s.Connected()) != 2 {
			t.Errorf("after the cut, clients = %d and connected devices = %v, want 0 clients and 2 devices", s.Clients(), s.Connected())
		}
		s.Close()
		if _, err := s.DialContext(t.Context(), "tcp", "indiserver.test:7624"); err == nil {
			t.Error("a closed server accepted a dial, want a refusal")
		}
	})
}

func TestADriverOfNoSimulatorFails(t *testing.T) {
	if _, err := inditest.NewDriver("nonesuch"); err == nil {
		t.Error("NewDriver(\"nonesuch\") returned no error, want one")
	}
}

func TestTheFrameHeaderStartsWithSIMPLEAndEndsWithEND(t *testing.T) {
	header := string(inditest.FrameHeader())
	if !strings.HasPrefix(header, "SIMPLE  =") || !strings.Contains(header, "\nEND") {
		t.Errorf("the header starts %q, want SIMPLE  = and an END card", header[:min(40, len(header))])
	}
}

func TestDefinitionsRefusesBrokenXML(t *testing.T) {
	if _, err := inditest.Definitions([]byte(`<defTextVector device="a" name="b"><defText`)); err == nil {
		t.Error("Definitions accepted broken XML, want an error")
	}
}
