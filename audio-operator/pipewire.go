package main

// This file reads PipeWire's graph with pw-dump, and pwcli.go writes
// to the objects in it.
//
// PipeWire and WirePlumber run as their own containers in this pod.
// They start before the operator and stop after it, so the kubelet
// supervises them and the operator starts no daemon.
//
// The graph has facts that ALSA cannot report: the name of the node
// that plays or records through each PCM device, which a consumer sets
// as PIPEWIRE_NODE, and the gain, mute, and format of that node, which
// the endpoint's status reports. WirePlumber's bluez monitor builds a
// Bluetooth speaker's node in the same graph, so one read returns both
// kinds of endpoint.
//
// The operator runs pw-dump, which prints the graph as JSON, because
// PipeWire's native protocol would need an implementation of its binary
// POD encoding. pwmonitor.go keeps one pw-dump -m running, so the
// operator does not start a process for each read.

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// pwDumpTimeout limits one graph read. pw-dump waits for the whole
// graph, so without a limit a PipeWire that stops responding blocks the
// read forever.
const pwDumpTimeout = 10 * time.Second

// runtimeDir is the directory where PipeWire creates its socket. The
// PipeWire container and the operator container both mount it from a
// hostPath, so a consumer's CDI spec can bind the same directory into
// its container and reach the socket that the operator reads.
const runtimeDir = "/var/run/audio.liken.sh"

// socketPath is the socket that a consumer connects to. PipeWire reads
// a PIPEWIRE_REMOTE that starts with a slash as a full path, so a
// consumer needs no runtime directory setting.
const socketPath = runtimeDir + "/pipewire-0"

// pcmAddress identifies one ALSA PCM device. It is the key that joins a
// PipeWire node to the ALSA endpoint that the operator publishes.
type pcmAddress struct {
	Card int
	PCM  int
}

// pwDirection is whether a node plays (sink) or records (source), from
// its media class. The values match the names of the Sink and Source
// resources.
type pwDirection string

const (
	directionSink   pwDirection = "sink"
	directionSource pwDirection = "source"
)

// mediaClassDirections maps the media class of an endpoint node to its
// direction. A video node, a MIDI node, or a client's stream has
// another media class and is not an endpoint.
var mediaClassDirections = map[string]pwDirection{
	"Audio/Sink":   directionSink,
	"Audio/Source": directionSource,
}

// nodeAddress identifies one node. A USB card plays and records through
// one PCM device number, so its playback and capture nodes have the
// same card and PCM properties, and the key needs the direction too.
type nodeAddress struct {
	pcmAddress
	Direction pwDirection
}

// pwNode holds the facts about one audio node that the endpoint's
// status reports.
//
// Suspended is true when the node's state is suspended. PipeWire
// applies a level written to a suspended node without updating its
// Props, so its Props can print the levels it last ran at
// (sinkstatus.go).
type pwNode struct {
	ID        int
	Name      string
	Mute      bool
	Volumes   []float64
	Format    pwFormat
	Suspended bool
}

// pwFormat is the format a node negotiated. Positions are PipeWire's
// channel names (FL, FR, LFE), and the status reports them unchanged.
type pwFormat struct {
	Rate      int      `json:"rate"`
	Channels  int      `json:"channels"`
	Positions []string `json:"position"`
}

// pwGraph is the result of one read: every ALSA node by PCM device and
// direction, and every Bluetooth speaker by peer MAC address in
// lowercase colon form.
//
// Linked has the id of every node at either end of a link. A stream
// that plays into a sink and a tap that records from the sink's
// monitor ports both link to the sink's node.
//
// Cookie is the random number that the PipeWire daemon chooses at
// startup (pw_rand32 in src/pipewire/impl-core.c), read from its Core
// object. A restarted PipeWire gives out the same small object ids
// again, so a node id identifies a node only together with the cookie.
// Zero means the graph had no Core object.
type pwGraph struct {
	Cookie   uint32
	Nodes    map[nodeAddress]pwNode
	Speakers map[string]bluezSink
	Linked   map[int]bool
}

