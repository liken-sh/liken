package main

// The hold on an HDMI slot whose monitor stops answering for a moment.
// The unit tests run in a synctest bubble, so the settle time passes on
// the bubble's clock and the deadline timer fires on it.

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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

// A slot whose own ELD keeps changing starts its hold again only until
// it has been empty for monitorCeiling, so a monitor that left is
// reported within the ceiling and the settle time, as display-operator
// reports the screen.
func TestAMonitorHoldEndsByTheCeiling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hold := newMonitorHold(nil)
		hold.apply([]alsaEndpoint{litSlot(t)})
		hold.apply([]alsaEndpoint{darkSlot()})
		reported := time.Duration(0)
		for elapsed := 3 * time.Second; elapsed <= monitorCeiling+2*monitorSettle; elapsed += 3 * time.Second {
			time.Sleep(3 * time.Second)
			hold.heard(controlEvent{Card: 0, Device: 3, Name: eldElementName})
			if got := hold.apply([]alsaEndpoint{darkSlot()})[0]; !got.Monitor && reported == 0 {
				reported = elapsed
			}
		}

		if reported == 0 || reported > monitorCeiling+monitorSettle {
			t.Errorf("the slot read empty %v after the drop, want by %v", reported, monitorCeiling+monitorSettle)
		}
	})
}

// A slice device carries every fact of the ELD the operator publishes,
// so the next operator can rebuild the block from it and hold the slot
// as if it had read it.
func TestAPublishedDeviceGivesBackItsELD(t *testing.T) {
	for _, name := range []string{"eld-hdmi-lg-ultrawide.bin", "eld-displayport-dell-u2415.bin"} {
		t.Run(name, func(t *testing.T) {
			block := *eldFixture(t, name)
			device := sliceDevices(named(alsaEndpoint{Card: 0, PCM: 3, HDMI: true, Monitor: true, ELD: block}),
				nil, outputGraph(map[pcmAddress]string{{Card: 0, PCM: 3}: sinkNodeName(0, 3)}))[0]

			got, ok := publishedELD(device)

			if !ok || !reflect.DeepEqual(got, block) {
				t.Errorf("the device gives back %+v (%v), want %+v", got, ok, block)
			}
		})
	}
}

// The encoded formats are among those facts. A seed that lost them
// would read as a monitor that stopped accepting them, and the pass
// would declare the sink again without them.
func TestAPublishedDeviceGivesBackItsEncodedFormats(t *testing.T) {
	block, err := parseELD(receiverBlock(t, 2, 10, 12))
	if err != nil {
		t.Fatal(err)
	}
	device := sliceDevices(named(alsaEndpoint{Card: 0, PCM: 3, HDMI: true, Monitor: true, ELD: block}),
		nil, outputGraph(map[pcmAddress]string{{Card: 0, PCM: 3}: sinkNodeName(0, 3)}))[0]

	if got := device.Attributes[encodedFormatsAttribute]; got.String == nil || *got.String != "AC3 EAC3 TrueHD" {
		t.Errorf("%s = %+v, want AC3 EAC3 TrueHD", encodedFormatsAttribute, got)
	}
	got, ok := publishedELD(device)
	if !ok || got.EncodedTypes != block.EncodedTypes {
		t.Errorf("the device gives back the types %#x (%v), want %#x", got.EncodedTypes, ok, block.EncodedTypes)
	}
}

// Only a device the slice published with a monitor seeds a hold: a
// device tainted for no monitor, and a device with no ELD facts at
// all, seed nothing.
func TestTheSeedsAreTheDevicesPublishedWithAMonitor(t *testing.T) {
	graph := outputGraph(map[pcmAddress]string{{Card: 0, PCM: 3}: sinkNodeName(0, 3)})
	lit := sliceDevices(named(alsaEndpoint{Card: 0, PCM: 3, HDMI: true, Monitor: true,
		ELD: *eldFixture(t, "eld-hdmi-lg-ultrawide.bin")}), nil, graph)
	empty := sliceDevices(named(alsaEndpoint{Card: 0, PCM: 3, HDMI: true}), nil, graph)
	analog := sliceDevices(named(alsaEndpoint{Card: 0, PCM: 0}), nil, graph)
	cases := []struct {
		name    string
		devices []SliceDevice
		want    []string
	}{
		{"a device with a monitor", lit, []string{testSinkName}},
		{"a device with no monitor", empty, nil},
		{"an analog device", analog, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			seeds := monitorSeeds(&ResourceSlice{Spec: ResourceSliceSpec{Devices: c.devices}})
			if got := slices.Sorted(maps.Keys(seeds)); !slices.Equal(got, c.want) {
				t.Errorf("the seeds are %q, want %q", got, c.want)
			}
		})
	}
}

