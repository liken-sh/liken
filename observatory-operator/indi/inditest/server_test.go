package inditest_test

// These tests run the real INDI client of package indi against the
// fake, so the fake answers what the client sends.

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

// connected answers a client of a server of the CCD and telescope
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
			t.Fatal(err)
		}
		if err := c.ConnectDevice(t.Context(), device); err != nil {
			t.Fatal(err)
		}
	}
	// The driver defines what its connect transcript defines after
	// CONNECTION is Ok, as the simulators do.
	synctest.Wait()
	return c, s
}

func TestConnectingADeviceDefinesItsConnectTranscript(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, s := connected(t)
		if _, ok := c.Property(ccd, "CCD_EXPOSURE"); !ok {
			t.Error("CCD_EXPOSURE is not defined after the connect")
		}
		if got := s.Connected(); !slices.Equal(got, []string{ccd, telescope}) {
			t.Errorf("connected = %v", got)
		}
		if err := c.DisconnectDevice(t.Context(), ccd); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if _, ok := c.Property(ccd, "CCD_EXPOSURE"); ok || s.Property(ccd, "CCD_EXPOSURE") != nil {
			t.Error("CCD_EXPOSURE is still defined after the disconnect")
		}
	})
}

func TestAChangeSettlesWithTheValuesSent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, s := connected(t)
		sent, err := c.SetTexts(ccd, "UPLOAD_SETTINGS", map[string]string{"UPLOAD_DIR": "/outbox/m42", "UPLOAD_PREFIX": "frame"})
		if err != nil {
			t.Fatal(err)
		}
		p, err := c.Settle(t.Context(), sent)
		if err != nil || p.State != indi.Ok {
			t.Fatalf("settle = %+v, %v", p, err)
		}
		if dir, _ := s.Property(ccd, "UPLOAD_SETTINGS").Value("UPLOAD_DIR"); dir != "/outbox/m42" {
			t.Errorf("UPLOAD_DIR = %q", dir)
		}
		if got := s.Changes(); !slices.Contains(got, "CCD Simulator.UPLOAD_SETTINGS UPLOAD_DIR=/outbox/m42 UPLOAD_PREFIX=frame") {
			t.Errorf("changes = %q", got)
		}
	})
}

// An exposure moves until the test ends it, and the driver's message
// names the file, as a driver that saves a frame does.
func TestAMotionEndsWhenTheTestSetsItsState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, s := connected(t)
		s.Move(ccd, "CCD_EXPOSURE")
		events := c.Subscribe(t.Context())
		sent, err := c.SetNumbers(ccd, "CCD_EXPOSURE", map[string]float64{"CCD_EXPOSURE_VALUE": 300})
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if p := s.Property(ccd, "CCD_EXPOSURE"); p.State != "Busy" {
			t.Errorf("state during the exposure = %s", p.State)
		}
		s.Message(ccd, "Image saved to /outbox/m42/frame_001.fits")
		s.SetState(ccd, "CCD_EXPOSURE", "Ok")
		if p, err := c.Settle(t.Context(), sent); err != nil || p.State != indi.Ok {
			t.Errorf("settle = %+v, %v", p, err)
		}
		if got := firstMessage(t, events); !strings.Contains(got, "frame_001.fits") {
			t.Errorf("message = %q", got)
		}
	})
}

// firstMessage answers the text of the first Message event, and fails
// the test when none arrives within a minute of the bubble's clock.
func firstMessage(t *testing.T, events <-chan indi.Event) string {
	timeout := time.After(time.Minute)
	for {
		select {
		case e := <-events:
			if e.Kind == indi.Message {
				return e.Message
			}
		case <-timeout:
			t.Fatal("no message arrived")
		}
	}
}

