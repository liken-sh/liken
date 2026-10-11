package indi

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
)

// INDI::CCD writes the FILTER keyword from the slot and the names that
// it snoops from the device that ACTIVE_FILTER names. The CCD simulator
// names itself there, and indiserver routes a snooped property to the
// driver that sent it, so a camera with its own wheel names its filter
// in each frame.
func TestCameraFrameNamesTheFilterOfItsOwnWheel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, server := connectedDevice(t, "ccd")
		active, _ := c.Property(server.device, "ACTIVE_DEVICES")
		if filter, _ := active.Member("ACTIVE_FILTER"); filter.Text != server.device {
			t.Fatalf("ACTIVE_FILTER = %q, want the camera itself, %q", filter.Text, server.device)
		}
		if err := c.EnableBLOB(server.device, "CCD1", BLOBAlso); err != nil {
			t.Fatal(err)
		}
		// An 8 by 8 frame keeps the recorded BLOB small.
		mustSettle(t, c, server.device, "CCD_FRAME", map[string]float64{"X": 0, "Y": 0, "WIDTH": 8, "HEIGHT": 8})
		mustSettle(t, c, server.device, "FILTER_SLOT", map[string]float64{"FILTER_SLOT_VALUE": 2})

		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		events := c.Subscribe(ctx)
		if _, err := c.SetNumbers(server.device, "CCD_EXPOSURE", map[string]float64{"CCD_EXPOSURE_VALUE": 0.1}); err != nil {
			t.Fatal(err)
		}
		frame := next(t, events, func(e Event) bool { return len(e.BLOBs) > 0 })
		names, _ := c.Property(server.device, "FILTER_NAME")
		slot2, _ := names.Member("FILTER_SLOT_NAME_2")
		if got := fitsHeader(t, frame.BLOBs[0])["FILTER"]; got != slot2.Text {
			t.Errorf("FILTER = %q, want %q, the name of slot 2", got, slot2.Text)
		}
	})
}

func mustSettle(t *testing.T, c *Client, device, property string, values map[string]float64) {
	t.Helper()
	sent, err := c.SetNumbers(device, property, values)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := settle(t, c, sent); err != nil {
		t.Fatal(err)
	}
}

// fitsHeader reads the keywords of a FITS file's primary header: cards
// of 80 bytes, up to the END card. It keeps a string value's text
// without its quotes and padding, which is all this test compares.
func fitsHeader(t *testing.T, blob BLOB) map[string]string {
	t.Helper()
	if blob.Format != ".fits" {
		t.Fatalf("the BLOB's format is %q, want .fits", blob.Format)
	}
	header := map[string]string{}
	for card := blob.Data; len(card) >= 80; card = card[80:] {
		keyword := strings.TrimSpace(string(card[:8]))
		if keyword == "END" {
			return header
		}
		if string(card[8:10]) != "= " {
			continue
		}
		value, _, _ := strings.Cut(string(card[10:80]), "/")
		header[keyword] = strings.TrimSpace(strings.Trim(strings.TrimSpace(value), "'"))
	}
	t.Fatal("the FITS header has no END card")
	return nil
}
