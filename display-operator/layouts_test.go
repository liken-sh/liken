package main

// These tests cover the Layout client: the read of the one Layout a
// Display names, the listing a pass reads, and the watch that turns an
// edited Layout into a wake.

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

// The front desk of the plan, as the API server serves it. The
// fixture is JSON rather than a struct, because what these tests
// prove is that the field names on the wire reach the fields this
// program reads.
const frontDeskJSON = `{
  "apiVersion": "display.liken.sh/v1alpha1",
  "kind": "Layout",
  "metadata": {"name": "front-desk"},
  "spec": {"regions": [
    {"name": "notices",
     "rect": {"left": 0, "top": 0, "width": 0.7, "height": 1},
     "selector": {"matchLabels": {"panel": "notices"}}},
    {"name": "lot",
     "rect": {"left": 0.7, "top": 0, "width": 0.3, "height": 0.6},
     "selector": {"matchExpressions": [
       {"key": "panel", "operator": "In", "values": ["parking-lot"]}]},
     "transition": {"kind": "fade", "milliseconds": 300}}
  ]}
}`

func TestGetLayoutReadsTheLayoutADisplayNames(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != LayoutsPath+"/front-desk" {
			t.Errorf("the operator read %s, want the front-desk layout", r.URL.Path)
		}
		fmt.Fprint(w, frontDeskJSON)
	}))

	layout, err := getLayout(client, "front-desk")
	if err != nil {
		t.Fatal(err)
	}
	if layout.Metadata.Name != "front-desk" {
		t.Errorf("the layout is named %q, want front-desk", layout.Metadata.Name)
	}
	if len(layout.Spec.Regions) != 2 {
		t.Fatalf("the layout holds %d regions, want 2", len(layout.Spec.Regions))
	}
	notices := layout.Spec.Regions[0]
	if notices.Rect != (LayoutRect{Width: 0.7, Height: 1}) {
		t.Errorf("notices covers %+v, want the left seven tenths", notices.Rect)
	}
	if notices.Selector.MatchLabels["panel"] != "notices" {
		t.Errorf("notices selects %v, want panel=notices", notices.Selector.MatchLabels)
	}
	lot := layout.Spec.Regions[1]
	if transitionOf(lot) != (LayoutTransition{Kind: "fade", Milliseconds: 300}) {
		t.Errorf("lot transitions with %+v, want a 300 ms fade", transitionOf(lot))
	}
	if len(lot.Selector.MatchExpressions) != 1 || lot.Selector.MatchExpressions[0].Operator != selectorIn {
		t.Errorf("lot selects %+v, want one In requirement", lot.Selector.MatchExpressions)
	}
}

func TestListLayoutsReadsEveryLayout(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != LayoutsPath {
			t.Errorf("the operator read %s, want the layout collection", r.URL.Path)
		}
		fmt.Fprintf(w, `{"items":[%s]}`, frontDeskJSON)
	}))

	layouts, err := listLayouts(client)
	if err != nil {
		t.Fatal(err)
	}
	if len(layouts) != 1 || layouts[0].Metadata.Name != "front-desk" {
		t.Errorf("the listing held %+v, want the front-desk layout", layouts)
	}
}

// The watch turns each event into one wake, which is all the pass
// needs from it: the pass reads every Layout again.
func TestTheLayoutWatchWakesOnEveryEvent(t *testing.T) {
	events := 2
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("watch") != "true" {
			t.Errorf("the operator opened %s, want a watch", r.URL)
		}
		for i := range events {
			fmt.Fprintf(w, `{"type":"MODIFIED","object":{"metadata":{"name":"front-desk-%d"}}}`, i)
		}
	}))

	wakes := 0
	if err := streamLayouts(t.Context(), client, func() { wakes++ }); err != nil {
		t.Fatal(err)
	}
	if wakes != events {
		t.Errorf("the watch woke the loop %d times, want %d", wakes, events)
	}
}

// The watch reopens its connection until the context ends, and a
// context that ended is what stops it. Without that, a shutdown would
// wait out the retry.
func TestTheLayoutWatchEndsWithTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancel()
		w.WriteHeader(http.StatusInternalServerError)
	}))

	watchLayouts(ctx, client, func() {})
}