// An abort ends the slew it interrupts, and a second change during a
// motion interrupts it with Alert.
func TestAnAbortEndsTheSlew(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, s := connected(t)
		s.Move(telescope, "EQUATORIAL_EOD_COORD")
		if _, err := c.SetNumbers(telescope, "EQUATORIAL_EOD_COORD", map[string]float64{"RA": 5.6, "DEC": -5.4}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if _, err := c.SetSwitches(telescope, "TELESCOPE_ABORT_MOTION", map[string]bool{"ABORT": true}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if p := s.Property(telescope, "EQUATORIAL_EOD_COORD"); p.State != "Ok" {
			t.Errorf("state after the abort = %s", p.State)
		}
		if _, err := c.SetNumbers(telescope, "EQUATORIAL_EOD_COORD", map[string]float64{"RA": 6, "DEC": 0}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if _, err := c.SetNumbers(telescope, "EQUATORIAL_EOD_COORD", map[string]float64{"RA": 7, "DEC": 0}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if p := s.Property(telescope, "EQUATORIAL_EOD_COORD"); p.State != "Alert" {
			t.Errorf("state after a change during the slew = %s", p.State)
		}
	})
}

// An abort during a park ends the park with Alert and every switch Off.
func TestAnAbortInterruptsAPark(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, s := connected(t)
		s.Move(telescope, "TELESCOPE_PARK")
		if _, err := c.SetSwitches(telescope, "TELESCOPE_PARK", map[string]bool{"PARK": true}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if _, err := c.SetSwitches(telescope, "TELESCOPE_ABORT_MOTION", map[string]bool{"ABORT": true}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		park := s.Property(telescope, "TELESCOPE_PARK")
		if on, _ := park.Value("PARK"); park.State != "Alert" || on != "Off" {
			t.Errorf("park = %s, PARK = %s", park.State, on)
		}
	})
}

func TestARefusedChangeAnswersAlert(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, s := connected(t)
		s.Refuse(ccd, "UPLOAD_MODE")
		sent, err := c.SetSwitches(ccd, "UPLOAD_MODE", map[string]bool{"UPLOAD_LOCAL": true})
		if err != nil {
			t.Fatal(err)
		}
		var alert *indi.AlertError
		if _, err := c.Settle(t.Context(), sent); !errors.As(err, &alert) {
			t.Errorf("settle = %v, want an alert", err)
		}
		s.Accept(ccd, "UPLOAD_MODE")
		sent, _ = c.SetSwitches(ccd, "UPLOAD_MODE", map[string]bool{"UPLOAD_LOCAL": true})
		if _, err := c.Settle(t.Context(), sent); err != nil {
			t.Errorf("settle after accept = %v", err)
		}
	})
}

// A held property's driver never answers, so the client's wait ends on
// its own deadline, and a release answers the next change.
func TestAHeldChangeGetsNoAnswer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, s := connected(t)
		s.Hold(ccd, "FITS_HEADER")
		sent, err := c.SetTexts(ccd, "FITS_HEADER", map[string]string{"KEYWORD_NAME": "OBJECT", "KEYWORD_VALUE": "M42"})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		if _, err := c.Settle(ctx, sent); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("settle = %v, want the deadline", err)
		}
		s.Release(ccd, "FITS_HEADER")
		sent, _ = c.SetTexts(ccd, "FITS_HEADER", map[string]string{"KEYWORD_NAME": "OBJECT", "KEYWORD_VALUE": "M42"})
		if _, err := c.Settle(t.Context(), sent); err != nil {
			t.Errorf("settle after release = %v", err)
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
			t.Errorf("RA = %v", got)
		}
	})
}

// A cut ends each connection while the drivers run, and a closed
// server refuses a dial, as a closed port does.
func TestACutAndAClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, s := connected(t)
		if s.Clients() != 1 {
			t.Errorf("clients = %d", s.Clients())
		}
		s.Cut()
		synctest.Wait()
		if s.Clients() != 0 || len(s.Connected()) != 2 {
			t.Errorf("after the cut: clients = %d, connected = %v", s.Clients(), s.Connected())
		}
		s.Close()
		if _, err := s.DialContext(t.Context(), "tcp", "indiserver.test:7624"); err == nil {
			t.Error("a closed server took a dial")
		}
	})
}

func TestADriverOfNoSimulatorFails(t *testing.T) {
	if _, err := inditest.NewDriver("nonesuch"); err == nil {
		t.Error("a driver of no simulator")
	}
}

func TestTheFrameHeaderIsCards(t *testing.T) {
	header := string(inditest.FrameHeader())
	if !strings.HasPrefix(header, "SIMPLE  =") || !strings.Contains(header, "\nEND") {
		t.Errorf("header starts %q", header[:min(40, len(header))])
	}
}

func TestDefinitionsRefusesBrokenXML(t *testing.T) {
	if _, err := inditest.Definitions([]byte(`<defTextVector device="a" name="b"><defText`)); err == nil {
		t.Error("broken XML parsed")
	}
}
