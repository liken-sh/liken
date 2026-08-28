package main

// Reading PipeWire's graph, and writing to the objects in it.
//
// PipeWire and WirePlumber run in their own containers of this pod,
// started before the operator and stopped after it, so nothing here
// starts or supervises a daemon. The kubelet does both.
//
// The operator needs facts from PipeWire that ALSA cannot give it:
// the name of the node that plays or records through each PCM device,
// which is the value a consumer's PIPEWIRE_NODE holds, and the gain,
// mute, and format that node runs at now, which the endpoint's status
// reports. A Bluetooth speaker's node is in the same graph, built by
// WirePlumber's bluez monitor, so one read yields both kinds.
//
// The operator reads the graph by running pw-dump, which ships with
// PipeWire in the same image, and parsing the JSON it prints. The
// alternative is to speak PipeWire's native protocol, which means
// implementing its binary POD encoding for every message the graph
// walk needs. A plain pw-dump is one process per read, and pwmonitor.go
// keeps one pw-dump -m running so that a steady state costs no process
// at all.
//
// The writes take the same path for the same reason: pw-cli
// set-param is one process per write, changing one parameter on an
// object the same dump named.

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
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

// pcmAddress names one PCM device, which is what ties a PipeWire node
// to the ALSA endpoint the operator publishes.
type pcmAddress struct {
	Card int
	PCM  int
}

// pwDirection is the half of the graph a node's media class puts it
// in. The two words are the same two the sink and source attributes
// carry and the two resource kinds are named for, so one word runs
// from the graph to the API without a translation.
type pwDirection string

const (
	directionSink   pwDirection = "sink"
	directionSource pwDirection = "source"
)

// mediaClassDirections is the filter on the graph. The operator reads
// the two audio classes and nothing else: a video node, a MIDI node,
// or a stream a client opened is not an endpoint.
var mediaClassDirections = map[string]pwDirection{
	"Audio/Sink":   directionSink,
	"Audio/Source": directionSource,
}

// nodeAddress keys one node. A USB card serves playback and capture
// through one PCM device number, and the two declared nodes carry the
// same card and PCM properties, so the direction is part of the key.
type nodeAddress struct {
	pcmAddress
	Direction pwDirection
}

// pwNode is what one audio node reports. Every field here is a fact
// the endpoint's status carries: the name a consumer targets, the
// gain and mute PipeWire applies, and the format the node negotiated.
type pwNode struct {
	ID      int
	Name    string
	Mute    bool
	Volumes []float64
	Format  pwFormat
}

// pwFormat is the format a node negotiated and runs at now. The
// positions are PipeWire's own channel names, FL, FR, LFE, and so on,
// and the status reports them in the same spelling.
type pwFormat struct {
	Rate      int      `json:"rate"`
	Channels  int      `json:"channels"`
	Positions []string `json:"position"`
}

// pwGraph is what one read yields: every ALSA node by PCM device and
// direction, and every Bluetooth speaker by peer MAC in the lowercase
// colon form.
type pwGraph struct {
	Nodes    map[nodeAddress]pwNode
	Speakers map[string]bluezSink
}

// bluezSink is one Bluetooth speaker as the graph reports it: its
// node, and the device the node belongs to.
//
// The node's part is the name a consumer's PIPEWIRE_NODE holds, the
// node id and channel count a level write needs, the codec the
// transport negotiated, and the gain, mute, and format the node runs
// at. The device's part is its object id, the codecs it offers, and
// its Route.
//
// The two parts are kept apart in the mind because they have
// different lives. A codec switch destroys the node and keeps the
// device, so the codec choice is written on the device. The Route is
// the device's too: it is where the speaker's own volume lives, and
// a write to it goes over AVRCP, where a write to the node's gain
// stays in software.
type bluezSink struct {
	Node    string
	NodeID  int
	Codec   string
	Mute    bool
	Volumes []float64
	Format  pwFormat
	Device  int
	Codecs  []bluezCodec
	Route   *pwRoute
}

// bluez5Device is the part of a bluez5 Device object this operator
// reads: its own object id, the codecs it offers, and its current
// Route. The name says bluez5 because bluez_test.go's bluezDevice is
// the other thing with that name, the D-Bus object bluetoothd
// publishes.
//
// The device object is read separately from the node and joined
// to it by peer address, because PipeWire publishes them as two
// objects and the address is the one property both carry.
type bluez5Device struct {
	ID     int
	Codecs []bluezCodec
	Route  *pwRoute
}

// The node properties the bluez5 SPA plugin sets on every node it
// emits (spa/plugins/bluez5/bluez5-device.c in the pipewire this
// image ships). The address is BlueZ's own uppercase colon form.
const (
	bluezAddressProperty = "api.bluez5.address"
	bluezCodecProperty   = "api.bluez5.codec"
)

