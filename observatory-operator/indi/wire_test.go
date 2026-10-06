package indi

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

// subscribed replays the focuser's baseline to a client, and opens a
// subscription once the client has applied the whole baseline.
func subscribed(t *testing.T) (*Client, *replayServer, <-chan Event, <-chan error) {
	t.Helper()
	server := startReplay(t, "focus")
	c := server.newClient()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	t.Cleanup(cancel)
	baseline := c.Subscribe(ctx)
	done := run(t, c)
	awaitDefinitions(t, baseline, len(definedNames(transcript(t, "focus", "baseline"))))
	return c, server, c.Subscribe(ctx), done
}

// next returns the first event that matches, and fails the test when
// the subscription ends first.
func next(t *testing.T, events <-chan Event, match func(Event) bool) Event {
	t.Helper()
	for event := range events {
		if match(event) {
			return event
		}
	}
	t.Fatal("the subscription ended before the event arrived")
	return Event{}
}

func kind(k EventKind) func(Event) bool {
	return func(e Event) bool { return e.Kind == k }
}

func TestMessage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, server, events, _ := subscribed(t)
		server.send(`<message device="Focuser Simulator" timestamp="2026-10-05T19:53:26.5" message="[INFO] Focuser moved"/>`)
		got := next(t, events, kind(Message))
		want := time.Date(2026, 10, 5, 19, 53, 26, 500_000_000, time.UTC)
		if got.Device != "Focuser Simulator" || got.Message != "[INFO] Focuser moved" || !got.Timestamp.Equal(want) {
			t.Errorf("message event = %+v", got)
		}
	})
}

func TestVectorMessage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, server, events, _ := subscribed(t)
		server.send(`<setNumberVector device="Focuser Simulator" name="POLLING_PERIOD" state="Ok" message="polling slower">
	<oneNumber name="PERIOD_MS">1000</oneNumber></setNumberVector>`)
		got := next(t, events, kind(Updated))
		if got.Property != "POLLING_PERIOD" || got.Message != "polling slower" {
			t.Errorf("update event = %+v, want POLLING_PERIOD with its message", got)
		}
	})
}

func TestUpdateChangesOnlyWhatItCarries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, server, events, _ := subscribed(t)
		before, _ := c.Property(server.device, "POLLING_PERIOD")
		server.send(`<setNumberVector device="Focuser Simulator" name="POLLING_PERIOD">
	<oneNumber name="PERIOD_MS" max="900000">
	   500
	</oneNumber></setNumberVector>`)
		next(t, events, kind(Updated))
		after, _ := c.Property(server.device, "POLLING_PERIOD")
		want := before.clone()
		want.Members[0].Number = 500
		want.Members[0].Max = 900000
		if !equalProperty(after, want) {
			t.Errorf("after the update:\n%+v\nwant\n%+v", after, want)
		}
	})
}

func TestDeviceWideDeletion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, server, events, _ := subscribed(t)
		server.send(`<delProperty device="Focuser Simulator" timestamp="2026-10-05T19:53:26"/>`)
		got := next(t, events, kind(Deleted))
		if got.Device != "Focuser Simulator" || got.Property != "" {
			t.Errorf("deletion event = %+v, want the whole device", got)
		}
		if len(c.Properties(server.device)) != 0 || len(c.Devices()) != 0 {
			t.Errorf("the store still holds %v", c.Devices())
		}
	})
}

func TestOnePropertyDeletion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, server, events, _ := subscribed(t)
		server.send(`<delProperty device="Focuser Simulator" name="POLLING_PERIOD"/>`)
		got := next(t, events, kind(Deleted))
		if got.Property != "POLLING_PERIOD" {
			t.Errorf("deletion event = %+v", got)
		}
		if _, ok := c.Property(server.device, "POLLING_PERIOD"); ok {
			t.Error("POLLING_PERIOD is still in the store")
		}
		if _, ok := c.Property(server.device, "CONNECTION"); !ok {
			t.Error("CONNECTION left the store with POLLING_PERIOD")
		}
	})
}

func TestRedefinitionKeepsThePlace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, server, events, _ := subscribed(t)
		before := c.Properties(server.device)
		server.send(`<defNumberVector device="Focuser Simulator" name="POLLING_PERIOD" label="Polling" group="Options" state="Ok" perm="rw">
	<defNumber name="PERIOD_MS" label="Period (ms)" format="%.f" min="10" max="600000" step="1000">750</defNumber></defNumberVector>`)
		next(t, events, func(e Event) bool { return e.Kind == Defined && e.Property == "POLLING_PERIOD" })
		after := c.Properties(server.device)
		if len(after) != len(before) {
			t.Fatalf("%d properties after the redefinition, want %d", len(after), len(before))
		}
		for i := range before {
			if after[i].Name != before[i].Name {
				t.Errorf("property %d is %s, want %s", i, after[i].Name, before[i].Name)
			}
		}
		p, _ := c.Property(server.device, "POLLING_PERIOD")
		if m, _ := p.Member("PERIOD_MS"); m.Number != 750 || p.State != Ok {
			t.Errorf("POLLING_PERIOD = %+v, want the new definition", p)
		}
	})
}

