package main

// Reading PipeWire's graph, and waiting for the container beside this
// one to serve it.
//
// PipeWire and WirePlumber run in their own containers of this pod,
// started before the operator and stopped after it, so nothing here
// starts or supervises a daemon. The kubelet does both.
//
// The operator needs one fact from PipeWire that ALSA cannot give it:
// the node name of the sink that plays through each PCM device. That
// name is the value a consumer's PIPEWIRE_NODE holds, and WirePlumber
// builds it from the card and the profile, so the name exists only in
// the running graph.
//
// A Bluetooth speaker's sink node is in the same graph, built by
// WirePlumber's bluez monitor, so one read yields both kinds.
//
// The operator reads the graph by running pw-dump, which ships with
// PipeWire in the same image, and parsing the JSON it prints. The
// alternative is to speak PipeWire's native protocol, which means
// implementing its binary POD encoding for every message the graph
// walk needs. The exec costs one process for each reconcile pass, at
// most one every settle window, and it reads a format that the
// PipeWire release in this image defines.
//
// The writes take the same path for the same reason: pw-cli
// set-param is one process per write, changing one property on an
// object the same dump named.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
)

// pwDumpTimeout bounds one graph read. pw-dump connects to the
// socket, waits for the whole graph, and prints it, so a PipeWire
// that stops answering would otherwise hold the reconcile pass
// forever.
const pwDumpTimeout = 10 * time.Second

// pwCLITimeout bounds one property write.
//
// A set-param connects to the same socket, writes one pod, and
// exits. The bound guards the same failure the dump's bound
// guards: a socket that accepts the connection and then answers
// nothing.
const pwCLITimeout = 10 * time.Second

// runtimeDir is where PipeWire creates its socket. It is a hostPath
// mount, so that a consumer's CDI spec can bind the same directory
// into a container on the same node.
//
// The PipeWire container and the operator container both mount it,
// so the socket the operator reads is the socket a consumer gets.
const runtimeDir = "/var/run/audio.liken.sh"

// socketPath is the absolute path of the socket a client connects to.
// A PIPEWIRE_REMOTE that starts with a slash is used as a path, and
// the runtime directory is not consulted, so one absolute path is the
// whole of what a consumer needs.
const socketPath = runtimeDir + "/pipewire-0"

// pipewireReadyTimeout bounds the wait for PipeWire to answer at
// startup.
//
// A PipeWire that never answers within this window is a container
// crashlooping beside this one. The operator publishes every output
// tainted and exits, so the slice says nothing plays, and the
// kubelet's restart of this container is the retry.
const pipewireReadyTimeout = 60 * time.Second

// pipewireReadyInterval is how often the startup wait asks again.
// PipeWire raises no event that says it is ready, and the operator
// has no connection to it until it is, so this one wait polls. Every
// later read is driven by an event.
const pipewireReadyInterval = time.Second

// nodeReadyTimeout bounds the wait for the declared sink nodes to
// appear in the graph. PipeWire creates the objects its configuration
// declares while it loads that configuration, which is before it
// serves the socket, so the nodes are there on the first read or they
// are not coming.
const nodeReadyTimeout = 15 * time.Second

// pcmAddress names one playback PCM device, which is what ties a
// PipeWire sink to the ALSA output the operator publishes.
type pcmAddress struct {
	Card int
	PCM  int
}

// pwGraph is what one pw-dump read yields: the sink node of each
// PCM device, and the sink node of each Bluetooth speaker, keyed by
// its peer MAC in the lowercase colon form.
type pwGraph struct {
	Outputs  map[pcmAddress]string
	Speakers map[string]bluezSink
}

