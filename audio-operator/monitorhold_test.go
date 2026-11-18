package main

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

func endpointWithMonitor(t *testing.T) alsaEndpoint {
	t.Helper()
	block, err := parseELD(fixture(t, "eld-hdmi-lg-ultrawide.bin"))
	if err != nil {
		t.Fatalf("parsing the ELD fixture: %v", err)
	}
	return alsaEndpoint{Card: 0, PCM: 3, HDMI: true, Monitor: true, ELD: block}
}

// endpointWithoutMonitor is the same PCM device as endpointWithMonitor
// while the graphics driver has emptied its ELD.
func endpointWithoutMonitor() alsaEndpoint {
	return alsaEndpoint{Card: 0, PCM: 3, HDMI: true}
}

// An HDMI output whose ELD goes empty reads as its last reading with a
// monitor until the ELD has been empty for monitorSettle. An output
// that this process never read with a monitor reads empty at once, and
// so does an analog endpoint, which has no ELD.
func TestAnEmptyELDReadsAsTheLastMonitorUntilTheSettleTime(t *testing.T) {
	analog := alsaEndpoint{Card: 0, PCM: 0}
	cases := []struct {
		name    string
		before  []alsaEndpoint
		empty   alsaEndpoint
		after   time.Duration
		monitor bool
	}{
		{"a drop shorter than the settle time", []alsaEndpoint{endpointWithMonitor(t)}, endpointWithoutMonitor(), monitorSettle - time.Millisecond, true},
		{"a drop as long as the settle time", []alsaEndpoint{endpointWithMonitor(t)}, endpointWithoutMonitor(), monitorSettle, false},
		{"an output never read with a monitor", nil, endpointWithoutMonitor(), 0, false},
		{"an analog endpoint", []alsaEndpoint{analog}, analog, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				hold := newMonitorHold(nil)
				for _, endpoint := range c.before {
					hold.apply([]alsaEndpoint{endpoint})
				}
				hold.apply([]alsaEndpoint{c.empty})
				time.Sleep(c.after)

				got := hold.apply([]alsaEndpoint{c.empty})[0]

				if got.Monitor != c.monitor {
					t.Fatalf("the output reads monitor=%v, want %v", got.Monitor, c.monitor)
				}
				if c.monitor && !reflect.DeepEqual(got.ELD, endpointWithMonitor(t).ELD) {
					t.Errorf("the output reads the ELD %+v, want the monitor's ELD", got.ELD)
				}
			})
		})
	}
}

// The monitor can come back and drop again between two passes, so that
// no pass reads it. The ELD change event between the two empty reads
// shows this, so the settle time starts again from the later read. A
// change on another output, or of another element, does not.
func TestAnELDChangeBetweenEmptyReadsStartsTheSettleTimeAgain(t *testing.T) {
	cases := []struct {
		name   string
		change controlEvent
		held   bool
	}{
		{"a change of the output's ELD", controlEvent{Card: 0, Device: 3, Name: eldElementName}, true},
		{"a change of another output's ELD", controlEvent{Card: 0, Device: 7, Name: eldElementName}, false},
		{"a change of another element", controlEvent{Card: 0, Device: 3, Name: "IEC958 Playback Switch"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				hold := newMonitorHold(nil)
				hold.apply([]alsaEndpoint{endpointWithMonitor(t)})
				hold.apply([]alsaEndpoint{endpointWithoutMonitor()})
				time.Sleep(monitorSettle - time.Second)
				hold.countELDChange(c.change)
				hold.apply([]alsaEndpoint{endpointWithoutMonitor()})
				time.Sleep(2 * time.Second)

				if got := hold.apply([]alsaEndpoint{endpointWithoutMonitor()})[0]; got.Monitor != c.held {
					t.Errorf("the output reads monitor=%v, want %v", got.Monitor, c.held)
				}
			})
		})
	}
}

