package main

// The hold on an HDMI slot whose monitor stops answering for a moment.
// The unit tests run in a synctest bubble, so the settle time passes on
// the bubble's clock and the deadline timer fires on it.

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// litSlot is an HDMI slot whose monitor answers with an ELD, and
// darkSlot is the same slot while the graphics driver has cleared it.
func litSlot(t *testing.T) alsaEndpoint {
	t.Helper()
	block, err := parseELD(fixture(t, "eld-hdmi-lg-ultrawide.bin"))
	if err != nil {
		t.Fatal(err)
	}
	return alsaEndpoint{Card: 0, PCM: 3, HDMI: true, Monitor: true, ELD: block}
}

func darkSlot() alsaEndpoint {
	return alsaEndpoint{Card: 0, PCM: 3, HDMI: true}
}

// A slot whose ELD goes empty answers its last reading with a monitor
// until it has stayed empty for the settle time. A slot this process
// never read with a monitor answers empty at once, and so does an
// analog endpoint, which has no ELD to hold.
func TestASlotWhoseMonitorStopsAnsweringIsHeld(t *testing.T) {
	analog := alsaEndpoint{Card: 0, PCM: 0}
	cases := []struct {
		name    string
		before  []alsaEndpoint
		dark    alsaEndpoint
		after   time.Duration
		monitor bool
	}{
		{"a drop shorter than the settle time", []alsaEndpoint{litSlot(t)}, darkSlot(), monitorSettle - time.Millisecond, true},
		{"a drop as long as the settle time", []alsaEndpoint{litSlot(t)}, darkSlot(), monitorSettle, false},
		{"a slot never read with a monitor", nil, darkSlot(), 0, false},
		{"an analog endpoint", []alsaEndpoint{analog}, analog, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				hold := newMonitorHold(nil)
				for _, endpoint := range c.before {
					hold.apply([]alsaEndpoint{endpoint})
				}
				hold.apply([]alsaEndpoint{c.dark})
				time.Sleep(c.after)

				got := hold.apply([]alsaEndpoint{c.dark})[0]

				if got.Monitor != c.monitor {
					t.Fatalf("the slot reads monitor=%v, want %v", got.Monitor, c.monitor)
				}
				if c.monitor && !reflect.DeepEqual(got.ELD, litSlot(t).ELD) {
					t.Errorf("the held slot answers the ELD %+v, want the lit one", got.ELD)
				}
			})
		})
	}
}

// The end of a hold wakes a pass once, so a monitor that really left
// is reported at the deadline. A monitor that answers again inside the
// settle time wakes nothing extra.
func TestTheEndOfAMonitorHoldWakesOnePass(t *testing.T) {
	cases := []struct {
		name  string
		back  bool
		wakes int64
	}{
		{"a monitor that stays away", false, 1},
		{"a monitor that comes back", true, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var wakes atomic.Int64
				hold := newMonitorHold(func() { wakes.Add(1) })
				hold.apply([]alsaEndpoint{litSlot(t)})
				hold.apply([]alsaEndpoint{darkSlot()})
				hold.apply([]alsaEndpoint{darkSlot()})
				if c.back {
					hold.apply([]alsaEndpoint{litSlot(t)})
				}

				time.Sleep(2 * monitorSettle)
				synctest.Wait()

				if got := wakes.Load(); got != c.wakes {
					t.Errorf("the hold woke %d passes, want %d", got, c.wakes)
				}
			})
		})
	}
}

// A drop shorter than the settle time leaves the slice as the lit pass
// published it, so no claim on the slot is tainted and no pod waiting
// on a device wakes. It writes no PipeWire declaration either, so the
// channel layout the ELD gives stays and PipeWire does not restart. A
// monitor that stays away past the settle time is published with the
// no-monitor taint.
func TestTheSliceOfAShortDropIsTheSame(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pipewireConfigDir = filepath.Join(t.TempDir(), "pipewire.conf.d")
		t.Cleanup(func() { pipewireConfigDir = "/etc/pipewire/pipewire.conf.d" })
		api := &slicePublishFixture{}
		operator := testReconciler(t, api, staticGraph(outputGraph(
			map[pcmAddress]string{{Card: 0, PCM: 3}: sinkNodeName(0, 3)})), "pcmC0D3p")
		operator.monitors = newMonitorHold(nil)
		monitor := true
		operator.cardRead = func() ([]alsaEndpoint, error) {
			endpoints, err := readEndpoints()
			for i := range endpoints {
				if monitor {
					endpoints[i].HDMI, endpoints[i].Monitor, endpoints[i].ELD = true, true, litSlot(t).ELD
				} else {
					endpoints[i].HDMI, endpoints[i].Monitor, endpoints[i].ELD = true, false, eld{}
				}
			}
			return endpoints, err
		}
		pass := func() []SliceDevice {
			t.Helper()
			api.updated = nil
			if err := operator.reconcile(context.Background()); err != nil {
				t.Fatal(err)
			}
			if api.updated != nil {
				api.existing = api.updated
				return api.updated.Spec.Devices
			}
			return nil
		}
		pass()
		api.existing = api.created
		declared := declarations(t)

		monitor = false
		time.Sleep(monitorSettle / 2)
		if changed := pass(); changed != nil {
			t.Errorf("a drop shorter than the settle time published %+v", changed)
		}
		if got := declarations(t); !reflect.DeepEqual(got, declared) {
			t.Errorf("a drop shorter than the settle time declared\n%q\nwant\n%q", got, declared)
		}

		time.Sleep(monitorSettle)
		changed := pass()
		if len(changed) != 1 || !hasTaint(changed[0], noMonitorTaint) {
			t.Errorf("a monitor that stayed away published %+v, want the %s taint", changed, noMonitorTaint)
		}
	})
}

// declarations reads every file the operator wrote for PipeWire, by
// name.
func declarations(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(pipewireConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(pipewireConfigDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[entry.Name()] = string(raw)
	}
	return files
}

func hasTaint(device SliceDevice, key string) bool {
	for _, taint := range device.Taints {
		if taint.Key == key {
			return true
		}
	}
	return false
}
