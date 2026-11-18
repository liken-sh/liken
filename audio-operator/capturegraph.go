package main

// A tap reads a pw-dump document to find the node that a request names
// and the rate and channel count to record at, and then to check that
// the stream it started is linked to that node.
//
// The capture container resolves the node before it starts a tap,
// because pw-record never refuses a bad target. With
// stream.capture.sink set to an unknown name it links to the default
// sink's monitor, and without the property a sink name links to a
// microphone. So a name that does not resolve returns 404, and the
// caller never gets another endpoint's sound.
//
// A running node's Format is the format that it negotiated. A suspended
// node prints an empty Format, so its channel count comes from its
// EnumFormat, which lists what the hardware offers, and its rate comes
// from the graph's settings metadata. So a suspended 5.1 sink is not
// recorded as stereo.

import (
	"encoding/json"
	"fmt"
)

// settingsMetadata names the metadata object that holds the graph's
// settings, and clockRateKey is its entry for the graph's rate.
const (
	settingsMetadata = "settings"
	clockRateKey     = "clock.rate"
)

// captureObject is the part of a pw-dump object that a tap reads. It is
// separate from pwObject because the reconcile loop reads which PCM
// device a node serves, and a tap reads a node's name, format, and
// links.
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

// captureFormat is the node that a tap records and the rate and channel
// count that each command of the tap's pipeline is given.
type captureFormat struct {
	NodeID   int
	Rate     int
	Channels int
}

// defaultCaptureRate is the rate a tap runs at when the graph reports
// none: no running node, and no settings metadata. 48000 is PipeWire's
// default clock.rate.
const defaultCaptureRate = 48000

// defaultCaptureChannels is the channel count a tap runs at when
// neither Format nor EnumFormat has one.
const defaultCaptureChannels = 2

// resolveNode returns the node with the given name and direction, and
// the format that a tap on it records at. It returns an error when the
// document does not parse or has no such node, and the capture
// container answers either error with 404.
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
	return captureFormat{}, fmt.Errorf("PipeWire has no %s node named %s", direction, name)
}

// linkState is what a tap's stream is linked to. linkNone is separate
// from linkElsewhere because PipeWire builds the link a moment after the
// stream starts.
type linkState int

const (
	linkNone linkState = iota
	linkOnTarget
	linkElsewhere
)

// confirmLink reports whether a tap's stream is linked to the requested
// node, to another node, or to nothing yet. A stream with links to both
// the requested node and another node returns linkOnTarget.
//
// A sink tap reads the sink's monitor ports and a source tap reads the
// source's output ports, so in both directions the target is the link's
// output node and the stream is its input node.
//
// The stream is found by the node.name that the capture container gave
// it. Every client of this socket arrives with the flatpak access,
// because the kernel cannot translate a peer's pid across PID
// namespaces. So PipeWire sets no application.process.id to match on,
// and application.name is pw-record for every tap.
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

// graphRate returns clock.rate from the settings metadata, or
// PipeWire's default when the graph has no settings object or no
// positive clock.rate.
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

// enumChannels returns the channel count that the hardware offers, from
// EnumFormat. pw-dump prints the field as a number, or as a choice
// object whose default is the count that the node uses when no client
// requests another.
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

// choiceValue reads a SPA field that pw-dump printed as a number or as
// a choice object, and returns the number or the choice's default.
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

// numericValue parses a whole number that pw-dump printed with or
// without quotes. A property value is a JSON string and a metadata
// value is raw JSON, so both forms occur.
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