// ELD changes start the settle time again only until monitorCeiling
// after the first empty read. So an output whose ELD keeps changing
// reads empty by monitorCeiling plus monitorSettle, the same bound that
// display-operator has for the screen.
func TestELDChangesStartTheSettleTimeAgainOnlyUntilTheCeiling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hold := newMonitorHold(nil)
		hold.apply([]alsaEndpoint{endpointWithMonitor(t)})
		hold.apply([]alsaEndpoint{endpointWithoutMonitor()})
		reported := time.Duration(0)
		for elapsed := 3 * time.Second; elapsed <= monitorCeiling+2*monitorSettle; elapsed += 3 * time.Second {
			time.Sleep(3 * time.Second)
			hold.countELDChange(controlEvent{Card: 0, Device: 3, Name: eldElementName})
			if got := hold.apply([]alsaEndpoint{endpointWithoutMonitor()})[0]; !got.Monitor && reported == 0 {
				reported = elapsed
			}
		}

		if reported == 0 || reported > monitorCeiling+monitorSettle {
			t.Errorf("the output read empty %v after the drop, want by %v", reported, monitorCeiling+monitorSettle)
		}
	})
}

// A published device has an attribute for each field of its ELD, so
// the next operator process rebuilds the same ELD from it.
func TestAPublishedDeviceRebuildsItsELD(t *testing.T) {
	for _, name := range []string{"eld-hdmi-lg-ultrawide.bin", "eld-displayport-dell-u2415.bin"} {
		t.Run(name, func(t *testing.T) {
			block := *eldFixture(t, name)
			device := sliceDevices(named(alsaEndpoint{Card: 0, PCM: 3, HDMI: true, Monitor: true, ELD: block}),
				nil, outputGraph(map[pcmAddress]string{{Card: 0, PCM: 3}: sinkNodeName(0, 3)}))[0]

			got, ok := publishedELD(device)

			if !ok || !reflect.DeepEqual(got, block) {
				t.Errorf("the device rebuilds %+v (%v), want %+v", got, ok, block)
			}
		})
	}
}

// A rebuilt ELD without the encoded formats would read as a monitor
// that stopped accepting them, and the pass would declare the sink
// again without them.
func TestAPublishedDeviceRebuildsItsEncodedFormats(t *testing.T) {
	block, err := parseELD(receiverBlock(t, 2, 10, 12))
	if err != nil {
		t.Fatalf("parsing the receiver's ELD: %v", err)
	}
	device := sliceDevices(named(alsaEndpoint{Card: 0, PCM: 3, HDMI: true, Monitor: true, ELD: block}),
		nil, outputGraph(map[pcmAddress]string{{Card: 0, PCM: 3}: sinkNodeName(0, 3)}))[0]

	if got := device.Attributes[encodedFormatsAttribute]; got.String == nil || *got.String != "AC3 EAC3 TrueHD" {
		t.Errorf("%s = %+v, want AC3 EAC3 TrueHD", encodedFormatsAttribute, got)
	}
	got, ok := publishedELD(device)
	if !ok || got.EncodedTypes != block.EncodedTypes {
		t.Errorf("the device rebuilds the types %#x (%v), want %#x", got.EncodedTypes, ok, block.EncodedTypes)
	}
}

// sliceMonitorELDs returns no ELD for a device with the no-monitor
// taint or for a device with no ELD attributes.
func TestOnlyADevicePublishedWithAMonitorGivesAnELD(t *testing.T) {
	graph := outputGraph(map[pcmAddress]string{{Card: 0, PCM: 3}: sinkNodeName(0, 3)})
	withMonitor := sliceDevices(named(alsaEndpoint{Card: 0, PCM: 3, HDMI: true, Monitor: true,
		ELD: *eldFixture(t, "eld-hdmi-lg-ultrawide.bin")}), nil, graph)
	empty := sliceDevices(named(alsaEndpoint{Card: 0, PCM: 3, HDMI: true}), nil, graph)
	analog := sliceDevices(named(alsaEndpoint{Card: 0, PCM: 0}), nil, graph)
	cases := []struct {
		name    string
		devices []SliceDevice
		want    []string
	}{
		{"a device with a monitor", withMonitor, []string{testSinkName}},
		{"a device with no monitor", empty, nil},
		{"an analog device", analog, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			elds := sliceMonitorELDs(&ResourceSlice{Spec: ResourceSliceSpec{Devices: c.devices}})
			if got := slices.Sorted(maps.Keys(elds)); !slices.Equal(got, c.want) {
				t.Errorf("the ELDs are for %q, want %q", got, c.want)
			}
		})
	}
}

