package main

// The reconcile pass parses the PipeWire drop-in that the operator
// wrote, to find which PCM devices have a node and the channel layout
// of each sink. PipeWire reads context.objects only when it loads its
// configuration, so the drop-in lists the nodes that PipeWire created
// at its start, unless the operator rewrote it since
// (declarationloaded.go). nodes.go writes the node array as JSON, which
// is also valid SPA-JSON, so encoding/json parses it.

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// declaredNode is one node of the drop-in. A source node has no
// layout.
type declaredNode struct {
	Address nodeAddress
	Layout  channelLayout
}

// parseDeclaration fails on a node whose card or PCM device number is
// missing or not a number, because the operator writes both for every
// node.
func parseDeclaration(document string) ([]declaredNode, error) {
	start := strings.Index(document, configPrefix)
	if start < 0 {
		return nil, fmt.Errorf("the declaration contains no %q", strings.TrimSpace(configPrefix))
	}
	var objects []staticNode
	if err := json.Unmarshal([]byte(document[start+len(configPrefix):]), &objects); err != nil {
		return nil, fmt.Errorf("reading the declaration: %w", err)
	}
	nodes := make([]declaredNode, 0, len(objects))
	for _, object := range objects {
		card, cardErr := strconv.Atoi(object.Args[nodeCardProperty])
		pcm, pcmErr := strconv.Atoi(object.Args[nodePCMProperty])
		if cardErr != nil || pcmErr != nil {
			return nil, fmt.Errorf("the node %q has no card and PCM device number", object.Args["node.name"])
		}
		direction := mediaClassDirections[object.Args["media.class"]]
		node := declaredNode{Address: nodeAddress{pcmAddress: pcmAddress{Card: card, PCM: pcm}, Direction: direction}}
		if direction == directionSink {
			node.Layout = declaredLayout(object.Args)
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// declaredLayout parses the layout that layoutArgs wrote into a node's
// args, with audio.position in the form "[ FL FR ]". A sink with no
// layoutSourceProperty has layoutNone.
func declaredLayout(args map[string]string) channelLayout {
	source := layoutSource(args[layoutSourceProperty])
	if source == "" {
		return channelLayout{Source: layoutNone}
	}
	positions := strings.Fields(strings.Trim(args["audio.position"], "[] "))
	if len(positions) == 0 {
		positions = nil
	}
	return channelLayout{Source: source, Positions: positions, Encoded: declaredEncoded(args)}
}

func declaredLayouts(nodes []declaredNode) map[nodeAddress]channelLayout {
	layouts := map[nodeAddress]channelLayout{}
	for _, node := range nodes {
		if node.Address.Direction == directionSink {
			layouts[node.Address] = node.Layout
		}
	}
	return layouts
}

// declaredEndpoints returns the endpoints that the drop-in was written
// for, so that a layout change rewrites it with nodeConfig and adds or
// removes no node. PipeWire does not read a new set of PCM devices
// until the pod is replaced.
func declaredEndpoints(nodes []declaredNode) []alsaEndpoint {
	endpoints := make([]alsaEndpoint, 0, len(nodes))
	for _, node := range nodes {
		endpoints = append(endpoints, alsaEndpoint{
			Card:    node.Address.Card,
			PCM:     node.Address.PCM,
			Capture: node.Address.Direction == directionSource,
		})
	}
	return endpoints
}

// samePCMDevices compares PCM devices and directions, and ignores
// layouts and order.
func samePCMDevices(nodes []declaredNode, outputs []alsaEndpoint) bool {
	declared := map[nodeAddress]bool{}
	for _, node := range nodes {
		declared[node.Address] = true
	}
	current := map[nodeAddress]bool{}
	for _, output := range outputs {
		current[output.graphAddress()] = true
	}
	return slices.Equal(sortedAddresses(declared), sortedAddresses(current))
}

// sortedAddresses sorts by card, PCM device, and direction, so that
// slices.Equal can compare two sets.
func sortedAddresses(set map[nodeAddress]bool) []nodeAddress {
	return slices.SortedFunc(maps.Keys(set), func(a, b nodeAddress) int {
		if a.Card != b.Card {
			return a.Card - b.Card
		}
		if a.PCM != b.PCM {
			return a.PCM - b.PCM
		}
		return strings.Compare(string(a.Direction), string(b.Direction))
	})
}
