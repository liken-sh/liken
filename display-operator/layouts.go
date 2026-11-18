package main

// The Layout resource divides one screen into regions. Each region is
// a rectangle given as fractions of the screen, with a label selector
// for the claims shown in it. A Display refers to the Layout it shows.
// A Layout names no namespace or monitor, so it is cluster-scoped and
// one Layout fits any screen.
//
// A person or a tool writes Layouts. The operator only reads them, and
// writes the result for each screen in the Display's status.
//
// These structs have only the fields this operator reads. The CRD in
// deploy/layouts.yaml is the full schema.

import (
	"context"

	"k8s.io/client-go/dynamic"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// LayoutsPath is the Layout collection, in the same API group and
// version as Display.
const LayoutsPath = "/apis/" + DisplayGroup + "/" + DisplayVersion + "/layouts"

// LayoutResolvedCondition is the condition on a Display that reports
// whether its Layout was found. Its reasons are in reasons.go.
const LayoutResolvedCondition = "LayoutResolved"

type Layout struct {
	APIVersion string     `json:"apiVersion,omitempty"`
	Kind       string     `json:"kind,omitempty"`
	Metadata   LayoutMeta `json:"metadata"`
	Spec       LayoutSpec `json:"spec"`
}

// LayoutMeta has no resourceVersion because the operator never
// writes a Layout back.
type LayoutMeta struct {
	Name string `json:"name"`
}

type LayoutSpec struct {
	Regions []LayoutRegion `json:"regions,omitempty"`
}

// LayoutRegion is one region of the screen. Its position in the list
// is its place in the stacking order, with the last on top. The CRD
// marks the list atomic, because server-side apply can reorder a list
// of type map and so change which region is on top.
type LayoutRegion struct {
	Name       string            `json:"name"`
	Rect       LayoutRect        `json:"rect"`
	Selector   LabelSelector     `json:"selector"`
	Transition *LayoutTransition `json:"transition,omitempty"`
}

// LayoutRect is a rectangle in fractions of the screen, so one Layout
// fits a 4K television and a 1080p panel.
type LayoutRect struct {
	Left   float64 `json:"left"`
	Top    float64 `json:"top"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// LayoutTransition is how a surface enters a region and how it leaves.
// The compositor animates both, because the workload does not know
// where its surface is on screen. Enter runs when a surface arrives in
// the region. Exit runs when a surface that is still drawing stops
// matching the region. When a client exits, its surface is gone, so
// no exit runs.
type LayoutTransition struct {
	Enter LayoutTransitionHalf `json:"enter"`
	Exit  LayoutTransitionHalf `json:"exit"`
}

// LayoutTransitionHalf is the enter or exit half of a transition: its
// kind and its duration. A half with no kind does nothing, so a region
// can have an enter transition and no exit transition.
type LayoutTransitionHalf struct {
	Kind         string `json:"kind,omitempty"`
	Milliseconds int    `json:"milliseconds,omitempty"`
}

// LabelSelector mirrors the Kubernetes label selector, with only the
// fields this program reads. The matching code is in placement.go.
type LabelSelector struct {
	MatchLabels      map[string]string          `json:"matchLabels,omitempty"`
	MatchExpressions []LabelSelectorRequirement `json:"matchExpressions,omitempty"`
}

type LabelSelectorRequirement struct {
	Key      string   `json:"key"`
	Operator string   `json:"operator"`
	Values   []string `json:"values,omitempty"`
}

// The operators a LabelSelectorRequirement accepts.
const (
	selectorIn           = "In"
	selectorNotIn        = "NotIn"
	selectorExists       = "Exists"
	selectorDoesNotExist = "DoesNotExist"
)

func getLayout(c *apiclient.Client, name string) (*Layout, error) {
	return apiclient.Get[Layout](c, LayoutsPath+"/"+name)
}

// openLayouts watches Layouts and wakes the operator's loop when one
// changes. The returned collection holds every Layout, and the
// placement pass reads Layouts from it (objectcache.go).
func openLayouts(ctx context.Context, client dynamic.Interface, wake func(), readings *metrics) *informer.Collection {
	return wakeWatch(ctx, client, kindLayout, informer.Source{Resource: layoutResource}, nil, wake, readings)
}
