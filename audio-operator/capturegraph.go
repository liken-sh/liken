package main

// Reading the graph for a tap: which node carries the endpoint the
// request named, what rate and channel count the tap runs at, and
// whether the stream that started linked to the node the request
// asked for.
//
// The container resolves the node before it taps because pw-record
// never refuses a bad target. With stream.capture.sink set and an
// unknown name it links to the default sink's monitor, and without
// the property a sink name links to a microphone. So an unresolved
// name is a 404 here rather than another endpoint's sound on the
// wire.
//
// The rate and channel count come from the graph. The node's Format
// is what it negotiated while it runs. A suspended node prints an
// empty Format, so the channel count then comes from the node's
// EnumFormat, which is what the hardware offers, and the rate comes
// from the graph's own settings metadata. A suspended 5.1 sink is
// never guessed as stereo.

import (
	"encoding/json"
	"fmt"
)

// settingsMetadata is the metadata object that holds the graph's own
// settings, and clockRateKey is the key in it that names the rate
// the graph runs at, which every node resamples to.
const (
	settingsMetadata = "settings"
	clockRateKey     = "clock.rate"
)

// captureObject is the part of one pw-dump object a tap reads. It is
// separate from pwObject because the questions differ: the reconcile
// loop asks which PCM device a node serves, and a tap asks which node
// carries one name, what it runs at, and what it is linked to.
type captureObject struct {
	ID       int                        `json:"id"`
	Type     string                     `json:"type"`
	Props    map[string]json.RawMessage `json:"props"`
	Info     *captureInfo               `json:"info"`
	Metadata []captureMetadata          `json:"metadata"`
}

type captureInfo struct {
	Props        map[string]json.RawMessage `json:"props"`
	Params       captureParams              `json:"params"`
	OutputNodeID int                        `json:"output-node-id"`
	InputNodeID  int                        `json:"input-node-id"`
}

type captureParams struct {
	Format     []json.RawMessage `json:"Format"`
	EnumFormat []json.RawMessage `json:"EnumFormat"`
}

type captureMetadata struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// captureFormat is what one tap runs at: the node it targets, and the
// rate and channel count every command on the pipeline is given.
type captureFormat struct {
	NodeID   int
	Rate     int
	Channels int
}

// defaultCaptureRate is the rate a tap runs at when the graph reports
// none at all: no running node, and no settings metadata published.
// 48000 is PipeWire's own default clock.rate.
const defaultCaptureRate = 48000

// defaultCaptureChannels is the channel count a tap runs at when
// neither Format nor EnumFormat names one.
const defaultCaptureChannels = 2

// resolveNode finds the node one request names, and the format a tap
// on it runs at. A name the graph does not hold is what the container
// answers 404 to.
func resolveNode(document []byte, name string, direction pwDirection) (captureFormat, error) {
	objects, err := parseCaptureObjects(document)
	if err != nil {
		return captureFormat{}, err
	}
	rate := graphRate(objects)
	for _, object := range objects {
		if object.Type != "PipeWire:Interface:Node" || object.Info == nil {
			continue
		}
		if property(object.Info.Props, "node.name") != name {
			continue
		}
		if mediaClassDirections[property(object.Info.Props, "media.class")] != direction {
			continue
		}
		format := nodeFormat(object.Info.Params.Format)
		found := captureFormat{NodeID: object.ID, Rate: format.Rate, Channels: format.Channels}
		if found.Rate == 0 {
			found.Rate = rate
		}
		if found.Channels == 0 {
			found.Channels = enumChannels(object.Info.Params.EnumFormat)
		}
		if found.Channels == 0 {
			found.Channels = defaultCaptureChannels
		}
		return found, nil
	}
	return captureFormat{}, fmt.Errorf("PipeWire holds no %s node named %s", direction, name)
}

// linkState is where the stream this container started ended up.
type linkState int

const (
	linkNone linkState = iota
	linkOnTarget
	linkElsewhere
)

