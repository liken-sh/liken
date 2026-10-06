package indi

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
)

// settle waits for the reply to one sent change, within testTimeout.
func settle(t *testing.T, c *Client, sent Sent) (Property, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	return c.Settle(ctx, sent)
}

// connectedDevice replays a simulator's baseline, connects its device,
// and waits until the client has applied every definition that the
// device sent on connect.
func connectedDevice(t *testing.T, simulator string) (*Client, *replayServer) {
	t.Helper()
	c, server := connectedBaseline(t, simulator)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	events := c.Subscribe(ctx)
	if err := connectDevice(t, c, server.device); err != nil {
		t.Fatal(err)
	}
	awaitDefinitions(t, events, len(definedNames(transcript(t, simulator, "connect"))))
	// The updates after the last definition are still on their way.
	// A test that sends a change reads its answer from the updates
	// after the send, so each of these must arrive before it.
	synctest.Wait()
	return c, server
}

// values lists each member of a request as name=value.
func values(r request) []string {
	var out []string
	for _, m := range r.Members {
		out = append(out, m.Name+"="+strings.TrimSpace(m.Value))
	}
	return out
}

func TestSettleWaitsUntilBusyEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, server := connectedDevice(t, "dome")
		sent, err := c.SetNumbers(server.device, "ABS_DOME_POSITION", map[string]float64{"DOME_ABSOLUTE_POSITION": 30})
		if err != nil {
			t.Fatal(err)
		}
		p, err := settle(t, c, sent)
		if err != nil {
			t.Fatal(err)
		}
		position, _ := p.Member("DOME_ABSOLUTE_POSITION")
		if p.State != Ok || position.Number != 30 {
			t.Errorf("ABS_DOME_POSITION settled at %s %v, want Ok at 30", p.State, position.Number)
		}
	})
}

// The dome simulator reports its position with the state Ok on each
// poll, and a poll can arrive after a change was sent and before the
// dome starts to turn. That report does not carry the position sent,
// so it does not answer the change.
func TestSettleWaitsPastAReportOfTheOldValue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, server := connectedDevice(t, "dome")
		server.mute()
		sent, err := c.SetNumbers(server.device, "ABS_DOME_POSITION", map[string]float64{"DOME_ABSOLUTE_POSITION": 30})
		if err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() {
			_, err := settle(t, c, sent)
			result <- err
		}()
		server.await("ABS_DOME_POSITION")
		position := func(state, degrees string) string {
			return `<setNumberVector device="Dome Simulator" name="ABS_DOME_POSITION" state="` + state +
				`"><oneNumber name="DOME_ABSOLUTE_POSITION">` + degrees + `</oneNumber></setNumberVector>`
		}
		server.send(position("Ok", "0"))
		synctest.Wait()
		select {
		case err := <-result:
			t.Fatalf("Settle ended on the report of the old position: %v", err)
		default:
		}
		server.send(position("Ok", "30"))
		if err := <-result; err != nil {
			t.Errorf("Settle = %v, want the report of the new position", err)
		}
	})
}

func TestSettleAfterAnImmediateOk(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, server := connectedDevice(t, "focus")
		sent, err := c.SetNumbers(server.device, "ABS_FOCUS_POSITION", map[string]float64{"FOCUS_ABSOLUTE_POSITION": 52000})
		if err != nil {
			t.Fatal(err)
		}
		p, err := settle(t, c, sent)
		if err != nil {
			t.Fatal(err)
		}
		position, _ := p.Member("FOCUS_ABSOLUTE_POSITION")
		if p.State != Ok || position.Number != 52000 {
			t.Errorf("ABS_FOCUS_POSITION settled at %s %v, want Ok at 52000", p.State, position.Number)
		}
	})
}

func TestSettleReportsAnAlert(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, server := connectedDevice(t, "focus")
		sent, err := c.SetNumbers(server.device, "ABS_FOCUS_POSITION", map[string]float64{"FOCUS_ABSOLUTE_POSITION": 200000})
		if err != nil {
			t.Fatal(err)
		}
		_, err = settle(t, c, sent)
		var alert *AlertError
		if !errors.As(err, &alert) || alert.Property.State != Alert || err.Error() != "indi: Focuser Simulator.ABS_FOCUS_POSITION is Alert" {
			t.Errorf("Settle = %v, want an AlertError for ABS_FOCUS_POSITION", err)
		}
	})
}

func TestSettleFailsWhenTheConnectionEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, server := connectedDevice(t, "focus")
		// No transcript answers this position, so the property stays as
		// it was until the connection ends.
		sent, err := c.SetNumbers(server.device, "ABS_FOCUS_POSITION", map[string]float64{"FOCUS_ABSOLUTE_POSITION": 1000})
		if err != nil {
			t.Fatal(err)
		}
		server.drop()
		if _, err := settle(t, c, sent); !errors.Is(err, ErrDisconnected) {
			t.Errorf("Settle = %v, want ErrDisconnected", err)
		}
	})
}

func TestSettleFailsWhenThePropertyIsDeleted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, server := connectedDevice(t, "focus")
		sent, err := c.SetNumbers(server.device, "ABS_FOCUS_POSITION", map[string]float64{"FOCUS_ABSOLUTE_POSITION": 1000})
		if err != nil {
			t.Fatal(err)
		}
		// indiserver sends this for every device of a driver that exits.
		server.send(`<delProperty device="Focuser Simulator"/>`)
		if _, err := settle(t, c, sent); !errors.Is(err, ErrDeleted) {
			t.Errorf("Settle = %v, want ErrDeleted", err)
		}
	})
}

