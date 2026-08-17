package main

// Reading PipeWire's graph, and running the daemons that hold it.
//
// The operator needs one fact from PipeWire that ALSA cannot give it:
// the node name of the sink that plays through each PCM device. That
// name is what a consumer's PIPEWIRE_NODE carries, and WirePlumber
// builds it from the card and the profile, so the name exists only in
// the running graph.
//
// The graph is read by running pw-dump, which ships with PipeWire in
// the same image, and parsing the JSON it prints. The alternative is
// to speak PipeWire's native protocol, which means implementing its
// binary POD encoding for every message the graph walk needs. The
// exec costs one process for each reconcile pass, at most one every
// settle window, and it reads a format that the PipeWire release in
// this image defines.

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

// pcmAddress names one playback PCM device, which is what ties a
// PipeWire sink to the ALSA output the operator publishes.
type pcmAddress struct {
	Card int
	PCM  int
}

// The property keys that carry a sink's ALSA address, in the order
// this operator reads them.
//
// The first key of each list is the one the operator writes itself,
// which is the only pair it can rely on: it declares every sink node
// in a configuration drop-in (see nodes.go), and a declared node
// carries no alsa.card and no alsa.device, because those two come from
// the udev device that WirePlumber's ALSA monitor builds and this
// graph has no monitor in it.
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
// object in the dump carries a type and, for the ones that came from
// a remote interface, an info block with its properties.
type pwObject struct {
	Type string `json:"type"`
	Info struct {
		Props map[string]json.RawMessage `json:"props"`
	} `json:"info"`
}

// readSinks runs pw-dump and returns the node name of the sink for
// each PCM device that has one.
func readSinks(ctx context.Context) (map[pcmAddress]string, error) {
	ctx, cancel := context.WithTimeout(ctx, pwDumpTimeout)
	defer cancel()

	command := exec.CommandContext(ctx, "pw-dump")
	// The context's kill bounds the exec, and WaitDelay bounds the
	// read after the kill: without it, Output blocks past the kill if
	// anything inherited the stdout pipe.
	command.WaitDelay = time.Second
	raw, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("running pw-dump: %w", err)
	}
	return parseSinks(raw)
}

// parseSinks reads the sinks out of one pw-dump document.
//
// A sink with no ALSA address is not an error. PipeWire publishes
// sinks that no card backs, such as a null sink or a network stream,
// and this operator publishes only the outputs the claimed card has.
//
// When two sinks name one PCM device, the first name in alphabetical
// order wins. The pair is what a profile change makes for a moment,
// and a stable choice keeps the operator from writing the slice twice
// for one event.
func parseSinks(document []byte) (map[pcmAddress]string, error) {
	var objects []pwObject
	if err := json.Unmarshal(document, &objects); err != nil {
		return nil, fmt.Errorf("reading pw-dump's output: %w", err)
	}

	sinks := map[pcmAddress]string{}
	for _, object := range objects {
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
		card, ok := numericProperty(object.Info.Props, sinkCardKeys)
		if !ok {
			continue
		}
		pcm, ok := numericProperty(object.Info.Props, sinkPCMKeys)
		if !ok {
			continue
		}
		address := pcmAddress{Card: card, PCM: pcm}
		if existing, taken := sinks[address]; taken && existing < name {
			continue
		}
		sinks[address] = name
	}
	return sinks, nil
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
