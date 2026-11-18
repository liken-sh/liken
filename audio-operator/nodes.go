package main

// This file declares the claimed card's sink and source nodes in a
// PipeWire configuration drop-in.
//
// WirePlumber's ALSA monitor finds sound cards through libudev and
// creates a PipeWire device, with its nodes, for each card. A liken
// machine runs no udevd, so the monitor finds no card. Every output
// the operator publishes would then carry the no-sink taint, and no pod
// could play through working speakers.
//
// So readEndpoints lists the card's PCM devices from the device nodes
// that the claim gives the pod, and this file writes an adapter object
// for each one into the drop-in: a sink for playback and a source for
// capture. The image's WirePlumber configuration turns the ALSA monitor
// off on every machine, so the drop-in is the only source of these nodes
// even where udevd runs.
//
// configHeader explains to a person who reads the drop-in on the
// machine who writes it and when (layoutdrift.go, restarts.go).

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// pipewireConfigDir is the directory of PipeWire configuration
// drop-ins that the pod's containers share. PipeWire appends the
// context.objects array of each drop-in to the packaged one, so its
// own dummy and freewheel drivers stay beside the nodes declared here.
// Tests point it at a temporary directory.
var pipewireConfigDir = "/etc/pipewire/pipewire.conf.d"

// dropInName starts with 60 because PipeWire loads a directory's
// drop-ins in sorted order, so this one loads after any that the
// distribution ships.
const dropInName = "60-liken-outputs.conf"

// The generated file is configHeader, then configPrefix, then a JSON
// array. PipeWire reads SPA-JSON, which accepts strict JSON, so the
// array is written with encoding/json and parseDeclaration reads it
// back the same way.
const configPrefix = "context.objects = "

// The node properties that record a declared node's card and PCM
// numbers, for parseGraph and parseDeclaration. A declared node has no
// alsa.card or alsa.device, because those come from the udev device
// that WirePlumber's ALSA monitor reads.
const (
	nodeCardProperty = "liken.audio.card"
	nodePCMProperty  = "liken.audio.pcm"
)

// nodeNamePrefix starts every declared node name. A consumer's CDI spec
// sets PIPEWIRE_NODE to the node name, so the prefix keeps that name
// from matching a node that something else creates.
const nodeNamePrefix = "liken.audio."

func sinkEndpoints(outputs []alsaEndpoint) []alsaEndpoint {
	playback := make([]alsaEndpoint, 0, len(outputs))
	for _, output := range outputs {
		if !output.Capture {
			playback = append(playback, output)
		}
	}
	return playback
}

// staticNode is one entry of PipeWire's context.objects array.
type staticNode struct {
	Factory string            `json:"factory"`
	Flags   []string          `json:"flags"`
	Args    map[string]string `json:"args"`
}

// nofail makes PipeWire skip an object it cannot create. Without it,
// one PCM device that fails to open stops PipeWire from starting, and
// every output on the card loses its sink. With it, only that output
// has no node, and the operator publishes it with the no-sink taint.
const nofail = "nofail"

// sinkNodeName names a node by its card and PCM numbers (alsaAddress).
// The numbers are unique on the machine while PipeWire runs, and a node
// lasts only as long as its PipeWire, so the name is unique for the
// node's life.
func sinkNodeName(card, pcm int) string {
	return nodeNamePrefix + alsaAddress(card, pcm)
}

// sourceNodeName adds ALSA's capture suffix, the c in
// /dev/snd/pcmC0D0c. Without it, a PCM device that both plays and
// records would declare a sink and a source under one name.
func sourceNodeName(card, pcm int) string {
	return sinkNodeName(card, pcm) + "c"
}

// nodeConfig builds the drop-in for a card's endpoints. Each playback PCM
// device gets a sink whether or not a monitor is connected, because
// PipeWire reads the drop-in only when it starts, and the sink must exist
// when a monitor is plugged in later. Objects are sorted by card and PCM
// number, with playback before capture, so the same endpoints always give
// the same file.
//
// layouts holds each sink's channel layout, keyed by graphAddress. A
// sink with no entry gets no layout properties.
func nodeConfig(outputs []alsaEndpoint, layouts map[nodeAddress]channelLayout) string {
	sorted := slices.Clone(outputs)
	slices.SortFunc(sorted, func(a, b alsaEndpoint) int {
		if a.Card != b.Card {
			return a.Card - b.Card
		}
		if a.PCM != b.PCM {
			return a.PCM - b.PCM
		}
		return boolOrder(a.Capture) - boolOrder(b.Capture)
	})

	objects := make([]staticNode, 0, len(sorted))
	for _, output := range sorted {
		if output.Capture {
			objects = append(objects, sourceObject(output.Card, output.PCM))
			continue
		}
		objects = append(objects, sinkObject(output.Card, output.PCM, layouts[output.graphAddress()]))
	}
	// Marshaling slices and maps of strings cannot fail.
	body, err := json.MarshalIndent(objects, "", "  ")
	if err != nil {
		panic(err)
	}

	var document strings.Builder
	document.WriteString(configHeader)
	document.WriteString(configPrefix)
	document.Write(body)
	document.WriteString("\n")
	return document.String()
}