// bluezSink is one Bluetooth sink node: the name a consumer's
// PIPEWIRE_NODE holds, the codec the transport negotiated, and what
// a codec switch needs to write.
//
// The name is all a delivery carries, but a codec switch needs
// the rest: the device id to write the codec on, the node id and
// channel count for the unity write, and the offered set to
// validate a claim against.
//
// Device is the bluez5 Device object's id, not the node's,
// because the codec choice is the device's property: the node
// dies in a switch and the device survives it.
type bluezSink struct {
	Node    string
	NodeID  int
	Codec   string
	Volumes []float64
	Device  int
	Codecs  []bluezCodec
}

// bluez5Device is the part of a bluez5 Device object this operator
// reads: its own object id, and the codecs it offers. The name says
// bluez5 because bluez_test.go's bluezDevice is the other thing with
// that name, the D-Bus object bluetoothd publishes.
//
// The device object is read separately from the node and joined
// to it by peer address, because PipeWire publishes them as two
// objects and the address is the one property both carry.
type bluez5Device struct {
	ID     int
	Codecs []bluezCodec
}

// The node properties the bluez5 SPA plugin sets on every node it
// emits (spa/plugins/bluez5/bluez5-device.c in the pipewire this
// image ships). The address is BlueZ's own uppercase colon form.
const (
	bluezAddressProperty = "api.bluez5.address"
	bluezCodecProperty   = "api.bluez5.codec"
)

// The property keys that hold a sink's ALSA address, in the order
// this operator reads them.
//
// The first key of each list is the one the operator writes itself,
// which is the only pair it can rely on. It declares every sink node
// in a configuration drop-in (see nodes.go), and a declared node has
// no alsa.card and no alsa.device, because those two come from the
// udev device that WirePlumber's ALSA monitor builds and this graph
// has no monitor in it.
//
// The rest are kept so that a sink from any other source still maps.
// alsa.card and alsa.device are what the card profile code puts on a
// node it creates from a profile, and the api.alsa.pcm pair is what
// the ALSA plugin puts on a node that opens a PCM device directly, as
// the Pro Audio profile does.
var (
	sinkCardKeys = []string{nodeCardProperty, "alsa.card", "api.alsa.pcm.card", "api.alsa.card"}
	sinkPCMKeys  = []string{nodePCMProperty, "alsa.device", "api.alsa.pcm.device"}
)

// pwObject is the part of pw-dump's output this operator reads. Every
// object in the dump has a type and, for the ones that came from a
// remote interface, an info block with its properties.
//
// The id is how a write names its object. Beside the properties,
// params holds the object's parameters: Props is current state,
// and PropInfo is what the object would accept.
type pwObject struct {
	ID   int    `json:"id"`
	Type string `json:"type"`
	Info struct {
		Props  map[string]json.RawMessage `json:"props"`
		Params pwParams                   `json:"params"`
	} `json:"info"`
}

// pwParams holds the two parameter lists this operator reads. Every
// value in either one is a SPA POD that pw-dump printed as JSON.
//
// PropInfo describes the properties an object accepts, and Props
// holds their current values. Every other parameter stays
// unparsed, because parsing it would couple this struct to pod
// shapes nothing here reads.
type pwParams struct {
	PropInfo []pwPropInfo      `json:"PropInfo"`
	Props    []json.RawMessage `json:"Props"`
}

// pwPropInfo describes one property an object accepts: its name, the
// values it takes, and a display label for each one.
//
// type and labels stay raw because their shape depends on the
// property: a choice prints an object of alternatives, a plain
// property prints a bare value, and only the codec reader knows
// which one it is looking at.
type pwPropInfo struct {
	ID     string          `json:"id"`
	Type   json.RawMessage `json:"type"`
	Labels json.RawMessage `json:"labels"`
}

// pwProps is the part of a node's current Props this operator reads.
//
// channelVolumes is the per-channel linear gain the node applies.
// The single volume value beside it is a separate multiplier: the
// lab's sink carried its 40 percent default in channelVolumes,
// cubed to 0.064, while volume read 1.0.
type pwProps struct {
	ChannelVolumes []float64 `json:"channelVolumes"`
}

