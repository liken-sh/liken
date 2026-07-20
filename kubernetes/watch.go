package kubernetes

// This file implements the watch protocol: how a controller learns
// about changes the moment they happen, and how it recovers when the
// stream drops.

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/liken-sh/liken/machine"
)

// WatchMachines turns the API server's watch mechanism into a channel
// of fresh Machine objects. A watch is an ordinary GET request with
// ?watch=true. The response never ends, and each line of it is a
// JSON event, such as {"type": "MODIFIED", "object": {…}}, sent the
// moment the object changes. This is the mechanism that informers,
// kubectl get -w, and every controller's responsiveness are built on.
//
// fieldSelector limits the scope of the watch, and this is where the
// two operators differ. The machine operator passes
// metadata.name=<self>, because its own object is the only one whose
// changes concern it. The cluster operator passes "" and hears about
// the whole fleet, because the Cluster's status is derived from
// every Machine. The server filters the results either way; a
// selector is only a query parameter on the same request.
//
// resourceVersion tells the server where to resume, so no change is
// missed between reconnects. When history has been compacted away,
// the server answers with 410 Gone. Stream drops are also routine,
// because the server ends watches on its own schedule. Both cases
// recover the same way, the way informers do: list the collection
// and watch again from the list's own resourceVersion, which is the
// current revision of the whole collection. A single object's
// version does not work here. That version is the revision of that
// object's own last write, and on a quiet object, that revision can
// be old enough to have been compacted away. Using it would earn
// another 410 and leave the loop stuck. The recovery list's items
// are delivered as events, so the caller's working copy is
// refreshed along the way.
//
// allowWatchBookmarks asks the server to send an occasional BOOKMARK
// event: no object change, just a signal that says "you are current
// through version X." Without this, a watch on a quiet fleet would
// sit on an increasingly old resourceVersion, and the next reconnect
// would more likely find that version already compacted away (see
// the 410 case above). Bookmarks keep the resume point fresh at no
// extra cost; informers request them for this reason.
func WatchMachines(c *Client, fieldSelector, resourceVersion string, events chan<- *machine.Machine) {
	selector := ""
	if fieldSelector != "" {
		selector = "&fieldSelector=" + url.QueryEscape(fieldSelector)
	}
	for {
		path := MachinesPath +
			"?watch=true&allowWatchBookmarks=true" +
			"&resourceVersion=" + resourceVersion + selector

		resp, err := c.Do(http.MethodGet, path, "", nil)
		if err == nil && resp.StatusCode == http.StatusOK {
			decoder := json.NewDecoder(resp.Body)
			for {
				var event struct {
					Type   string          `json:"type"`
					Object machine.Machine `json:"object"`
				}
				if err := decoder.Decode(&event); err != nil {
					break
				}
				if event.Type == "ERROR" {
					// Usually a 410 Gone status wrapped in an event. Fall
					// back to a fresh list below.
					break
				}
				resourceVersion = event.Object.Metadata.ResourceVersion
				if event.Type == "BOOKMARK" {
					// A bookmark only refreshes the resume point. There
					// is no change to reconcile.
					continue
				}
				events <- &event.Object
			}
		}
		if resp != nil {
			resp.Body.Close()
		}

		RetryPause()
		var list struct {
			Metadata struct {
				ResourceVersion string `json:"resourceVersion"`
			} `json:"metadata"`
			Items []machine.Machine `json:"items"`
		}
		listPath := MachinesPath
		if selector != "" {
			listPath += "?fieldSelector=" + url.QueryEscape(fieldSelector)
		}
		if err := c.RequestJSON(http.MethodGet, listPath, nil, &list); err == nil {
			resourceVersion = list.Metadata.ResourceVersion
			for i := range list.Items {
				events <- &list.Items[i]
			}
		}
	}
}
