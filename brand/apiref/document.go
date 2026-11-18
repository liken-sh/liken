// This file reads an OpenAPI document into a tree that keeps the
// order of each object's keys. The page lists paths, methods, and
// statuses in the order the document writes them, and a map from
// encoding/json loses that order.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// nodeKind is the JSON type of a node, so that an empty object, an
// empty array, and an empty string are different values.
type nodeKind int

const (
	scalarNode nodeKind = iota
	objectNode
	arrayNode
)

// member is one key of an object and its value.
type member struct {
	name  string
	value *node
}

// node is one JSON value. A scalar keeps its text as the document
// writes it, because the page prints every scalar as text in a table
// cell.
type node struct {
	kind    nodeKind
	members []member
	items   []*node
	text    string
}

// parse reads one JSON document. It fails on content after the
// top-level value, so a truncated or doubled file fails here and the
// page is not rendered from part of it.
func parse(data []byte) (*node, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	// UseNumber keeps a number as the digits in the file. A float64
	// would rewrite a large or precise number.
	decoder.UseNumber()
	root, err := parseValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("the file has content after its first JSON document")
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

// parseToken builds the value that starts with token, which the
// caller has already read. parseArray reads each token to check for
// the closing bracket first.
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
	return nil, fmt.Errorf("the document has %q where a value should start", delimiter)
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

// member returns the value of a key. It returns nil for a missing key
// and for a node that is nil or not an object, so lookups can chain
// without a check at each step.
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

// each calls visit for each member of an object, in document order.
func (n *node) each(visit func(name string, value *node)) {
	if n == nil || n.kind != objectNode {
		return
	}
	for _, m := range n.members {
		visit(m.name, m.value)
	}
}

// elements returns the values of an array, and nil for any other node.
func (n *node) elements() []*node {
	if n == nil || n.kind != arrayNode {
		return nil
	}
	return n.items
}

// value returns the text of a scalar, and "" for any other node.
func (n *node) value() string {
	if n == nil || n.kind != scalarNode {
		return ""
	}
	return n.text
}