// readGraph runs pw-dump and returns the sinks it holds, both the
// card's and the radio's. One read answers every question a pass
// asks, so the slice and a prepare call can never report different
// node names for one device.
func readGraph(ctx context.Context) (pwGraph, error) {
	ctx, cancel := context.WithTimeout(ctx, pwDumpTimeout)
	defer cancel()

	command := exec.CommandContext(ctx, "pw-dump")
	// The context's kill bounds the exec, and WaitDelay bounds the
	// read after the kill: without it, Output blocks past the kill if
	// anything inherited the stdout pipe.
	command.WaitDelay = time.Second
	raw, err := command.Output()
	if err != nil {
		return pwGraph{}, fmt.Errorf("running pw-dump: %w", err)
	}
	return parseGraph(raw)
}

// parseGraph reads the sinks out of one pw-dump document.
//
// A sink with no ALSA address and no Bluetooth address is not an
// error. PipeWire publishes sinks that no hardware backs, such as a
// null sink or a network stream, and this operator publishes only the
// outputs its claim delivered.
//
// When two sinks name one PCM device, the first name in alphabetical
// order wins. The pair is what a profile change makes for a moment,
// and a stable choice keeps the operator from writing the slice twice
// for one event.
//
// A Bluetooth speaker's node carries api.bluez5.address and no ALSA
// address at all, so the address decides which of the two maps a
// sink joins.
func parseGraph(document []byte) (pwGraph, error) {
	var objects []pwObject
	if err := json.Unmarshal(document, &objects); err != nil {
		return pwGraph{}, fmt.Errorf("reading pw-dump's output: %w", err)
	}

	graph := pwGraph{
		Outputs:  map[pcmAddress]string{},
		Speakers: map[string]bluezSink{},
	}
	devices := map[string]bluez5Device{}
	for _, object := range objects {
		if object.Type == "PipeWire:Interface:Device" {
			address := normalizeMAC(property(object.Info.Props, bluezAddressProperty))
			if validMAC(address) {
				devices[address] = bluez5Device{
					ID:     object.ID,
					Codecs: codecOptions(object.Info.Params.PropInfo),
				}
			}
			continue
		}
		if object.Type != "PipeWire:Interface:Node" {
			continue
		}
		if property(object.Info.Props, "media.class") != "Audio/Sink" {
			continue
		}
		name := property(object.Info.Props, "node.name")
		if name == "" {
			continue
		}
		if address := normalizeMAC(property(object.Info.Props, bluezAddressProperty)); validMAC(address) {
			if existing, taken := graph.Speakers[address]; taken && existing.Node < name {
				continue
			}
			graph.Speakers[address] = bluezSink{
				Node:    name,
				NodeID:  object.ID,
				Codec:   property(object.Info.Props, bluezCodecProperty),
				Volumes: channelVolumes(object.Info.Params.Props),
			}
			continue
		}
		card, ok := numericProperty(object.Info.Props, sinkCardKeys)
		if !ok {
			continue
		}
		pcm, ok := numericProperty(object.Info.Props, sinkPCMKeys)
		if !ok {
			continue
		}
		output := pcmAddress{Card: card, PCM: pcm}
		if existing, taken := graph.Outputs[output]; taken && existing < name {
			continue
		}
		graph.Outputs[output] = name
	}
	// The device's facts join after the loop because the dump's
	// order is not a contract: a device may print after its node.
	for address, device := range devices {
		sink, hasSink := graph.Speakers[address]
		if !hasSink {
			continue
		}
		sink.Device = device.ID
		sink.Codecs = device.Codecs
		graph.Speakers[address] = sink
	}
	return graph, nil
}

// channelVolumes reads the per-channel levels out of a node's current
// Props.
//
// The first entry that carries channelVolumes is the answer,
// because a node prints one current-values block. A node that
// reports none leaves the channel count unknown, and the unity
// write assumes stereo.
func channelVolumes(params []json.RawMessage) []float64 {
	for _, raw := range params {
		var props pwProps
		if err := json.Unmarshal(raw, &props); err != nil {
			continue
		}
		if len(props.ChannelVolumes) > 0 {
			return props.ChannelVolumes
		}
	}
	return nil
}

