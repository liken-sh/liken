package kubernetes

// Reading, watching, and reporting on Machines: the verbs both
// operators share. The machine operator reads and writes its own
// Machine; the cluster operator reads and watches them all.

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/chrisguidry/liken/machine"
)

func GetMachine(c *Client, name string) (*machine.Machine, error) {
	m := &machine.Machine{}
	if err := c.RequestJSON(http.MethodGet, MachinesPath+"/"+name, nil, m); err != nil {
		return nil, err
	}
	return m, nil
}

func ListMachines(c *Client) ([]machine.Machine, error) {
	var list struct {
		Items []machine.Machine `json:"items"`
	}
	if err := c.RequestJSON(http.MethodGet, MachinesPath, nil, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// PublishStatus writes through the status subresource: a separate
// endpoint (…/machines/<name>/status) that updates *only* the status
// half of the object. That means a controller can never accidentally
// rewrite the spec it is acting on, and RBAC can grant the two halves
// separately. The write is a PUT carrying the object's resourceVersion:
// if anything else changed the object in between, the server answers
// 409 Conflict instead of applying our stale copy. The caller's next
// pass re-reads and tries again. This is optimistic concurrency, and
// it is how every Kubernetes controller handles contention.
func PublishStatus(c *Client, m *machine.Machine, status *machine.MachineStatus) error {
	updated := *m
	updated.Status = *status
	body, err := json.Marshal(&updated)
	if err != nil {
		return err
	}
	path := MachinesPath + "/" + m.Metadata.Name + "/status"
	return c.RequestJSON(http.MethodPut, path, body, nil)
}

// WatchMachines turns the API server's watch mechanism into a channel
// of fresh Machine objects. A watch is an ordinary GET with
// ?watch=true: the response never ends, and each line of it is a JSON
// event like {"type": "MODIFIED", "object": {…}}, pushed the moment
// the object changes. This is the mechanism informers, kubectl get -w,
// and every controller's responsiveness are built on.
//
// fieldSelector scopes the watch, and it is where the two operators
// part ways: the machine operator passes metadata.name=<self>,
// because its own object is the only one whose changes concern it,
// while the cluster operator passes "" and hears the whole fleet,
// because the Cluster's status is derived from every Machine. The
// server does the filtering either way; a selector is just a query
// parameter on the same request.
//
// resourceVersion tells the server where to resume so no change is
// missed between reconnects; when history has been compacted away the
// server says 410 Gone. Stream drops are routine too (the server ends
// watches on its own schedule). Both recover the same way, the one
// informers use: list the collection and watch from the *list's*
// resourceVersion, which is the current revision of the world. A
// single object's version would not do here: it is the revision of
// that object's own last write, and on a quiet object that can be old
// enough to have been compacted away, which would earn another 410
// and strand the loop. The recovery list's items are delivered as
// events, so the caller's working copy is refreshed along the way.
//
// allowWatchBookmarks asks the server to send an occasional BOOKMARK
// event: no object change, just "you are current through version X."
// A watch on a quiet fleet would otherwise sit on an ever-staler
// resourceVersion, and the next reconnect would be more likely to
// find that version compacted away (the 410 above). Bookmarks keep
// the resume point fresh for free; informers request them for
// exactly this reason.
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
					// Usually 410 Gone wrapped in an event; fall back to
					// a fresh list below.
					break
				}
				resourceVersion = event.Object.Metadata.ResourceVersion
				if event.Type == "BOOKMARK" {
					// A bookmark only refreshes the resume point; there
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