func TestUpdateOfAnUndefinedPropertyIsIgnored(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, server, events, _ := subscribed(t)
		server.send(`<setNumberVector device="Focuser Simulator" name="NOT_DEFINED"><oneNumber name="X">1</oneNumber></setNumberVector>
	<message device="Focuser Simulator" message="after"/>`)
		got := next(t, events, func(e Event) bool { return e.Kind == Message || e.Property == "NOT_DEFINED" })
		if got.Kind != Message {
			t.Errorf("event = %+v, want the message that follows the ignored update", got)
		}
		if _, ok := c.Property(server.device, "NOT_DEFINED"); ok {
			t.Error("an update defined a property")
		}
	})
}

func TestInvalidVectorIsReportedAndSkipped(t *testing.T) {
	cases := []struct {
		name, data string
	}{
		{"a number that does not parse", `<setNumberVector device="Focuser Simulator" name="POLLING_PERIOD"><oneNumber name="PERIOD_MS">fast</oneNumber></setNumberVector>`},
		{"a switch that is neither On nor Off", `<setSwitchVector device="Focuser Simulator" name="CONNECTION"><oneSwitch name="CONNECT">Maybe</oneSwitch></setSwitchVector>`},
		{"an unknown state", `<setSwitchVector device="Focuser Simulator" name="CONNECTION" state="Fine"/>`},
		{"a member the property lacks", `<setNumberVector device="Focuser Simulator" name="POLLING_PERIOD"><oneNumber name="PERIOD_S">1</oneNumber></setNumberVector>`},
		{"a definition with no name", `<defTextVector device="Focuser Simulator" perm="ro"/>`},
		{"a definition with an unknown permission", `<defTextVector device="Focuser Simulator" name="X" perm="rx"/>`},
		{"a switch definition with an unknown rule", `<defSwitchVector device="Focuser Simulator" name="X" perm="rw" rule="SomeOfMany"/>`},
		{"a light that is not a state", `<defLightVector device="Focuser Simulator" name="X"><defLight name="L">Dim</defLight></defLightVector>`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c, server, events, _ := subscribed(t)
				before, _ := c.Property(server.device, "CONNECTION")
				server.send(tc.data + `<message device="Focuser Simulator" message="after"/>`)
				invalid := next(t, events, func(e Event) bool { return e.Kind == Invalid || e.Kind == Message })
				if invalid.Kind != Invalid || invalid.Err == nil {
					t.Errorf("event = %+v, want Invalid with an error", invalid)
				}
				next(t, events, kind(Message))
				after, _ := c.Property(server.device, "CONNECTION")
				if !equalProperty(before, after) {
					t.Errorf("CONNECTION changed from %+v to %+v", before, after)
				}
			})
		})
	}
}

func TestUnknownElementsAreSkipped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, server, events, _ := subscribed(t)
		// indiserver passes another client's new*Vector to drivers only,
		// but a chained server or a future protocol can send elements this
		// client does not read.
		server.send(`<newSwitchVector device="Focuser Simulator" name="CONNECTION"><oneSwitch name="CONNECT">On</oneSwitch></newSwitchVector>
	<getProperties version="1.7"/><futureThing a="b"><nested/></futureThing>
	<message device="Focuser Simulator" message="after"/>`)
		if got := next(t, events, func(e Event) bool { return e.Kind != Message || e.Message == "after" }); got.Kind != Message {
			t.Errorf("event = %+v, want only the message", got)
		}
	})
}

func TestInvalidUTF8IsReplaced(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, server, events, _ := subscribed(t)
		server.send("<message device=\"Focuser Simulator\" message=\"25\xb0C\"/>")
		got := next(t, events, kind(Message))
		if got.Message != "25�C" {
			t.Errorf("message = %q, want the invalid byte replaced", got.Message)
		}
	})
}

func TestBrokenXMLEndsTheConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, server, _, done := subscribed(t)
		server.send(`<setNumberVector device="Focuser Simulator" name="POLLING_PERIOD"></setTextVector>`)
		select {
		case err := <-done:
			if err == nil {
				t.Error("Run returned no error for a broken stream")
			}
		case <-time.After(testTimeout):
			t.Fatal("Run did not end on a broken stream")
		}
		if c.Connected() {
			t.Error("the client reports a connection after the stream broke")
		}
	})
}