// The property keys that hold a node's ALSA address, in the order
// this operator reads them. A playback node and a capture node of one
// PCM device carry the same pair, and the media class tells them
// apart.
//
// The first key of each list is the one the operator writes itself,
// which is the only pair it can rely on. It declares every node in a
// configuration drop-in (see nodes.go), and a declared node has no
// alsa.card and no alsa.device, because those two come from the udev
// device that WirePlumber's ALSA monitor builds and this graph has no
// monitor in it.
//
// The rest are kept so that a node from any other source still maps.
// alsa.card and alsa.device are what the card profile code puts on a
// node it creates from a profile, and the api.alsa.pcm pair is what
// the ALSA plugin puts on a node that opens a PCM device directly, as
// the Pro Audio profile does.
var (
	nodeCardKeys = []string{nodeCardProperty, "alsa.card", "api.alsa.pcm.card", "api.alsa.card"}
	nodePCMKeys  = []string{nodePCMProperty, "alsa.device", "api.alsa.pcm.device"}
)

// pwObject is the part of pw-dump's output this operator reads. Every
// object in the dump has a type and, for the ones that came from a
// remote interface, an info block with its properties.
//
// The info block is a pointer because its absence means something.
// pw-dump -m prints a removed global as an object whose info is null,
// and a global that never carried a block holds nothing this operator
// reads, so the monitor drops both.
//
// The id is how a write names its object. Beside the properties,
// params holds the object's parameters: Props is current state,
// and PropInfo is what the object would accept.
type pwObject struct {
	ID   int     `json:"id"`
	Type string  `json:"type"`
	Info *pwInfo `json:"info"`
}

// pwInfo is an object's properties and parameters.
type pwInfo struct {
	Props  map[string]json.RawMessage `json:"props"`
	Params pwParams                   `json:"params"`
}

// pwParams holds the parameter lists this operator reads. Every value
// in any of them is a SPA POD that pw-dump printed as JSON.
//
// PropInfo describes the properties an object accepts, and Props
// holds their current values. Format is what a node negotiated, and
// Route is where a Bluetooth device's own volume lives. Every other
// parameter stays unparsed, because parsing it would couple this
// struct to pod shapes nothing here reads.
type pwParams struct {
	PropInfo []pwPropInfo      `json:"PropInfo"`
	Props    []json.RawMessage `json:"Props"`
	Format   []json.RawMessage `json:"Format"`
	Route    []json.RawMessage `json:"Route"`
}

// pwProps is the part of a node's current Props this operator reads.
//
// channelVolumes is the per-channel linear gain the node applies, and
// mute is whether it applies silence instead. The single volume value
// beside them is a separate multiplier: the lab's sink carried its 40
// percent default in channelVolumes, cubed to 0.064, while volume
// read 1.0.
type pwProps struct {
	Mute           bool
	ChannelVolumes []float64
}

// readGraph runs pw-dump and returns the endpoints it holds, both the
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

// parseGraph reads the endpoints out of one pw-dump document. The
// parse and the build are two steps because the monitor in
// pwmonitor.go decodes its own stream and hands buildGraph the
// objects it holds.
func parseGraph(document []byte) (pwGraph, error) {
	var objects []pwObject
	if err := json.Unmarshal(document, &objects); err != nil {
		return pwGraph{}, fmt.Errorf("reading pw-dump's output: %w", err)
	}
	return buildGraph(objects), nil
}

