package main

// The sink and source nodes this operator declares to PipeWire, and
// why it declares them instead of letting WirePlumber find them.
//
// WirePlumber finds ALSA cards through libudev. Its ALSA monitor asks
// udev for the sound subsystem, builds one PipeWire device for each
// card it is told about, and creates the sink nodes from that device's
// profile. liken runs no udevd, so udev answers with nothing, and the
// whole chain produces no card, no device, and no sink. Every output
// this operator publishes then has the no-sink taint, and no pod can
// play through a machine that has working speakers.
//
// The operator needs no udev to find the hardware. readEndpoints
// enumerates the card's PCM devices from the nodes the claim
// delivers, and reads each playback one's ELD through the control
// interface. So the operator writes the nodes down for PipeWire
// rather than asking it to discover them: one adapter object for each
// PCM device, a sink for playback and a source for capture, declared
// in a PipeWire configuration drop-in.
//
// An init container writes the drop-in and exits, and the PipeWire
// container starts after it, so the pod's own container order puts
// the declaration on disk before PipeWire reads it.
//
// This runs the same way on a machine that does have udevd, because
// the drop-in declares the nodes and the image's WirePlumber
// configuration turns the ALSA monitor off everywhere.

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// pipewireConfigDir is where PipeWire reads its configuration
// fragments. It loads pipewire.conf from the first of
// $XDG_CONFIG_HOME/pipewire, /etc/pipewire, and /usr/share/pipewire
// that has one, and then loads every fragment under the
// pipewire.conf.d of all three, /usr/share first and $XDG_CONFIG_HOME
// last.
//
// A fragment adds to the packaged configuration rather than replacing
// it, because PipeWire merges a dictionary section key by key and
// appends an array section. context.objects is an array, so the
// daemon's own objects, which are its dummy driver and its freewheel
// driver, stay where they are and the outputs below join them.
//
// It is a variable so the tests can point it at a directory they
// control.
var pipewireConfigDir = "/etc/pipewire/pipewire.conf.d"

// dropInName is the generated fragment's file name. PipeWire reads the
// fragments of one directory in sorted order, and 60 puts this one
// after anything the distribution ships.
const dropInName = "60-liken-outputs.conf"

// configPrefix is the whole of the generated file that is not JSON.
// Everything after it is one JSON array, so the generator can build
// the array with encoding/json and the tests can parse it back.
//
// SPA-JSON, which is what PipeWire's configuration parser reads, is a
// superset of JSON: it also admits bare keys, = for :, and newlines
// for commas. Writing the strict subset costs nothing and makes the
// output checkable.
const configPrefix = "context.objects = "

// The property keys that hold an output's ALSA address on a node this
// operator declared. A node that WirePlumber's ALSA monitor built has
// alsa.card and alsa.device, which come from the udev device
// this operator has none of, so it publishes the same two numbers
// under its own names and parseGraph reads those first.
const (
	nodeCardProperty = "liken.audio.card"
	nodePCMProperty  = "liken.audio.pcm"
)

// nodeNamePrefix begins every node name this operator declares. The
// name is the value a consumer's PIPEWIRE_NODE holds, so it must not
// collide with a node that anything else in the graph created.
const nodeNamePrefix = "liken.audio."

// sinkEndpoints picks the playback endpoints out of an inventory.
// The startup wait asks PipeWire for a sink node, and only a
// playback endpoint has one.
func sinkEndpoints(outputs []alsaEndpoint) []alsaEndpoint {
	playback := make([]alsaEndpoint, 0, len(outputs))
	for _, output := range outputs {
		if !output.Capture {
			playback = append(playback, output)
		}
	}
	return playback
}

// staticNode is one entry of PipeWire's context.objects list: the
// factory that creates the object, the flags that say what happens
// when it cannot be created, and the properties it is created with.
type staticNode struct {
	Factory string            `json:"factory"`
	Flags   []string          `json:"flags"`
	Args    map[string]string `json:"args"`
}

// nofail is the object flag that keeps one output's failure from
// taking the card down with it. Without it, an object that PipeWire
// cannot create ends the daemon's startup, so one PCM device that
// refuses to open would leave every other output on the card with no
// sink. With it, PipeWire logs the failure, creates no node, and the
// operator publishes that one output with the no-sink taint.
const nofail = "nofail"