func TestPingRequestIsAnswered(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, server, _, _ := subscribed(t)
		server.send(`<pingRequest uid="ping-1"/>`)
		if r := server.awaitElement("pingReply", 1); r.UID != "ping-1" {
			t.Errorf("pingReply uid = %q, want ping-1", r.UID)
		}
	})
}

const blobUpdate = `<setBLOBVector device="Telescope Simulator" name="ALIGNMENT_POINT_OPTIONAL_BINARY_BLOB" state="Ok">
<oneBLOB name="ALIGNMENT_POINT_ENTRY_PRIVATE" size="5" format=".bin" enclen="8">
aGVs
bG8=
</oneBLOB></setBLOBVector>`

func TestBLOBDataGoesToSubscribersOnly(t *testing.T) {
	for _, property := range []string{"", "ALIGNMENT_POINT_OPTIONAL_BINARY_BLOB"} {
		t.Run("enableBLOB for "+property, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c, server := connectedBaseline(t, "telescope")
				ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
				defer cancel()
				events := c.Subscribe(ctx)
				if err := c.EnableBLOB(server.device, property, BLOBAlso); err != nil {
					t.Fatal(err)
				}
				if r := server.awaitElement("enableBLOB", 1); r.Name != property || r.Text != "Also" {
					t.Errorf("enableBLOB = %+v, want %q with Also", r, property)
				}
				server.send(blobUpdate)
				got := next(t, events, kind(Updated))
				if len(got.BLOBs) != 1 || got.BLOBs[0].Member != "ALIGNMENT_POINT_ENTRY_PRIVATE" ||
					got.BLOBs[0].Format != ".bin" || got.BLOBs[0].Size != 5 || string(got.BLOBs[0].Data) != "hello" {
					t.Errorf("BLOBs = %+v, want the 5 bytes of hello as .bin", got.BLOBs)
				}
				p, _ := c.Property(server.device, "ALIGNMENT_POINT_OPTIONAL_BINARY_BLOB")
				m, _ := p.Member("ALIGNMENT_POINT_ENTRY_PRIVATE")
				if m.BLOBFormat != ".bin" || m.BLOBSize != 5 || p.State != Ok {
					t.Errorf("the store holds %+v, want the format and size of the BLOB", p)
				}
			})
		})
	}
}

func TestBLOBDataThatNobodyAskedForIsDropped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, server := connectedBaseline(t, "telescope")
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		events := c.Subscribe(ctx)
		server.send(blobUpdate)
		got := next(t, events, kind(Updated))
		if len(got.BLOBs) != 0 {
			t.Errorf("BLOBs = %+v, want none, because no caller enabled them", got.BLOBs)
		}
	})
}

func TestEnableBLOBFollowsGetPropertiesOnEachConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := startReplay(t, "focus")
		c := server.newClient()
		if err := c.EnableBLOB(server.device, "", BLOBNever); err != nil {
			t.Fatal(err)
		}
		if err := c.EnableBLOB(server.device, "", BLOBOnly); err != nil {
			t.Fatal(err)
		}
		done := run(t, c)
		waitFor(t, c, "the baseline", defined(server.device, "CONNECTION"))
		server.drop()
		<-done
		run(t, c)
		waitFor(t, c, "the new baseline", defined(server.device, "CONNECTION"))
		server.awaitElement("enableBLOB", 2)

		var got []string
		for _, r := range server.received() {
			got = append(got, r.XMLName.Local+" "+r.Device+" "+r.Text)
		}
		want := []string{
			"getProperties  ", "enableBLOB Focuser Simulator Only",
			"getProperties  ", "enableBLOB Focuser Simulator Only",
		}
		if !slices.Equal(got, want) {
			t.Errorf("the server read %q, want %q", got, want)
		}
	})
}

func TestEnableBLOBRefusesAnUnknownMode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := NewClient("127.0.0.1:1")
		if err := c.EnableBLOB("CCD Simulator", "", BLOBMode("Sometimes")); !errors.Is(err, ErrInvalid) {
			t.Errorf("EnableBLOB = %v, want ErrInvalid", err)
		}
	})
}

func TestEventKindNames(t *testing.T) {
	cases := map[EventKind]string{
		Connected: "Connected", Disconnected: "Disconnected", Defined: "Defined",
		Updated: "Updated", Deleted: "Deleted", Message: "Message", Invalid: "Invalid",
		EventKind(0): "EventKind(?)",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("EventKind(%d) = %q, want %q", int(k), got, want)
		}
	}
}