// An operator that starts during a drop on an HDMI output publishes the
// monitor that the previous operator published, so the slice stays as
// it was. A monitor that stays away is published with the no-monitor
// taint when monitorSettle has passed since the first pass that read
// the output empty.
func TestAnOperatorThatStartsDuringADropKeepsThePublishedMonitor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		before := newOutputPass(t)
		before.block = eldFixture(t, "eld-hdmi-lg-ultrawide.bin")
		published := before.run()
		time.Sleep(time.Minute)

		pass := newOutputPass(t)
		pass.api.existing = before.api.created
		pass.operator.monitors.setPublishedELDs(sliceMonitorELDs(before.api.created))
		if changed := pass.run(); changed != nil {
			t.Errorf("the first pass during a drop published %+v, want the slice as it was:\n%+v", changed, published)
		}

		time.Sleep(monitorSettle)
		changed := pass.run()
		if len(changed) != 1 || !hasTaint(changed[0], noMonitorTaint) {
			t.Errorf("a monitor that stayed away published %+v, want the %s taint", changed, noMonitorTaint)
		}
	})
}

// A published ELD describes the card when the previous operator
// stopped, so apply uses it on the first read only. An output that the
// first read did not include, such as one on a card that arrives
// later, reads as it is.
func TestAPublishedELDServesOnlyTheFirstRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hold := newMonitorHold(nil)
		hold.setPublishedELDs(map[string]eld{testSinkName: endpointWithMonitor(t).ELD})
		hold.apply(named(alsaEndpoint{Card: 0, PCM: 0}))
		time.Sleep(time.Hour)

		if got := hold.apply(named(endpointWithoutMonitor()))[0]; got.Monitor {
			t.Errorf("an output that arrived after the first read reads the published monitor %+v, want no monitor", got.ELD)
		}
	})
}

// readOutputsForDeclaration receives ELD changes for the whole wait,
// however long the read of the Sinks before it takes.
func TestTheDeclarationReceivesChangesForTheWholeWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reads := 0
		read := func() ([]alsaEndpoint, error) {
			reads++
			if reads > 2 {
				return []alsaEndpoint{endpointWithMonitor(t)}, nil
			}
			return []alsaEndpoint{endpointWithoutMonitor()}, nil
		}
		// The Sinks take 3 seconds to read, and the monitor comes back 3
		// seconds into the wait after them.
		readMissing := func([]alsaEndpoint) func([]alsaEndpoint) bool {
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

		outputs, err := readOutputsForDeclaration(read, readMissing, subscribe)

		if err != nil || !outputs[0].Monitor {
			t.Errorf("the declaration found monitor=%v (%v), want the monitor that came back", outputs[0].Monitor, err)
		}
	})
}

// While missing returns true, awaitMonitors waits up to monitorSettle
// for an ELD change and reads the card again. An output with no
// expected monitor, such as an unused HDMI output of a GPU, is not
// waited for.
func TestTheDeclarationWaitsOnlyForAnExpectedMonitor(t *testing.T) {
	cases := []struct {
		name     string
		expected bool
		change   bool
		monitor  bool
		reads    int
		waited   time.Duration
	}{
		{"an expected monitor that comes back", true, true, true, 2, 0},
		{"an expected monitor that stays away", true, false, false, 1, monitorSettle},
		{"an output with no expected monitor", false, false, false, 1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				reads := 0
				read := func() ([]alsaEndpoint, error) {
					reads++
					if reads > 1 {
						return []alsaEndpoint{endpointWithMonitor(t)}, nil
					}
					return []alsaEndpoint{endpointWithoutMonitor()}, nil
				}
				missing := func(outputs []alsaEndpoint) bool { return c.expected && !outputs[0].Monitor }
				changes := make(chan controlEvent, 1)
				if c.change {
					changes <- controlEvent{Card: 0, Device: 3, Name: eldElementName}
				}

				start := time.Now()
				outputs, err := awaitMonitors(read, missing, changes, monitorSettle)
				waited := time.Since(start)

				if err != nil || reads != c.reads || outputs[0].Monitor != c.monitor || waited != c.waited {
					t.Errorf("the declaration read %d times in %v and found monitor=%v (%v), want %d in %v and %v",
						reads, waited, outputs[0].Monitor, err, c.reads, c.waited, c.monitor)
				}
			})
		})
	}
}

func TestAMonitorIsMissingOnlyWhenItsSinkReportsOne(t *testing.T) {
	output := named(endpointWithoutMonitor())[0]
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
			if got := missingSinkMonitor(c.sinks)([]alsaEndpoint{output}); got != c.want {
				t.Errorf("missingSinkMonitor = %v, want %v", got, c.want)
			}
		})
	}
}