// setParam writes one object's Props through pw-cli.
//
// The write is an exec of pw-cli rather than a protocol message,
// for readGraph's reason: one short-lived process per action, no
// client library, no long-lived connection to keep healthy.
//
// The props argument is a SPA object literal, pw-cli's own input
// form: { bluetoothAudioCodec: 1 } names an enum value by its
// integer id.
func setParam(ctx context.Context, object int, props string) error {
	ctx, cancel := context.WithTimeout(ctx, pwCLITimeout)
	defer cancel()

	command := exec.CommandContext(ctx, "pw-cli", "set-param", strconv.Itoa(object), "Props", props)
	command.WaitDelay = time.Second
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("running pw-cli set-param %d Props %s: %w: %s",
			object, props, err, strings.TrimSpace(string(output)))
	}
	return nil
}

// property reads one property as a string. pw-dump prints a property
// whose value reads as a number or a boolean without quotes, even
// though every value in PipeWire's property list is a string, so this
// accepts both forms.
func property(props map[string]json.RawMessage, key string) string {
	raw, ok := props[key]
	if !ok {
		return ""
	}
	text := strings.TrimSpace(string(raw))
	if unquoted, err := strconv.Unquote(text); err == nil {
		return unquoted
	}
	return text
}

// numericProperty reads the first of several keys that holds a whole
// number.
func numericProperty(props map[string]json.RawMessage, keys []string) (int, bool) {
	for _, key := range keys {
		value, err := strconv.Atoi(property(props, key))
		if err == nil {
			return value, true
		}
	}
	return 0, false
}

// waitForPipeWire blocks until PipeWire answers a graph read, or
// until the timeout passes.
func waitForPipeWire(ctx context.Context, read func(context.Context) (pwGraph, error), timeout time.Duration) error {
	deadline := time.After(timeout)
	tick := time.NewTicker(pipewireReadyInterval)
	defer tick.Stop()
	var last error
	for {
		_, err := read(ctx)
		if err == nil {
			return nil
		}
		last = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("PipeWire did not answer within %s: %w", timeout, last)
		case <-tick.C:
		}
	}
}

// waitForNodes blocks until every output has a sink in the graph, or
// until the timeout passes. It reports what it found and never fails.
//
// An output whose node PipeWire could not create is a fact the slice
// reports as the no-sink taint, so the operator publishes it rather
// than refusing to start. Failing here instead would restart the pod
// over one PCM device that cannot open, and take the card's working
// outputs down with it on every attempt.
func waitForNodes(ctx context.Context, read func(context.Context) (pwGraph, error), outputs []alsaOutput, timeout time.Duration) {
	deadline := time.After(timeout)
	tick := time.NewTicker(pipewireReadyInterval)
	defer tick.Stop()
	var report string
	for {
		graph, err := read(ctx)
		if err != nil {
			report = fmt.Sprintf("PipeWire's graph did not read: %v", err)
		} else {
			missing := missingNodes(outputs, graph.Outputs)
			if len(missing) == 0 {
				return
			}
			report = "PipeWire holds no sink for " + strings.Join(missing, ", ")
		}
		select {
		case <-ctx.Done():
			return
		case <-deadline:
			fmt.Fprintf(os.Stderr, "%s after %s; those outputs publish with the no-sink taint\n",
				report, timeout)
			return
		case <-tick.C:
		}
	}
}

// missingNodes names the outputs that have no sink, sorted by name.
func missingNodes(outputs []alsaOutput, sinks map[pcmAddress]string) []string {
	var missing []string
	for _, output := range outputs {
		if _, has := sinks[pcmAddress{Card: output.Card, PCM: output.PCM}]; !has {
			missing = append(missing, output.Name())
		}
	}
	slices.Sort(missing)
	return missing
}
