package main

// Reading the declaration PipeWire runs back out of the drop-in.
//
// PipeWire reads context.objects once, while it loads its
// configuration, so the drop-in on disk is the record of the nodes
// the running graph was built from. The reconcile pass reads two
// facts from it: which PCM devices have a node, and which channel
// layout each sink was declared with. The file is the strict JSON
// subset of SPA-JSON (nodes.go), so it reads back with encoding/json.

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// declaredNode is one node the declaration holds: the PCM device and
// direction it serves, and the layout it was declared with. A source
// node has no layout.
type declaredNode struct {
	Address nodeAddress
	Layout  channelLayout
}

// parseDeclaration reads every node out of one drop-in. A node whose
// card or PCM device number does not read is an error, because every
// node this operator writes carries both.
func parseDeclaration(document string) ([]declaredNode, error) {
	start := strings.Index(document, configPrefix)
	if start < 0 {
		return nil, fmt.Errorf("the declaration holds no %q", strings.TrimSpace(configPrefix))
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

// declaredLayout reads the layout layoutArgs wrote. The position list
// is in the "[ FL FR ]" form, and a sink with no source property was
// declared with no layout.
func declaredLayout(args map[string]string) channelLayout {
	source := layoutSource(args[layoutSourceProperty])
	if source == "" {
		return channelLayout{Source: layoutNone}
	}
	positions := strings.Fields(strings.Trim(args["audio.position"], "[] "))
	if len(positions) == 0 {
		positions = nil
	}
	return channelLayout{Source: source, Positions: positions}
}

// declaredLayouts holds the layout of every declared sink, keyed by
// its address in the graph.
func declaredLayouts(nodes []declaredNode) map[nodeAddress]channelLayout {
	layouts := map[nodeAddress]channelLayout{}
	for _, node := range nodes {
		if node.Address.Direction == directionSink {
			layouts[node.Address] = node.Layout
		}
	}
	return layouts
}

// declaredEndpoints rebuilds the endpoints a declaration was
// generated from, so that nodeConfig writes the same set of nodes
// with a new layout. A rewrite for a layout change keeps the set of
// PCM devices PipeWire runs, and only a replacement pod declares a
// new set.
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

// samePCMDevices reports whether the card's endpoints now are the
// endpoints the declaration holds a node for, whatever the layouts.
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

// sortedAddresses lists a set of addresses in a fixed order, so two
// sets compare as two lists.
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