// An operator that starts while an HDMI slot is in a drop holds the
// slot from the monitor the previous operator published on it, so the
// slice stays as it was, and a monitor that stays away is published
// with the no-monitor taint at the end of the hold. The hold's clock
// starts at the first pass that reads the slot empty.
func TestAnOperatorThatStartsDuringADropHoldsThePublishedMonitor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		before := newSlotPass(t)
		before.block = eldFixture(t, "eld-hdmi-lg-ultrawide.bin")
		published := before.run()
		time.Sleep(time.Minute)

		slot := newSlotPass(t)
		slot.api.existing = before.api.created
		slot.operator.monitors.seed(monitorSeeds(before.api.created))
		if changed := slot.run(); changed != nil {
			t.Errorf("the first pass during a drop published %+v, want the slice as it was:\n%+v", changed, published)
		}

		time.Sleep(monitorSettle)
		changed := slot.run()
		if len(changed) != 1 || !hasTaint(changed[0], noMonitorTaint) {
			t.Errorf("a monitor that stayed away published %+v, want the %s taint", changed, noMonitorTaint)
		}
	})
}

// A seed describes the card as the previous operator left it, so it
// serves only the first read. A slot the first read did not hold, such
// as one whose card arrives later, reads as it is and is never lit by a
// monitor that left long ago.
func TestASeedServesOnlyTheFirstRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hold := newMonitorHold(nil)
		hold.seed(map[string]eld{testSinkName: litSlot(t).ELD})
		hold.apply(named(alsaEndpoint{Card: 0, PCM: 0}))
		time.Sleep(time.Hour)

		if got := hold.apply(named(darkSlot()))[0]; got.Monitor {
			t.Errorf("a slot that arrived after the first read answers the seed's monitor: %+v", got.ELD)
		}
	})
}

// The declaration's wait hears ELD changes for all of its time, however
// long the read of the Sinks before it took.
func TestTheDeclarationHearsChangesForTheWholeWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reads := 0
		read := func() ([]alsaEndpoint, error) {
			reads++
			if reads > 2 {
				return []alsaEndpoint{litSlot(t)}, nil
			}
			return []alsaEndpoint{darkSlot()}, nil
		}
		// The Sinks take 3 seconds to read, and the monitor answers 3
		// seconds into the wait after them.
		expect := func([]alsaEndpoint) func([]alsaEndpoint) bool {
			time.Sleep(3 * time.Second)
			return func(outputs []alsaEndpoint) bool { return !outputs[0].Monitor }
		}
		subscribe := func(ctx context.Context) <-chan controlEvent {
			changes := make(chan controlEvent)
			go func() {
				time.Sleep(6 * time.Second)
				// A subscription whose context ended has stopped reading
				// the card, and delivers nothing.
				if ctx.Err() != nil {
					return
				}
				select {
				case changes <- controlEvent{Card: 0, Device: 3, Name: eldElementName}:
				case <-ctx.Done():
				}
			}()
			return changes
		}

		outputs, err := declaredReading(read, expect, subscribe)

		if err != nil || !outputs[0].Monitor {
			t.Errorf("the declaration found monitor=%v (%v), want the monitor that answered", outputs[0].Monitor, err)
		}
	})
}

// The init container reads the card before PipeWire starts, and an
// empty slot gives a stereo layout. So when a slot that the previous
// operator published with a monitor reads empty, it waits for an ELD
// change, up to the settle time, and reads again. A slot nobody
// published with a monitor, which is most of a GPU's HDMI slots, is
// never waited for.
func TestTheDeclarationWaitsOnlyForAnExpectedMonitor(t *testing.T) {
	cases := []struct {
		name     string
		expected bool
		change   bool
		monitor  bool
		reads    int
		waited   time.Duration
	}{
		{"an expected monitor that answers", true, true, true, 2, 0},
		{"an expected monitor that stays away", true, false, false, 1, monitorSettle},
		{"a slot nobody published with a monitor", false, false, false, 1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				reads := 0
				read := func() ([]alsaEndpoint, error) {
					reads++
					if reads > 1 {
						return []alsaEndpoint{litSlot(t)}, nil
					}
					return []alsaEndpoint{darkSlot()}, nil
				}
				waitFor := func(outputs []alsaEndpoint) bool { return c.expected && !outputs[0].Monitor }
				changes := make(chan controlEvent, 1)
				if c.change {
					changes <- controlEvent{Card: 0, Device: 3, Name: eldElementName}
				}

				start := time.Now()
				outputs, err := awaitMonitors(read, waitFor, changes, monitorSettle)
				waited := time.Since(start)

				if err != nil || reads != c.reads || outputs[0].Monitor != c.monitor || waited != c.waited {
					t.Errorf("the declaration read %d times in %v and found monitor=%v (%v), want %d in %v and %v",
						reads, waited, outputs[0].Monitor, err, c.reads, c.waited, c.monitor)
				}
			})
		})
	}
}

// expectedEmpty answers the declaration's wait for one Sink: a slot
// whose Sink reports a monitor, and that reads empty now, is waited for.
func TestTheDeclarationExpectsTheMonitorsTheSinksReport(t *testing.T) {
	slot := named(darkSlot())[0]
	cases := []struct {
		name  string
		sinks map[string]Sink
		want  bool
	}{
		{"a Sink that reports a monitor", map[string]Sink{testSinkName: {Status: SinkStatus{EndpointStatus: EndpointStatus{Monitor: &EndpointMonitor{Display: "gsm-7716"}}}}}, true},
		{"a Sink with no monitor", map[string]Sink{testSinkName: {}}, false},
		{"no Sink", map[string]Sink{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := expectedEmpty(c.sinks)([]alsaEndpoint{slot}); got != c.want {
				t.Errorf("the declaration waits: %v, want %v", got, c.want)
			}
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