// configHeader explains the generated file to someone who reads it on a
// machine. PipeWire treats # to the end of a line as a comment.
const configHeader = `# The claimed card's PipeWire nodes: a sink for each playback PCM
# device and a source for each capture PCM device. WirePlumber's ALSA
# monitor is off, so these are the card's only nodes.
#
# The pod's declare init container writes this file before PipeWire
# starts, and each replacement pod writes it again. When a sink's
# channel layout changes, the operator container writes it again, and
# the PipeWire container's first process restarts PipeWire in place to
# load it. The next replacement pod or layout change overwrites a hand
# edit.
#
# The syntax is strict JSON, which PipeWire's SPA-JSON parser accepts,
# so every key and value is quoted. nofail lets PipeWire start when it
# cannot create one node, so the card's other outputs keep their nodes.

`

// sinkObject declares one playback PCM device.
//
// api.alsa.path is hw:<card>,<pcm>, the form aplay -D takes. For a sink
// that passes encoded formats through, bitstreamArgs replaces it with
// alsa-lib's hdmi device. api.alsa.pcm.card is set as well, because the
// node opens the card's control interface for its mixer elements and
// reads the card number from that property, not from the path.
//
// media.class must be set here. The ALSA plugin keeps its media class
// inside the SPA node, and the adapter module does not copy it onto the
// PipeWire node, so without this property no client or session manager
// treats the node as a sink.
func sinkObject(card, pcm int, layout channelLayout) staticNode {
	node := staticNode{
		Factory: "adapter",
		Flags:   []string{nofail},
		Args: map[string]string{
			"factory.name":      "api.alsa.pcm.sink",
			"api.alsa.path":     fmt.Sprintf("hw:%d,%d", card, pcm),
			"api.alsa.pcm.card": fmt.Sprint(card),
			"node.name":         sinkNodeName(card, pcm),
			"node.description": fmt.Sprintf("liken audio output, ALSA card %d device %d",
				card, pcm),
			"media.class": "Audio/Sink",
			// The adapter module copies every arg onto the node, so
			// pw-dump shows these keys although PipeWire does not read
			// them. The PCM number needs a key of the operator's own,
			// because a sink node reads its device number from
			// api.alsa.path and does not publish api.alsa.pcm.device.
			nodeCardProperty: fmt.Sprint(card),
			nodePCMProperty:  fmt.Sprint(pcm),
		},
	}
	maps.Copy(node.Args, layoutArgs(layout))
	maps.Copy(node.Args, bitstreamArgs(card, pcm, layout))
	return node
}

// layoutArgs returns the node properties that set a sink's channel
// layout.
//
// A layout with positions sets audio.channels and audio.position
// together. The ALSA node fixes its channel count only when
// audio.channels names it, and it reports positions in its EnumFormat
// only when the count is fixed and audio.position has that many entries
// (add_channels in spa/plugins/alsa/alsa-pcm.c, pipewire 1.4).
// spa_alsa_parse_position reads audio.position in the form "[ FL FR ]".
//
// A layout from the channel map sets api.alsa.use-chmap and no
// positions, so the node reads the positions from the PCM device's
// channel map when it opens the device. A sink with no layout gets none
// of these, and the node reports the card's own channel range with no
// positions.
//
// PipeWire ignores layoutSourceProperty. declaredLayout reads it back
// to report where the layout came from and to apply the rule for an
// HDMI monitor that turns off (layout.go).
func layoutArgs(layout channelLayout) map[string]string {
	switch layout.source() {
	case layoutFromChannelMap:
		return map[string]string{
			"api.alsa.use-chmap": "true",
			layoutSourceProperty: string(layoutFromChannelMap),
		}
	case layoutNone:
		return nil
	}
	return map[string]string{
		"audio.channels":     fmt.Sprint(len(layout.Positions)),
		"audio.position":     "[ " + strings.Join(layout.Positions, " ") + " ]",
		layoutSourceProperty: string(layout.Source),
	}
}

const layoutSourceProperty = "liken.audio.layout.source"

