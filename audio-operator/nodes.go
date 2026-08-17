package main

// The sink nodes this operator declares to PipeWire, and why it
// declares them instead of letting WirePlumber find them.
//
// WirePlumber finds ALSA cards through libudev. Its ALSA monitor asks
// udev for the sound subsystem, builds one PipeWire device for each
// card it is told about, and creates the sink nodes from that device's
// profile. liken runs no udevd, so udev answers with nothing, and the
// whole chain produces no card, no device, and no sink. Every output
// this operator publishes then carries the no-sink taint, and no pod
// can play through a machine that has working speakers.
//
// The operator needs no udev to find the hardware. readOutputs
// enumerates the card's playback PCM devices from the nodes the claim
// delivers, and reads each one's ELD through the control interface. So
// the operator writes the nodes down for PipeWire rather than asking
// it to discover them: one adapter object for each playback PCM,
// declared in a PipeWire configuration drop-in that the operator
// generates before it starts the daemons.
//
// This runs the same way on a machine that does have udevd, because
// the drop-in declares the nodes and the image's WirePlumber
// configuration turns the ALSA monitor off everywhere. One graph on
// every host, built from one enumeration.

import (
	"encoding/json"
	"fmt"
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

// The property keys that carry an output's ALSA address on a node this
// operator declared. A node that WirePlumber's ALSA monitor built
// carries alsa.card and alsa.device, which come from the udev device
// this operator has none of, so it publishes the same two numbers
// under its own names and readSinks reads those first.
const (
	nodeCardProperty = "liken.audio.card"
	nodePCMProperty  = "liken.audio.pcm"
)

// nodeNamePrefix begins every node name this operator declares. The
// name is what a consumer's PIPEWIRE_NODE carries, so it must not
// collide with a node that anything else in the graph created.
const nodeNamePrefix = "liken.audio."

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

// sinkNodeName is the PipeWire node name for one output. It is derived
// from the ALSA address and nothing else, so it is the same at every
// start on the same card, which the sink name a monitor-built node
// carries is not.
func sinkNodeName(card, pcm int) string {
	return nodeNamePrefix + deviceName(card, pcm)
}

// nodeConfig builds the whole drop-in for one card's outputs.
//
// Every playback PCM device gets a node, including an HDMI PCM with no
// monitor on it. The PCM device is what the card has, and a monitor is
// what somebody plugs into it, so a node set that followed the cables
// would change under a running graph and a node set that follows the
// card does not. An HDMI output with no monitor still carries the
// disconnected and no-monitor taints, because those two read the ELD
// and not the node, so nothing schedules onto it while the cable is
// out. Its node is there, so it carries no no-sink taint.
func nodeConfig(outputs []alsaOutput) string {
	sorted := slices.Clone(outputs)
	slices.SortFunc(sorted, func(a, b alsaOutput) int {
		if a.Card != b.Card {
			return a.Card - b.Card
		}
		return a.PCM - b.PCM
	})

	objects := make([]staticNode, 0, len(sorted))
	for _, output := range sorted {
		objects = append(objects, sinkObject(output.Card, output.PCM))
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
const configHeader = `# The claimed card's playback outputs, written by audio-operator
# before it started PipeWire. Editing this file achieves nothing: the
# operator writes it again at every start.
#
# WirePlumber's ALSA monitor enumerates cards through libudev, and a
# liken machine runs no udevd, so the monitor finds no card and builds
# no sink. The operator enumerates the card itself, through the ALSA
# control interface, and declares one sink node for each playback PCM
# device here. The monitor stays off, on every host, so the graph has
# one source.
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
// api.alsa.pcm.card carries the card number on its own, because the
// node opens the card's control interface for its mixer elements and
// reads the number from that property rather than from the path.
//
// media.class is set here and nowhere else. The ALSA plugin keeps a
// media class of its own inside the SPA node, and the adapter module
// copies no default onto the PipeWire node, so a node declared without
// it is a node that no client and no session manager sees as a sink.
//
// The channel layout is stereo on every output. A monitor's ELD
// reports how many uncompressed channels it accepts, and the operator
// publishes that number as an attribute, but the ELD is readable only
// while the monitor is connected and this file is written once at
// start. A layout that came from a cable would give one graph when the
// monitor is plugged in at boot and another when somebody plugs it in
// later. Stereo is what every HDMI monitor and every analog jack
// accepts, so it is the layout that is true at every start.
func sinkObject(card, pcm int) staticNode {
	return staticNode{
		Factory: "adapter",
		Flags:   []string{nofail},
		Args: map[string]string{
			"factory.name":      "api.alsa.pcm.sink",
			"api.alsa.path":     fmt.Sprintf("hw:%d,%d", card, pcm),
			"api.alsa.pcm.card": fmt.Sprint(card),
			"node.name":         sinkNodeName(card, pcm),
			"node.description": fmt.Sprintf("liken audio output, ALSA card %d device %d",
				card, pcm),
			"media.class":    "Audio/Sink",
			"audio.channels": "2",
			"audio.position": "FL,FR",
			// The two numbers readSinks maps a node back to. The adapter
			// module hands the whole args dictionary to the node it
			// creates, and a node's info block carries its whole property
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
}

// writeNodeConfig generates the drop-in and writes it where PipeWire
// reads it. It returns the document it wrote, which the reconcile pass
// compares against to notice a card whose PCM devices changed under a
// running PipeWire.
//
// The write happens before the daemons start. PipeWire builds
// context.objects while it loads its configuration, so a fragment that
// arrives later is not read, and the operator starts the daemons
// itself, which makes generate-then-start one ordinary sequence rather
// than a synchronization problem.
func writeNodeConfig(outputs []alsaOutput) (string, error) {
	document := nodeConfig(outputs)
	if err := os.MkdirAll(pipewireConfigDir, 0o755); err != nil {
		return "", fmt.Errorf("making %s: %w", pipewireConfigDir, err)
	}
	path := filepath.Join(pipewireConfigDir, dropInName)
	if err := os.WriteFile(path, []byte(document), 0o644); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	fmt.Printf("declared %d sink node(s) to PipeWire in %s\n", len(outputs), path)
	return document, nil
}
