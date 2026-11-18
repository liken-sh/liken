package main

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

// frontDeskJSON is a Layout as the API server returns it. It is JSON
// and not a struct so that the test checks the field names on the wire
// decode into the fields this program reads.
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
     "transition": {"enter": {"kind": "fade", "milliseconds": 600},
                    "exit": {"kind": "fade", "milliseconds": 250}}}
  ]}
}`

func TestGetLayoutReadsTheLayoutADisplayReferences(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != LayoutsPath+"/front-desk" {
			t.Errorf("the operator read %s, want the front-desk layout path", r.URL.Path)
		}
		fmt.Fprint(w, frontDeskJSON)
	}))

	layout, err := getLayout(client, "front-desk")
	if err != nil {
		t.Fatalf("getting the layout front-desk: %v", err)
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
	want := LayoutTransition{
		Enter: LayoutTransitionHalf{Kind: "fade", Milliseconds: 600},
		Exit:  LayoutTransitionHalf{Kind: "fade", Milliseconds: 250},
	}
	if transitionOf(lot) != want {
		t.Errorf("lot transitions with %+v, want %+v", transitionOf(lot), want)
	}
	if len(lot.Selector.MatchExpressions) != 1 || lot.Selector.MatchExpressions[0].Operator != selectorIn {
		t.Errorf("lot selects %+v, want one In requirement", lot.Selector.MatchExpressions)
	}
}

// The watch retries a failed connection until the context ends. A
// cancelled context must stop it, or shutdown would wait for the next
// retry.
func TestTheLayoutWatchEndsWithTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	watcher := testWatcher(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancel()
		w.WriteHeader(http.StatusInternalServerError)
	}))

	<-openLayouts(ctx, watcher, func() {}, nil).Done()
}