// When monitorSettle ends for an output that still reads empty, the
// hold wakes one pass, so a monitor that left is reported then. A
// monitor that comes back sooner wakes no pass.
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
				hold.apply([]alsaEndpoint{endpointWithMonitor(t)})
				hold.apply([]alsaEndpoint{endpointWithoutMonitor()})
				hold.apply([]alsaEndpoint{endpointWithoutMonitor()})
				if c.back {
					hold.apply([]alsaEndpoint{endpointWithMonitor(t)})
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

// outputPass runs reconcile passes over one HDMI output whose ELD the
// test sets. run returns the devices that a pass wrote, or nil when
// the slice did not change.
type outputPass struct {
	t        *testing.T
	api      *slicePublishFixture
	operator *reconciler
	// block is the ELD that the output reads, and nil while no monitor
	// is connected.
	block *eld
}

func newOutputPass(t *testing.T) *outputPass {
	t.Helper()
	pipewireConfigDir = filepath.Join(t.TempDir(), "pipewire.conf.d")
	t.Cleanup(func() { pipewireConfigDir = "/etc/pipewire/pipewire.conf.d" })
	api := &slicePublishFixture{}
	operator := testReconciler(t, api, staticGraph(outputGraph(
		map[pcmAddress]string{{Card: 0, PCM: 3}: sinkNodeName(0, 3)})), "pcmC0D3p")
	operator.monitors = newMonitorHold(nil)
	pass := &outputPass{t: t, api: api, operator: operator}
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

func (p *outputPass) run() []SliceDevice {
	p.t.Helper()
	p.api.updated = nil
	if err := p.operator.reconcile(context.Background()); err != nil {
		p.t.Fatalf("reconciling the output: %v", err)
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
		t.Fatalf("parsing the ELD fixture %s: %v", name, err)
	}
	return &block
}

// A pass during a drop shorter than monitorSettle, or after the
// monitor comes back, writes no slice, so no claim on the output is
// tainted and no pending pod wakes. It writes no PipeWire drop-in
// either, so the channel layout stays and PipeWire does not restart. A
// monitor that stays away for monitorSettle is published with the
// no-monitor taint.
func TestAShortDropChangesNeitherTheSliceNorTheLayout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pass := newOutputPass(t)
		pass.block = eldFixture(t, "eld-hdmi-lg-ultrawide.bin")
		pass.run()
		declared := declarations(t)
		withMonitor := pass.block

		pass.block = nil
		start := pass.run()
		time.Sleep(monitorSettle / 2)
		during := pass.run()
		pass.block = withMonitor
		back := pass.run()
		for name, changed := range map[string][]SliceDevice{"as it starts": start, "while it lasts": during, "after it": back} {
			if changed != nil {
				t.Errorf("the output %s published %+v, want no change", name, changed)
			}
		}
		if got := declarations(t); !reflect.DeepEqual(got, declared) {
			t.Errorf("a drop shorter than the settle time declared\n%q\nwant\n%q", got, declared)
		}

		pass.block = nil
		pass.run()
		time.Sleep(monitorSettle)
		changed := pass.run()
		if len(changed) != 1 || !hasTaint(changed[0], noMonitorTaint) {
			t.Errorf("a monitor that stayed away published %+v, want the %s taint", changed, noMonitorTaint)
		}
	})
}

// A different monitor that appears during a drop is published at once,
// with its own ELD.
func TestADifferentMonitorDuringADropIsPublished(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pass := newOutputPass(t)
		pass.block = eldFixture(t, "eld-hdmi-lg-ultrawide.bin")
		published := pass.run()

		pass.block = nil
		pass.run()
		pass.block = eldFixture(t, "eld-displayport-dell-u2415.bin")
		changed := pass.run()

		if len(changed) != 1 || reflect.DeepEqual(changed[0].Attributes, published[0].Attributes) {
			t.Errorf("the output published %+v for a different monitor, want the new monitor's ELD", changed)
		}
	})
}

// declarations returns the content of each PipeWire drop-in, by file
// name.
func declarations(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(pipewireConfigDir)
	if err != nil {
		t.Fatalf("listing the PipeWire drop-ins: %v", err)
	}
	files := map[string]string{}
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(pipewireConfigDir, entry.Name()))
		if err != nil {
			t.Fatalf("reading a PipeWire drop-in: %v", err)
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