// bluezSink is one Bluetooth speaker: the node that plays into it and
// the device that the node belongs to.
//
// The fields up to Suspended come from the node. A consumer sets Node
// as PIPEWIRE_NODE, and a level write on the node names it by NodeID
// and reads the channel count from Volumes. Device, Codecs, and Route
// come from the device.
//
// The operator writes the codec on the device because a codec switch
// destroys the node and keeps the device. The device's Route has the
// speaker's own volume (routes.go).
type bluezSink struct {
	Node      string
	NodeID    int
	Codec     string
	Mute      bool
	Volumes   []float64
	Format    pwFormat
	Suspended bool
	Device    int
	Codecs    []bluezCodec
	Route     *pwRoute
}

// bluez5Device is the part of a PipeWire Device object of the bluez5
// plugin that the operator reads. It is not the BlueZ D-Bus device that
// bluetoothd publishes.
//
// The peer address is the only property that both the device and its
// node carry, so buildGraph joins them on it.
type bluez5Device struct {
	ID     int
	Codecs []bluezCodec
	Route  *pwRoute
}

// The bluez5 SPA plugin sets these properties on every node it creates
// (spa/plugins/bluez5/bluez5-device.c in the PipeWire of this image).
// The address is in BlueZ's uppercase colon form.
const (
	bluezAddressProperty = "api.bluez5.address"
	bluezCodecProperty   = "api.bluez5.codec"
)

// The property keys that give a node's ALSA card and PCM numbers, tried
// in order.
//
// The operator sets the first key of each list on every node that it
// declares in a configuration drop-in (nodes.go). A declared node has
// no alsa.card or alsa.device, because WirePlumber's ALSA monitor sets
// those from the udev device, and this graph has no ALSA monitor.
//
// The other keys map a node from another source. The card profile code
// sets alsa.card and alsa.device on a node that it creates from a
// profile. The ALSA plugin sets the api.alsa.pcm keys on a node that
// opens a PCM device directly, as the Pro Audio profile does.
var (
	nodeCardKeys = []string{nodeCardProperty, "alsa.card", "api.alsa.pcm.card", "api.alsa.card"}
	nodePCMKeys  = []string{nodePCMProperty, "alsa.device", "api.alsa.pcm.device"}
)

// pwObject is the part of a pw-dump object that the operator reads. A
// pw-cli write names the object by ID.
//
// Info is a pointer because pw-dump -m prints a removed global with a
// null info block. The monitor removes an object with a nil Info, and
// buildGraph skips it.
type pwObject struct {
	ID   int     `json:"id"`
	Type string  `json:"type"`
	Info *pwInfo `json:"info"`
}

// pwInfo is an object's info block. A link's block has the ids of the
// two nodes it joins, and no other object's block has them. A node's
// block has its state: creating, suspended, idle, running, or error.
type pwInfo struct {
	Cookie       uint32                     `json:"cookie"`
	State        string                     `json:"state"`
	Props        map[string]json.RawMessage `json:"props"`
	Params       pwParams                   `json:"params"`
	OutputNodeID *int                       `json:"output-node-id"`
	InputNodeID  *int                       `json:"input-node-id"`
}

// pwParams holds the parameter lists that the operator reads. Each
// value is a SPA POD that pw-dump prints as JSON. PropInfo lists the
// properties that an object accepts, and Props has their current
// values. Format is what a node negotiated, and Route has a Bluetooth
// device's own volume.
type pwParams struct {
	PropInfo []pwPropInfo      `json:"PropInfo"`
	Props    []json.RawMessage `json:"Props"`
	Format   []json.RawMessage `json:"Format"`
	Route    []json.RawMessage `json:"Route"`
}

// pwProps is what the operator reads from a node's current Props.
// ChannelVolumes is the linear gain of each channel. The Props also
// have a separate volume, a further multiplier that the operator does
// not read. A sink on the lab fleet read its 40 percent default as
// 0.064, the cube of 0.4, in channelVolumes, with volume at 1.0.
type pwProps struct {
	Mute           bool
	ChannelVolumes []float64
}