// sourceObject declares one capture PCM device. It uses the
// api.alsa.pcm.source factory and the Audio/Source media class, and has
// no channel layout. The source node accepts the same Props as a sink, so
// volume and mute are written the same way
// (spa/plugins/alsa/alsa-pcm-source.c, pipewire 1.4.2).
func sourceObject(card, pcm int) staticNode {
	return staticNode{
		Factory: "adapter",
		Flags:   []string{nofail},
		Args: map[string]string{
			"factory.name":      "api.alsa.pcm.source",
			"api.alsa.path":     fmt.Sprintf("hw:%d,%d", card, pcm),
			"api.alsa.pcm.card": fmt.Sprint(card),
			"node.name":         sourceNodeName(card, pcm),
			"node.description": fmt.Sprintf("liken audio input, ALSA card %d device %d",
				card, pcm),
			"media.class":    "Audio/Source",
			nodeCardProperty: fmt.Sprint(card),
			nodePCMProperty:  fmt.Sprint(pcm),
		},
	}
}

// boolOrder orders false before true, so a PCM device's playback node
// sorts before its capture node.
func boolOrder(value bool) int {
	if value {
		return 1
	}
	return 0
}

// declareMode is the argument that runs the image as the declare init
// container.
const declareMode = "declare"

// declare is the declare init container's entry point. It writes the
// PipeWire drop-in and the WirePlumber fragments, and returns so that
// the container exits and the kubelet starts PipeWire and WirePlumber.
//
// It chooses each sink's layout before PipeWire starts, so PipeWire
// creates the node with its positions and no stream links to it as
// stereo first. spec.layout comes from the Sinks on the API server.
// When the server does not respond, the layouts come from the ELD and
// the channel map alone (layoutspec.go).
func declare() {
	// During a hotplug, an HDMI output can read with no monitor and
	// would then be declared as stereo. So for an output whose Sink
	// reports a monitor, readOutputsForDeclaration waits up to
	// monitorSettle for the monitor to read again (monitorhold.go).
	outputs, err := readOutputsForDeclaration(readEndpoints, readMissingSinkMonitor, cardChanges)
	if err != nil {
		fatal("reading the card's outputs: %v", err)
	}
	layouts := selectLayouts(outputs, declaredSpecs(outputs), nil)
	if _, err := writeNodeConfig(outputs, layouts); err != nil {
		fatal("declaring the card's outputs to PipeWire: %v", err)
	}
	if _, err := writeMonitorConfig(); err != nil {
		fatal("enabling WirePlumber's Bluetooth monitor: %v", err)
	}
	if err := writeVolumeConfig(); err != nil {
		fatal("setting WirePlumber's default sink volume: %v", err)
	}
}

func writeNodeConfig(outputs []alsaEndpoint, layouts map[nodeAddress]channelLayout) (string, error) {
	document := nodeConfig(outputs, layouts)
	if err := writeDeclaration(document); err != nil {
		return "", err
	}
	fmt.Printf("declared %d sink node(s) and %d source node(s) to PipeWire in %s\n",
		len(sinkEndpoints(outputs)), len(outputs)-len(sinkEndpoints(outputs)),
		filepath.Join(pipewireConfigDir, dropInName))
	for _, output := range sinkEndpoints(outputs) {
		fmt.Printf("declared %s with %s\n", output.Address(), layouts[output.graphAddress()])
	}
	return document, nil
}

// writeDeclaration writes a .new file and renames it over the drop-in.
// PipeWire does not load .new files, and the rename is atomic, so a
// PipeWire that starts during the write reads either the whole old
// drop-in or the whole new one.
func writeDeclaration(document string) error {
	if err := os.MkdirAll(pipewireConfigDir, 0o755); err != nil {
		return fmt.Errorf("making PipeWire's configuration directory: %w", err)
	}
	path := filepath.Join(pipewireConfigDir, dropInName)
	staged := path + ".new"
	if err := os.WriteFile(staged, []byte(document), 0o644); err != nil {
		return fmt.Errorf("staging the PipeWire declaration: %w", err)
	}
	if err := os.Rename(staged, path); err != nil {
		return fmt.Errorf("replacing the PipeWire declaration: %w", err)
	}
	return nil
}

// readNodeConfig returns the drop-in on disk. The operator reads it at
// start as the record of the nodes and layouts that PipeWire loaded,
// and the reconcile pass compares the card's PCM devices and layouts
// with it (declared.go).
func readNodeConfig() (string, error) {
	path := filepath.Join(pipewireConfigDir, dropInName)
	document, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading the PipeWire declaration: %w", err)
	}
	return string(document), nil
}
