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

// A burst of drops that no pass reads lit between is still a burst of
// short drops. An ELD change on the slot between two empty reads means
// the monitor answered and dropped again, so the hold starts again
// from the later read. A change on another slot does not.
func TestAnELDChangeBetweenEmptyReadsStartsTheHoldAgain(t *testing.T) {
	cases := []struct {
		name   string
		change controlEvent
		held   bool
	}{
		{"a change of the slot's ELD", controlEvent{Card: 0, Device: 3, Name: eldElementName}, true},
		{"a change of another slot's ELD", controlEvent{Card: 0, Device: 7, Name: eldElementName}, false},
		{"a change of another element", controlEvent{Card: 0, Device: 3, Name: "IEC958 Playback Switch"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				hold := newMonitorHold(nil)
				hold.apply([]alsaEndpoint{litSlot(t)})
				hold.apply([]alsaEndpoint{darkSlot()})
				time.Sleep(monitorSettle - time.Second)
				hold.heard(c.change)
				hold.apply([]alsaEndpoint{darkSlot()})
				time.Sleep(2 * time.Second)

				if got := hold.apply([]alsaEndpoint{darkSlot()})[0]; got.Monitor != c.held {
					t.Errorf("the slot reads monitor=%v, want %v", got.Monitor, c.held)
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

// slotPass runs reconcile passes over one HDMI slot whose ELD the test
// states, through one hold that lives as long as the operator does,
// and answers the devices a pass wrote, or nothing when the slice did
// not change.
type slotPass struct {
	t        *testing.T
	api      *slicePublishFixture
	operator *reconciler
	// block is the ELD the slot answers, and nil while no monitor
	// answers.
	block *eld
}

func newSlotPass(t *testing.T) *slotPass {
	t.Helper()
	pipewireConfigDir = filepath.Join(t.TempDir(), "pipewire.conf.d")
	t.Cleanup(func() { pipewireConfigDir = "/etc/pipewire/pipewire.conf.d" })
	api := &slicePublishFixture{}
	operator := testReconciler(t, api, staticGraph(outputGraph(
		map[pcmAddress]string{{Card: 0, PCM: 3}: sinkNodeName(0, 3)})), "pcmC0D3p")
	operator.monitors = newMonitorHold(nil)
	pass := &slotPass{t: t, api: api, operator: operator}
	operator.cardRead = func() ([]alsaEndpoint, error) {
		endpoints, err := readEndpoints()
		for i := range endpoints {
			endpoints[i].HDMI, endpoints[i].Monitor, endpoints[i].ELD = true, false, eld{}
			if pass.block != nil {
				endpoints[i].Monitor, endpoints[i].ELD = true, *pass.block
			}
		}
		return endpoints, err
	}
	return pass
}

func (p *slotPass) run() []SliceDevice {
	p.t.Helper()
	p.api.updated = nil
	if err := p.operator.reconcile(context.Background()); err != nil {
		p.t.Fatal(err)
	}
	if p.api.created != nil && p.api.existing == nil {
		p.api.existing = p.api.created
		return p.api.created.Spec.Devices
	}
	if p.api.updated != nil {
		p.api.existing = p.api.updated
		return p.api.updated.Spec.Devices
	}
	return nil
}

func eldFixture(t *testing.T, name string) *eld {
	t.Helper()
	block, err := parseELD(fixture(t, name))
	if err != nil {
		t.Fatal(err)
	}
	return &block
}

// A drop shorter than the settle time leaves the slice as the lit pass
// published it, read as the drop starts, while it lasts, and after the
// monitor answers again, so no claim on the slot is tainted and no pod
// waiting on a device wakes. It writes no PipeWire declaration either,
// so the channel layout the ELD gives stays and PipeWire does not
// restart. A monitor that stays away past the settle time is published
// with the no-monitor taint.
func TestTheSliceOfAShortDropIsTheSame(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		slot := newSlotPass(t)
		slot.block = eldFixture(t, "eld-hdmi-lg-ultrawide.bin")
		slot.run()
		declared := declarations(t)
		lit := slot.block

		slot.block = nil
		start := slot.run()
		time.Sleep(monitorSettle / 2)
		during := slot.run()
		slot.block = lit
		back := slot.run()
		for name, changed := range map[string][]SliceDevice{"as it starts": start, "while it lasts": during, "after it": back} {
			if changed != nil {
				t.Errorf("the slot %s published %+v", name, changed)
			}
		}
		if got := declarations(t); !reflect.DeepEqual(got, declared) {
			t.Errorf("a drop shorter than the settle time declared\n%q\nwant\n%q", got, declared)
		}

		slot.block = nil
		slot.run()
		time.Sleep(monitorSettle)
		changed := slot.run()
		if len(changed) != 1 || !hasTaint(changed[0], noMonitorTaint) {
			t.Errorf("a monitor that stayed away published %+v, want the %s taint", changed, noMonitorTaint)
		}
	})
}

// A different monitor that answers inside the hold is read as it is:
// the slot publishes the new monitor's facts, and never the held one's.
func TestADifferentMonitorInsideTheHoldIsPublished(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		slot := newSlotPass(t)
		slot.block = eldFixture(t, "eld-hdmi-lg-ultrawide.bin")
		lit := slot.run()

		slot.block = nil
		slot.run()
		slot.block = eldFixture(t, "eld-displayport-dell-u2415.bin")
		changed := slot.run()

		if len(changed) != 1 || reflect.DeepEqual(changed[0].Attributes, lit[0].Attributes) {
			t.Errorf("the slot published %+v for a different monitor, want the new monitor's facts", changed)
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