// sinkNodeName is the PipeWire node name for one playback PCM
// device.
//
// The name is derived from the ALSA address and nothing else, so it
// is the same at every start on the same card. It is not the device
// name, which is built from the hardware's identity, for two
// reasons. The declare init container writes this file before
// PipeWire starts and receives no machine name to build that
// identity with. And a node lives only as long as the graph does,
// where a device name has to outlive a reboot.
func sinkNodeName(card, pcm int) string {
	return nodeNamePrefix + alsaAddress(card, pcm)
}

// sourceNodeName is the PipeWire node name for one capture PCM
// device. The trailing c is ALSA's own direction letter, the one
// /dev/snd/pcmC0D0c carries. A card that records and plays through
// one PCM device would otherwise declare two nodes under one name.
func sourceNodeName(card, pcm int) string {
	return sinkNodeName(card, pcm) + "c"
}

// nodeConfig builds the whole drop-in for one card's endpoints:
// one sink node for every playback PCM device, monitor or not, and
// one source node for every capture PCM device. A PCM device that
// runs in both directions declares its sink first, so the file reads
// in the order the devices appear.
//
// layouts holds the channel layout of each sink, keyed by its
// address in the graph (layout.go). A sink the map does not name is
// declared with no positions.
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
	// encoding/json cannot fail on a slice of strings and maps of
	// strings, and a generator that returned an error for a case that
	// cannot happen would put a branch in every caller for nothing.
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

// configHeader explains the file to whoever finds it on a machine.
// PipeWire's parser takes # to the end of the line as a comment.
const configHeader = `# The claimed card's endpoints, written by the pod's declare init
# container before PipeWire starts, and written again by every
# replacement pod. The operator container writes it again when a
# sink's channel layout changes, and the kubelet then restarts the
# PipeWire container. Editing this file by hand restarts PipeWire with
# the edit, and the next layout change overwrites it.
#
# WirePlumber's ALSA monitor enumerates cards through libudev, and a
# liken machine runs no udevd, so the monitor finds no card and builds
# no sink. The operator enumerates the card itself, through the ALSA
# control interface, and declares one sink node for each playback PCM
# device and one source node for each capture PCM device here. The
# monitor stays off, on every host, so the graph has one writer.
#
# The syntax below is strict JSON, which is a subset of the SPA-JSON
# that PipeWire reads. That is why every key is quoted and sorted, and
# why nofail appears as a one-element array. nofail keeps one output
# that cannot be created from stopping the daemon, which would take
# every other output on the card down with it.

`

// sinkObject declares one playback PCM device as an audio sink.
//
// api.alsa.path is the ALSA device name the node opens, in the same
// hw:<card>,<device> form that aplay -D takes. It is the whole of what
// this node needs from ALSA, and it needs no udev device to resolve.
// api.alsa.pcm.card holds the card number on its own, because the
// node opens the card's control interface for its mixer elements and
// reads the number from that property rather than from the path.
//
// media.class is set here and nowhere else. The ALSA plugin keeps a
// media class of its own inside the SPA node, and the adapter module
// copies no default onto the PipeWire node, so a node declared without
// it is a node that no client and no session manager treats as a sink.
//
// The channel layout comes from layout.go, and layoutArgs says how
// each kind of layout reaches the node.
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
			// The two numbers parseGraph maps a node back to. The adapter
			// module hands the whole args dictionary to the node it
			// creates, and a node's info block holds its whole property
			// list, so a property PipeWire does not recognize still
			// arrives in pw-dump's output.
			//
			// The PCM device number needs a property of this operator's
			// own. api.alsa.pcm.device is the key that names it elsewhere
			// in PipeWire, and a sink node neither reads it nor publishes
			// it: the node takes the device number from the path.
			nodeCardProperty: fmt.Sprint(card),
			nodePCMProperty:  fmt.Sprint(pcm),
		},
	}
	maps.Copy(node.Args, layoutArgs(layout))
	return node
}

