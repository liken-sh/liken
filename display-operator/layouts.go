package main

// The Layout resource: the regions of one screen, each a fraction of
// it with a label selector. A Display names the Layout it shows.
//
// A Layout is cluster-scoped because nothing in it names a namespace
// or a monitor. Its rectangles are fractions and its selectors match
// labels, so one Layout serves a screen in one building and a screen
// in another, the way a Keymap does. The order of the regions is the
// stacking order, last on top, which is why the CRD marks the list
// atomic: a map-typed list would let server-side apply reorder the
// regions and restack the screen.
//
// The operator only reads Layouts. A person or a tool writes them,
// and what a screen made of one is on the Display's status, not on
// the Layout.
//
// These structs hold only the fields this operator reads; the CRD in
// deploy/layouts.yaml is the full schema.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// The Layout collection, under the same group and version the
// Display resource is served at.
const LayoutsPath = "/apis/" + DisplayGroup + "/" + DisplayVersion + "/layouts"

// The condition a Display carries for the Layout it names, and the
// reason a name that resolves to nothing carries. The screen shows the
// default layout in that state, so the condition is the only report of
// the name that failed.
const (
	LayoutResolvedCondition = "LayoutResolved"
	LayoutNotFoundReason    = "LayoutNotFound"
	// The two reasons the condition carries when it is met: the screen
	// shows the Layout it names, or it names none and shows the
	// default. They are two states of one condition, because a reader
	// asking why a screen is arranged as it is needs to know which.
	LayoutFoundReason   = "LayoutFound"
	DefaultLayoutReason = "DefaultLayout"
)

type Layout struct {
	APIVersion string     `json:"apiVersion,omitempty"`
	Kind       string     `json:"kind,omitempty"`
	Metadata   LayoutMeta `json:"metadata"`
	Spec       LayoutSpec `json:"spec"`
}

type LayoutList struct {
	Items []Layout `json:"items"`
}

// The operator never writes a Layout, so there is no
// resourceVersion here: nothing of this resource is read to be
// written back.
type LayoutMeta struct {
	Name string `json:"name"`
}

type LayoutSpec struct {
	Regions []LayoutRegion `json:"regions,omitempty"`
}

// One region of the screen. Its position in the list is its position
// in the stacking order, last on top.
type LayoutRegion struct {
	Name       string            `json:"name"`
	Rect       LayoutRect        `json:"rect"`
	Selector   LabelSelector     `json:"selector"`
	Transition *LayoutTransition `json:"transition,omitempty"`
}

// A rectangle in fractions of the screen, so one Layout fits a 4K
// television and a 1080p panel without a second copy.
type LayoutRect struct {
	Left   float64 `json:"left"`
	Top    float64 `json:"top"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// How a surface enters the region and how it leaves. The compositor
// animates both, because the workload never knows where it is. The
// enter half runs when a surface arrives in the region, and the exit
// half runs when a surface that is still drawing stops matching it.
// A client that exits takes its surface with it, and no exit runs for
// a surface the compositor no longer holds.
type LayoutTransition struct {
	Enter LayoutTransitionHalf `json:"enter"`
	Exit  LayoutTransitionHalf `json:"exit"`
}

// One half of a transition: what it does, over how long. A half with
// no kind does nothing, so a region may state an entrance and no
// exit.
type LayoutTransitionHalf struct {
	Kind         string `json:"kind,omitempty"`
	Milliseconds int    `json:"milliseconds,omitempty"`
}

// The label selector of the upstream API, held here for the reason
// every other struct in this program is held here: this program reads
// these fields and no others. The matching is in placement.go.
type LabelSelector struct {
	MatchLabels      map[string]string          `json:"matchLabels,omitempty"`
	MatchExpressions []LabelSelectorRequirement `json:"matchExpressions,omitempty"`
}

type LabelSelectorRequirement struct {
	Key      string   `json:"key"`
	Operator string   `json:"operator"`
	Values   []string `json:"values,omitempty"`
}

// The four operators the schema states.
const (
	selectorIn           = "In"
	selectorNotIn        = "NotIn"
	selectorExists       = "Exists"
	selectorDoesNotExist = "DoesNotExist"
)

func getLayout(c *Client, name string) (*Layout, error) {
	return get[Layout](c, LayoutsPath+"/"+name)
}

func listLayouts(c *Client) ([]Layout, error) {
	list, err := get[LayoutList](c, LayoutsPath)
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

// The watch turns a Layout a person edited into one wake. It keeps
// the bounds the Display watch keeps, because what they bound is the
// API server's own behavior and not anything about either resource.
func watchLayouts(ctx context.Context, c *Client, wake func(), readings *metrics) {
	first := true
	for ctx.Err() == nil {
		if !first {
			// The API server closed the last connection and this one
			// opens in its place, the one restart milestone 65 counts.
			readings.watchRestarted(kindLayout)
		}
		first = false
		if err := streamLayouts(ctx, c, wake); err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "watching layouts: %v\n", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(displayWatchRetry):
		}
	}
}

// One watch connection. It starts at the present, because an event
// carries nothing the pass uses and a missed event costs one backstop
// tick.
func streamLayouts(ctx context.Context, c *Client, wake func()) error {
	path := fmt.Sprintf("%s?watch=true&timeoutSeconds=%d", LayoutsPath, int(displayWatchTimeout.Seconds()))
	body, err := c.Watch(ctx, path)
	if err != nil {
		return err
	}
	defer drain(body)

	events := json.NewDecoder(body)
	for {
		var event struct {
			Type string `json:"type"`
		}
		if err := events.Decode(&event); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		wake()
	}
}
