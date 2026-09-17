// The reader for an OpenAPI document.
//
// The page follows the document's own order: the paths in the order
// the file writes them, the methods under each path in that order,
// and the statuses under each method in that order. Go's
// encoding/json decodes an object into a map, and a map has no
// order, so this reader keeps each object's members as a list
// instead.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// nodeKind says which of the three shapes a node holds. An empty
// object, an empty array, and an empty string are all worth telling
// apart, so the shape is a field and not a count of members.
type nodeKind int

const (
	scalarNode nodeKind = iota
	objectNode
	arrayNode
)

// member is one key and its value, in the position the document
// writes them.
type member struct {
	name  string
	value *node
}

// node is one JSON value. A scalar keeps the text the document
// writes, because every scalar this page renders lands in a table
// cell as text: a number, a boolean, and a string all read the same
// there.
type node struct {
	kind    nodeKind
	members []member
	items   []*node
	text    string
}

// parse reads one JSON document. It refuses trailing content after
// the top-level value, so a truncated or doubled file fails here
// rather than rendering a page from half of it.
func parse(data []byte) (*node, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	// UseNumber keeps a number as the digits the file holds. Decoding
	// into float64 would render 3.1 as 3.1 and 200 as 200, but a
	// large or precise number would come back in a spelling the
	// document never wrote.
	decoder.UseNumber()
	root, err := parseValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("the file holds more than one JSON document")
	}
	return root, nil
}

func parseValue(decoder *json.Decoder) (*node, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	return parseToken(decoder, token)
}

func parseObject(decoder *json.Decoder) (*node, error) {
	object := &node{kind: objectNode}
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		name, isName := token.(string)
		if !isName {
			return object, nil
		}
		value, err := parseValue(decoder)
		if err != nil {
			return nil, err
		}
		object.members = append(object.members, member{name: name, value: value})
	}
}

func parseArray(decoder *json.Decoder) (*node, error) {
	array := &node{kind: arrayNode}
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		if token == json.Delim(']') {
			return array, nil
		}
		element, err := parseToken(decoder, token)
		if err != nil {
			return nil, err
		}
		array.items = append(array.items, element)
	}
}

// parseToken builds the value whose first token is already read.
// An array's loop reads a token to test it for the closing bracket,
// and hands the token here when it is not one.
func parseToken(decoder *json.Decoder, token json.Token) (*node, error) {
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return &node{kind: scalarNode, text: scalarText(token)}, nil
	}
	switch delimiter {
	case '{':
		return parseObject(decoder)
	case '[':
		return parseArray(decoder)
	}
	return nil, fmt.Errorf("unexpected %q", delimiter)
}

func scalarText(token json.Token) string {
	switch value := token.(type) {
	case string:
		return value
	case json.Number:
		return value.String()
	case bool:
		if value {
			return "true"
		}
		return "false"
	}
	return "null"
}

// member finds one key's value. A nil node and a missing key both
// return nil, so lookups chain without a check at each step.
func (n *node) member(name string) *node {
	if n == nil || n.kind != objectNode {
		return nil
	}
	for _, m := range n.members {
		if m.name == name {
			return m.value
		}
	}
	return nil
}

// each visits an object's members in document order.
func (n *node) each(visit func(name string, value *node)) {
	if n == nil || n.kind != objectNode {
		return
	}
	for _, m := range n.members {
		visit(m.name, m.value)
	}
}

// elements returns an array's values, and nothing for anything else.
func (n *node) elements() []*node {
	if n == nil || n.kind != arrayNode {
		return nil
	}
	return n.items
}

// value returns a scalar's text, and "" for an object or an array.
func (n *node) value() string {
	if n == nil || n.kind != scalarNode {
		return ""
	}
	return n.text
}