// layoutArgs are the properties that give a sink node its channel
// layout.
//
// A layout with positions sets audio.channels and audio.position
// together. The ALSA node narrows its channel range to the one count
// only when audio.channels names it, and it reports the positions in
// its EnumFormat only when the count is fixed and audio.position has
// that many entries (add_channels in spa/plugins/alsa/alsa-pcm.c in
// pipewire 1.4). The position list is in SPA-JSON array form,
// "[ FL FR ]", which is the form spa_alsa_parse_position reads.
//
// A layout from the channel map sets api.alsa.use-chmap and no
// position, so the node takes its positions from the PCM's channel
// map when it opens the device. A sink with no layout gets none of
// these, and the node reports the card's own channel range with no
// positions.
//
// layoutSourceProperty records where the layout came from. PipeWire
// ignores it, and the operator reads it back to report the source and
// to apply the rule for an HDMI monitor that turns off (layout.go).
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

// layoutSourceProperty is the node property that records where a
// sink's layout came from.
const layoutSourceProperty = "liken.audio.layout.source"

// sourceObject declares one capture PCM device as an audio source.
//
// It is the same adapter object as a sink with two values changed:
// api.alsa.pcm.source opens the PCM device for capture, and the
// media class puts the node on the other side of the graph. The
// source node takes the same Props a sink does
// (spa/plugins/alsa/alsa-pcm-source.c in pipewire 1.4.2), so a
// volume and a mute reach it through the same write.
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

// boolOrder sorts false ahead of true, which puts a PCM device's
// playback node ahead of its capture node.
func boolOrder(value bool) int {
	if value {
		return 1
	}
	return 0
}

// declareMode is the argument that selects the declaration mode. The
// pod runs the image once in this mode, as an init container, and the
// container exits when the drop-in is on disk.
const declareMode = "declare"

// declare writes the drop-in and ends the process.
//
// The write must finish before PipeWire starts, because PipeWire
// builds context.objects while it loads its configuration and never
// reads a fragment that arrives later. An init container that runs to
// completion before the PipeWire sidecar starts is the kubelet's own
// expression of that order, so no code here waits for anything.
//
// The same container is the switch for the Bluetooth monitor,
// because its environment is where the claim's delivery states
// whether a media bus came with it (bluez.go).
//
// Each sink's channel layout is selected here, before PipeWire starts,
// so a node is created with its positions and no stream links to it
// as stereo first. The Sinks' spec.layout comes from the API server,
// and a server that does not answer leaves the ELD and the channel
// map as the sources (layoutspec.go).
func declare() {
	outputs, err := readEndpoints()
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
	// The volume fragment is written on every machine, where the
	// Bluetooth one follows the delivered bus, because the unity
	// default is policy for the card's sinks as much as a radio's.
	if err := writeVolumeConfig(); err != nil {
		fatal("setting WirePlumber's default sink volume: %v", err)
	}
}

// writeNodeConfig generates the drop-in and writes it where PipeWire
// reads it. It returns the document it wrote.
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

// writeDeclaration replaces the drop-in with one document.
//
// The document is written beside the drop-in under a name that does
// not end in .conf, which PipeWire does not load, and then renamed
// over it. A PipeWire that starts during the write reads the whole of
// the old document or the whole of the new one.
func writeDeclaration(document string) error {
	if err := os.MkdirAll(pipewireConfigDir, 0o755); err != nil {
		return fmt.Errorf("making %s: %w", pipewireConfigDir, err)
	}
	path := filepath.Join(pipewireConfigDir, dropInName)
	staged := path + ".new"
	if err := os.WriteFile(staged, []byte(document), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", staged, err)
	}
	if err := os.Rename(staged, path); err != nil {
		return fmt.Errorf("renaming %s to %s: %w", staged, path, err)
	}
	return nil
}

// readNodeConfig reads the drop-in back.
//
// The declare init container writes this file before PipeWire starts,
// and the operator container writes it again for a layout change just
// before PipeWire restarts in its container, so the file is the record of
// what PipeWire builds its graph from. The reconcile pass compares the
// card's current PCM devices and layouts against it (declared.go).
func readNodeConfig() (string, error) {
	path := filepath.Join(pipewireConfigDir, dropInName)
	document, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	return string(document), nil
}