func TestSetNumbersSendsEveryMember(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, server := connectedBaseline(t, "telescope")
		if _, err := c.SetNumbers(server.device, "ALIGNMENT_POINT_MANDATORY_NUMBERS", map[string]float64{
			"ALIGNMENT_POINT_ENTRY_RA":  5.588,
			"ALIGNMENT_POINT_ENTRY_DEC": -5.39,
		}); err != nil {
			t.Fatal(err)
		}
		got := values(server.await("ALIGNMENT_POINT_MANDATORY_NUMBERS"))
		want := []string{
			"ALIGNMENT_POINT_ENTRY_OBSERVATION_JULIAN_DATE=0",
			"ALIGNMENT_POINT_ENTRY_RA=5.588",
			"ALIGNMENT_POINT_ENTRY_DEC=-5.39",
			"ALIGNMENT_POINT_ENTRY_VECTOR_X=0",
			"ALIGNMENT_POINT_ENTRY_VECTOR_Y=0",
			"ALIGNMENT_POINT_ENTRY_VECTOR_Z=0",
		}
		if !slices.Equal(got, want) {
			t.Errorf("sent %v, want %v", got, want)
		}
	})
}

func TestSetTextsSendsEveryMember(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, server := connectedBaseline(t, "ccd")
		p, _ := c.Property(server.device, "ACTIVE_DEVICES")
		if _, err := c.SetTexts(server.device, "ACTIVE_DEVICES", map[string]string{"ACTIVE_TELESCOPE": "Mount <east> & \"west\""}); err != nil {
			t.Fatal(err)
		}
		var want []string
		for _, m := range p.Members {
			value := m.Text
			if m.Name == "ACTIVE_TELESCOPE" {
				value = "Mount <east> & \"west\""
			}
			want = append(want, m.Name+"="+value)
		}
		if got := values(server.await("ACTIVE_DEVICES")); !slices.Equal(got, want) {
			t.Errorf("sent %v, want %v", got, want)
		}
	})
}

func TestSetSwitchesFollowsTheRule(t *testing.T) {
	cases := []struct {
		name, simulator, property string
		change                    map[string]bool
		want                      []string
	}{
		{"OneOfMany turns the others off", "focus", "CONNECTION", map[string]bool{"CONNECT": true}, []string{"CONNECT=On", "DISCONNECT=Off"}},
		{"AtMostOne turns the others off", "dome", "DOME_MOTION", map[string]bool{"DOME_CCW": true}, []string{"DOME_CW=Off", "DOME_CCW=On"}},
		{"AtMostOne allows none", "dome", "DOME_MOTION", map[string]bool{"DOME_CW": false}, []string{"DOME_CW=Off", "DOME_CCW=Off"}},
		{"AnyOfMany keeps the others", "dome", "DOME_SHUTTER_PARK_POLICY", map[string]bool{"SHUTTER_CLOSE_ON_PARK": true}, []string{"SHUTTER_CLOSE_ON_PARK=On", "SHUTTER_OPEN_ON_UNPARK=Off"}},
		{"AnyOfMany allows all", "dome", "DOME_SHUTTER_PARK_POLICY", map[string]bool{"SHUTTER_CLOSE_ON_PARK": true, "SHUTTER_OPEN_ON_UNPARK": true}, []string{"SHUTTER_CLOSE_ON_PARK=On", "SHUTTER_OPEN_ON_UNPARK=On"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c, server := connectedDevice(t, tc.simulator)
				if _, err := c.SetSwitches(server.device, tc.property, tc.change); err != nil {
					t.Fatal(err)
				}
				if got := values(server.await(tc.property)); !slices.Equal(got, tc.want) {
					t.Errorf("sent %v, want %v", got, tc.want)
				}
			})
		})
	}
}

func TestSetRefusesWhatTheDefinitionForbids(t *testing.T) {
	cases := []struct {
		name string
		send func(c *Client, device string) (Sent, error)
		want error
	}{
		{"two on in OneOfMany", func(c *Client, device string) (Sent, error) {
			return c.SetSwitches(device, "CONNECTION", map[string]bool{"CONNECT": true, "DISCONNECT": true})
		}, ErrInvalid},
		{"none on in OneOfMany", func(c *Client, device string) (Sent, error) {
			return c.SetSwitches(device, "CONNECTION", map[string]bool{"DISCONNECT": false})
		}, ErrInvalid},
		{"a read-only property", func(c *Client, device string) (Sent, error) {
			return c.SetTexts(device, "DRIVER_INFO", map[string]string{"DRIVER_NAME": "x"})
		}, ErrInvalid},
		{"the wrong type", func(c *Client, device string) (Sent, error) {
			return c.SetNumbers(device, "CONNECTION", map[string]float64{"CONNECT": 1})
		}, ErrInvalid},
		{"a member the property lacks", func(c *Client, device string) (Sent, error) {
			return c.SetNumbers(device, "POLLING_PERIOD", map[string]float64{"PERIOD_S": 1})
		}, ErrInvalid},
		{"a property the device lacks", func(c *Client, device string) (Sent, error) {
			return c.SetNumbers(device, "NO_SUCH_PROPERTY", map[string]float64{"X": 1})
		}, ErrNotDefined},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c, server := connectedBaseline(t, "telescope")
				if _, err := tc.send(c, server.device); !errors.Is(err, tc.want) {
					t.Errorf("err = %v, want %v", err, tc.want)
				}
			})
		})
	}
}

func TestSetRefusesWithNoConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := NewClient("127.0.0.1:1")
		if _, err := c.SetNumbers("Focuser Simulator", "ABS_FOCUS_POSITION", map[string]float64{"FOCUS_ABSOLUTE_POSITION": 1}); !errors.Is(err, ErrNotConnected) {
			t.Errorf("SetNumbers = %v, want ErrNotConnected", err)
		}
	})
}