// readGraph runs pw-dump and returns the sound cards' and the radio's
// endpoints. One read has everything that a pass needs, so the
// ResourceSlice and a prepare call cannot report different node names
// for one device.
func readGraph(ctx context.Context) (pwGraph, error) {
	ctx, cancel := context.WithTimeout(ctx, pwDumpTimeout)
	defer cancel()

	command := exec.CommandContext(ctx, "pw-dump")
	// Cancelling the context kills pw-dump, and WaitDelay limits how long
	// Output then waits for the stdout pipe. Without it, Output blocks
	// past the kill when another process inherited the pipe.
	command.WaitDelay = time.Second
	raw, err := command.Output()
	if err != nil {
		return pwGraph{}, fmt.Errorf("running pw-dump: %w", err)
	}
	return parseGraph(raw)
}

// parseGraph decodes one pw-dump document. It is separate from
// buildGraph because the monitor in pwmonitor.go decodes its own stream
// and calls buildGraph with the objects it has collected.
func parseGraph(document []byte) (pwGraph, error) {
	var objects []pwObject
	if err := json.Unmarshal(document, &objects); err != nil {
		return pwGraph{}, fmt.Errorf("reading pw-dump's output: %w", err)
	}
	return buildGraph(objects), nil
}

// buildGraph returns the endpoints in a set of objects, in any order.
// The monitor passes the objects from a map, and a plain dump can print
// a device after its node, so the loop collects the devices and joins
// them to their speakers after it ends.
//
// A node with neither an ALSA address nor a Bluetooth address is
// skipped without an error. PipeWire publishes nodes that no hardware
// backs, such as a null sink or a network stream.
//
// When two nodes claim one PCM device and direction, or one speaker,
// the name that sorts first wins. A profile change creates such a pair
// for a moment, and a stable choice keeps the operator from writing the
// ResourceSlice twice for one event.
func buildGraph(objects []pwObject) pwGraph {
	graph := pwGraph{
		Nodes:    map[nodeAddress]pwNode{},
		Speakers: map[string]bluezSink{},
		Linked:   map[int]bool{},
	}
	devices := map[string]bluez5Device{}
	for _, object := range objects {
		if object.Info == nil {
			continue
		}
		if object.Type == "PipeWire:Interface:Core" {
			graph.Cookie = object.Info.Cookie
			continue
		}
		if object.Type == "PipeWire:Interface:Link" {
			for _, end := range []*int{object.Info.OutputNodeID, object.Info.InputNodeID} {
				if end != nil {
					graph.Linked[*end] = true
				}
			}
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
			// A node with no state in the document reads as not
			// suspended.
			Suspended: object.Info.State == "suspended",
		}
		if address := normalizeMAC(property(object.Info.Props, bluezAddressProperty)); validMAC(address) {
			// A Bluetooth capture node is a headset microphone over
			// HFP. This pod registers no headset profile, so the
			// operator skips such a node and does not publish a Source
			// that no consumer can open.
			if direction != directionSink {
				continue
			}
			if existing, taken := graph.Speakers[address]; taken && existing.Node < name {
				continue
			}
			graph.Speakers[address] = bluezSink{
				Node:      name,
				NodeID:    node.ID,
				Codec:     property(object.Info.Props, bluezCodecProperty),
				Mute:      node.Mute,
				Volumes:   node.Volumes,
				Format:    node.Format,
				Suspended: node.Suspended,
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

// nodeProps reads the gain and mute from a node's Props.
//
// An adapter node prints two Props blocks: the audioconvert block, with
// volume, mute, and channelVolumes, and the ALSA block, with the device
// and its latency. nodeProps uses the first block that has a
// channelVolumes key, even an empty list. A suspended node can print
// channelVolumes as an empty list (seen on liken-1), and a test of the
// list's length would read the ALSA block, which has no mute. Another
// suspended node printed the list it last ran with (seen on stick-1),
// so only pwNode.Suspended shows whether the level is current.
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

// nodeFormat returns the first Format param with a rate above zero. A
// suspended node prints an empty Format list, so it reports no format.
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

// property returns one property as a string. PipeWire stores every
// property as a string, but pw-dump prints a value that looks like a
// number or a boolean without quotes, so property accepts both forms.
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

func numericProperty(props map[string]json.RawMessage, keys []string) (int, bool) {
	for _, key := range keys {
		value, err := strconv.Atoi(property(props, key))
		if err == nil {
			return value, true
		}
	}
	return 0, false
}