// buildGraph reads the endpoints out of a set of objects, in any
// order. A node with no ALSA address and no Bluetooth address is not
// an error. PipeWire publishes nodes that no hardware backs, such as
// a null sink or a network stream, and this operator publishes only
// the endpoints it declared.
//
// The order is not a contract because the monitor hands over the
// objects out of a map, and because a device may print after its
// node in a plain dump. So a device's facts join its speaker after
// the loop.
//
// When two nodes name one PCM device and one direction, the first
// name in alphabetical order wins. The pair is what a profile change
// makes for a moment, and a stable choice keeps the operator from
// writing the slice twice for one event.
//
// A Bluetooth speaker's node carries api.bluez5.address and no ALSA
// address at all, so the address decides which of the two maps a node
// joins.
func buildGraph(objects []pwObject) pwGraph {
	graph := pwGraph{
		Nodes:    map[nodeAddress]pwNode{},
		Speakers: map[string]bluezSink{},
	}
	devices := map[string]bluez5Device{}
	for _, object := range objects {
		if object.Info == nil {
			continue
		}
		if object.Type == "PipeWire:Interface:Device" {
			address := normalizeMAC(property(object.Info.Props, bluezAddressProperty))
			if validMAC(address) {
				devices[address] = bluez5Device{
					ID:     object.ID,
					Codecs: codecOptions(object.Info.Params.PropInfo),
					Route:  outputRoute(object.Info.Params.Route),
				}
			}
			continue
		}
		if object.Type != "PipeWire:Interface:Node" {
			continue
		}
		direction, audio := mediaClassDirections[property(object.Info.Props, "media.class")]
		if !audio {
			continue
		}
		name := property(object.Info.Props, "node.name")
		if name == "" {
			continue
		}
		props := nodeProps(object.Info.Params.Props)
		node := pwNode{
			ID:      object.ID,
			Name:    name,
			Mute:    props.Mute,
			Volumes: props.ChannelVolumes,
			Format:  nodeFormat(object.Info.Params.Format),
		}
		if address := normalizeMAC(property(object.Info.Props, bluezAddressProperty)); validMAC(address) {
			// A Bluetooth capture node is a headset microphone over
			// HFP, and this pod registers no headset profile, so the
			// radio's nodes are sinks or nothing. A capture node that
			// appears anyway is left out rather than published as a
			// Source nobody can open.
			if direction != directionSink {
				continue
			}
			if existing, taken := graph.Speakers[address]; taken && existing.Node < name {
				continue
			}
			graph.Speakers[address] = bluezSink{
				Node:    name,
				NodeID:  node.ID,
				Codec:   property(object.Info.Props, bluezCodecProperty),
				Mute:    node.Mute,
				Volumes: node.Volumes,
				Format:  node.Format,
			}
			continue
		}
		card, ok := numericProperty(object.Info.Props, nodeCardKeys)
		if !ok {
			continue
		}
		pcm, ok := numericProperty(object.Info.Props, nodePCMKeys)
		if !ok {
			continue
		}
		endpoint := nodeAddress{pcmAddress: pcmAddress{Card: card, PCM: pcm}, Direction: direction}
		if existing, taken := graph.Nodes[endpoint]; taken && existing.Name < name {
			continue
		}
		graph.Nodes[endpoint] = node
	}
	for address, device := range devices {
		sink, hasSink := graph.Speakers[address]
		if !hasSink {
			continue
		}
		sink.Device = device.ID
		sink.Codecs = device.Codecs
		sink.Route = device.Route
		graph.Speakers[address] = sink
	}
	return graph
}

// nodeProps reads the gain and mute out of a node's Props.
//
// An adapter node prints two Props blocks: the audioconvert one,
// which carries volume, mute, and channelVolumes, and the ALSA one,
// which carries the device and its latency. The audioconvert block is
// the one with the channelVolumes key, and the key's presence is the
// test, not the list's length: a suspended node on liken-1 prints
// channelVolumes as an empty list, and a length test would fall
// through to the ALSA block and read a mute that is not there.
func nodeProps(params []json.RawMessage) pwProps {
	for _, raw := range params {
		var block struct {
			Mute           bool       `json:"mute"`
			ChannelVolumes *[]float64 `json:"channelVolumes"`
		}
		if err := json.Unmarshal(raw, &block); err != nil {
			continue
		}
		if block.ChannelVolumes == nil {
			continue
		}
		return pwProps{Mute: block.Mute, ChannelVolumes: *block.ChannelVolumes}
	}
	return pwProps{}
}

// nodeFormat reads the format a node negotiated when it started. A
// suspended node prints an empty Format list, and a rate above zero
// is what says a format was read at all, so a node that is not
// running reports no format.
func nodeFormat(params []json.RawMessage) pwFormat {
	for _, raw := range params {
		var format pwFormat
		if err := json.Unmarshal(raw, &format); err != nil {
			continue
		}
		if format.Rate > 0 {
			return format
		}
	}
	return pwFormat{}
}

// setParam writes one of an object's parameters through pw-cli. The
// parameter is named because the two writes this operator makes go
// to different ones: Props on a node, and Route on a Bluetooth
// device.
//
// The write is an exec of pw-cli rather than a protocol message,
// for readGraph's reason: one short-lived process per action, no
// client library, no long-lived connection to keep healthy.
//
// The pod argument is a SPA object literal, pw-cli's own input
// form: { bluetoothAudioCodec: 1 } names an enum value by its
// integer id.
func setParam(ctx context.Context, object int, param, pod string) error {
	ctx, cancel := context.WithTimeout(ctx, pwCLITimeout)
	defer cancel()

	command := exec.CommandContext(ctx, "pw-cli", "set-param", strconv.Itoa(object), param, pod)
	command.WaitDelay = time.Second
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("running pw-cli set-param %d %s %s: %w: %s",
			object, param, pod, err, strings.TrimSpace(string(output)))
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