// confirmLink says whether the stream one tap started is linked to the
// node the request named, to some other node, or to nothing yet. One
// walk of the graph answers all three, because a poll reads the whole
// dump anyway.
//
// A sink tap reads the sink's monitor ports, so the target is the
// link's output and the stream is its input. A source tap reads the
// source's output ports, and the ends are the same way round. So one
// read covers both directions.
//
// The stream is found by the node.name this container gave it. Every
// client of this socket arrives with the access flatpak, because the
// kernel cannot translate a peer's pid across PID namespaces, so
// PipeWire sets no application.process.id to match on and
// application.name is pw-record for every tap on the node.
func confirmLink(document []byte, stream string, targetNodeID int) (linkState, error) {
	objects, err := parseCaptureObjects(document)
	if err != nil {
		return linkNone, err
	}
	streams := map[int]bool{}
	for _, object := range objects {
		if object.Type != "PipeWire:Interface:Node" || object.Info == nil {
			continue
		}
		if property(object.Info.Props, "node.name") == stream {
			streams[object.ID] = true
		}
	}
	if len(streams) == 0 {
		return linkNone, nil
	}
	state := linkNone
	for _, object := range objects {
		if object.Type != "PipeWire:Interface:Link" || object.Info == nil {
			continue
		}
		if !streams[object.Info.InputNodeID] {
			continue
		}
		if object.Info.OutputNodeID == targetNodeID {
			return linkOnTarget, nil
		}
		state = linkElsewhere
	}
	return state, nil
}

func parseCaptureObjects(document []byte) ([]captureObject, error) {
	var objects []captureObject
	if err := json.Unmarshal(document, &objects); err != nil {
		return nil, fmt.Errorf("reading pw-dump's output: %w", err)
	}
	return objects, nil
}

// graphRate reads clock.rate out of the settings metadata, which is
// the rate every node in the graph runs at while nothing forces
// another. A graph with no settings object runs at PipeWire's
// default.
func graphRate(objects []captureObject) int {
	for _, object := range objects {
		if object.Type != "PipeWire:Interface:Metadata" {
			continue
		}
		if property(object.Props, "metadata.name") != settingsMetadata {
			continue
		}
		for _, entry := range object.Metadata {
			if entry.Key != clockRateKey {
				continue
			}
			if rate := numericValue(string(entry.Value)); rate > 0 {
				return rate
			}
		}
	}
	return defaultCaptureRate
}

// enumChannels reads the channel count the hardware offers. pw-dump
// prints the field as a plain number, or as a choice whose default is
// the value the node takes when nothing asks for another.
func enumChannels(params []json.RawMessage) int {
	for _, raw := range params {
		var block struct {
			Channels json.RawMessage `json:"channels"`
		}
		if err := json.Unmarshal(raw, &block); err != nil {
			continue
		}
		if channels := choiceValue(block.Channels); channels > 0 {
			return channels
		}
	}
	return 0
}

// choiceValue reads a SPA field that pw-dump printed either as a
// number or as a choice object with a default.
func choiceValue(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var number int
	if err := json.Unmarshal(raw, &number); err == nil {
		return number
	}
	var choice struct {
		Default int `json:"default"`
	}
	if err := json.Unmarshal(raw, &choice); err == nil {
		return choice.Default
	}
	return 0
}

// numericValue reads a whole number pw-dump printed with or without
// quotes, the way property does for a string. A property value
// arrives as a JSON string, and a metadata value arrives as raw JSON,
// so both spellings are read.
func numericValue(text string) int {
	var number int
	if err := json.Unmarshal([]byte(text), &number); err == nil {
		return number
	}
	var quoted string
	if err := json.Unmarshal([]byte(text), &quoted); err == nil {
		if err := json.Unmarshal([]byte(quoted), &number); err == nil {
			return number
		}
	}
	var direct int
	if _, err := fmt.Sscanf(text, "%d", &direct); err == nil {
		return direct
	}
	return 0
}
