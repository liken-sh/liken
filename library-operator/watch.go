package main

// A watch is an ordinary GET with watch=true whose response never
// ends: the API server holds the connection open and writes one JSON
// event per change, the same protocol liken's own operators speak.
//
// A watch carries no object to the loop. Every pass re-lists, so a
// change here is only a wake, and the loop decides what to read.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// A watch is a request whose response never ends, so it carries a context
// with no deadline and no cancel; the bounded contexts belong to the
// passes in operate.go. A watcher runs for the life of the process, and
// the process ending is what ends it.
func watchContext() context.Context {
	return context.Background()
}

// WatchRetryPause is how long a watcher waits before it re-lists after
// a dropped stream, and a variable so a test drives a reconnect in
// milliseconds.
var watchRetryPause = 2 * time.Second

// WatchLibraries resumes each stream from a resourceVersion, so no
// change is missed between reconnects. A 410 Gone and a routine stream
// end recover the same way: list the collection, wake the loop, and
// watch again from the list's own version.
//
// The list after every ended stream is what keeps the resume point
// current, so a bookmark's version matters only when that list itself
// fails. The watcher asks for bookmarks anyway because they cost one
// line each and make that failure window resumable, where a relist
// costs one full read of the collection and the pass the wake
// triggers.
func watchLibraries(c *Client, resourceVersion string, wake chan<- struct{}) {
	for {
		path := librariesPath + "?watch=true&allowWatchBookmarks=true&resourceVersion=" + resourceVersion
		resp, err := c.Do(watchContext(), http.MethodGet, path, nil)
		if err == nil && resp.StatusCode == http.StatusOK {
			resourceVersion = readWatchStream(resp, resourceVersion, wake)
		}
		if resp != nil {
			drain(resp.Body)
		}

		// A failed watch is never fatal. The ticker keeps the passes
		// running while this loop is down, and a relist is the whole
		// recovery.
		time.Sleep(watchRetryPause)
		list, err := ListLibraries(watchContext(), c)
		if err != nil {
			fmt.Fprintf(os.Stderr, "listing libraries to resume the watch: %v\n", err)
			continue
		}
		resourceVersion = list.Metadata.ResourceVersion
		poke(wake)
	}
}

// WatchCatalogs wakes the loop on every Catalog change, so a Library
// waiting on its namespace's Catalog proceeds on the next pass, and a
// second Catalog is marked Blocked without a backstop tick's delay. The
// recovery is watchLibraries's: a dropped stream or a 410 Gone lists the
// collection, wakes the loop, and resumes from the list's version.
func watchCatalogs(c *Client, resourceVersion string, wake chan<- struct{}) {
	for {
		path := catalogsPath + "?watch=true&allowWatchBookmarks=true&resourceVersion=" + resourceVersion
		resp, err := c.Do(watchContext(), http.MethodGet, path, nil)
		if err == nil && resp.StatusCode == http.StatusOK {
			resourceVersion = readWatchStream(resp, resourceVersion, wake)
		}
		if resp != nil {
			drain(resp.Body)
		}

		time.Sleep(watchRetryPause)
		list, err := ListCatalogs(watchContext(), c)
		if err != nil {
			fmt.Fprintf(os.Stderr, "listing catalogs to resume the watch: %v\n", err)
			continue
		}
		resourceVersion = list.Metadata.ResourceVersion
		poke(wake)
	}
}

// WatchPlayers wakes the loop on every Player change, so a Player that
// names this operator as its idle controller gets a screen pod without a
// backstop tick's delay, and one that names another controller loses its
// screen pod as fast. The recovery is watchLibraries's: a dropped stream or a
// 410 Gone lists the collection, wakes the loop, and resumes from the list's
// version. A list that fails leaves the resume point where it was, which is
// what a cluster with no media-operator answers on every turn.
func watchPlayers(c *Client, resourceVersion string, wake chan<- struct{}) {
	for {
		path := playersPath + "?watch=true&allowWatchBookmarks=true&resourceVersion=" + resourceVersion
		resp, err := c.Do(watchContext(), http.MethodGet, path, nil)
		if err == nil && resp.StatusCode == http.StatusOK {
			resourceVersion = readWatchStream(resp, resourceVersion, wake)
		}
		if resp != nil {
			drain(resp.Body)
		}

		time.Sleep(watchRetryPause)
		list, err := ListPlayers(watchContext(), c)
		if err != nil {
			fmt.Fprintf(os.Stderr, "listing players to resume the watch: %v\n", err)
			continue
		}
		resourceVersion = list.Metadata.ResourceVersion
		poke(wake)
	}
}

// WatchPods wakes the loop on every change to a scanner pod, and the
// label selector keeps the stream to this operator's own pods. Every
// event earns a wake here, because a Library is Ready only while its
// pod runs with every container ready: the update that turns a
// container ready is as much a change to report as a delete.
//
// The recovery is the same as watchLibraries: a dropped stream or a
// 410 Gone lists the collection, wakes the loop, and resumes the watch
// from the list's version.
func watchPods(c *Client, resourceVersion string, wake chan<- struct{}) {
	for {
		path := podsAllPath + "?watch=true&allowWatchBookmarks=true&" + scannerPodsQuery +
			"&resourceVersion=" + resourceVersion
		resp, err := c.Do(watchContext(), http.MethodGet, path, nil)
		if err == nil && resp.StatusCode == http.StatusOK {
			resourceVersion = readWatchStream(resp, resourceVersion, wake)
		}
		if resp != nil {
			drain(resp.Body)
		}

		time.Sleep(watchRetryPause)
		list, err := ListScannerPods(watchContext(), c)
		if err != nil {
			fmt.Fprintf(os.Stderr, "listing scanner pods to resume the watch: %v\n", err)
			continue
		}
		resourceVersion = list.Metadata.ResourceVersion
		poke(wake)
	}
}

// ReadWatchStream reads one connection's worth of events. The returned
// version is where the next watch resumes.
func readWatchStream(resp *http.Response, resourceVersion string, wake chan<- struct{}) string {
	decoder := json.NewDecoder(resp.Body)
	for {
		var event struct {
			Type   string `json:"type"`
			Object struct {
				Metadata ObjectMeta `json:"metadata"`
			} `json:"object"`
		}
		if err := decoder.Decode(&event); err != nil {
			return resourceVersion
		}
		if event.Type == "ERROR" {
			// Usually a 410 Gone wrapped in an event: the server no
			// longer holds this resourceVersion. The relist in the
			// caller is the answer.
			return resourceVersion
		}
		if event.Object.Metadata.ResourceVersion != "" {
			resourceVersion = event.Object.Metadata.ResourceVersion
		}
		if event.Type == "BOOKMARK" {
			// A bookmark moves the resume point and reconciles
			// nothing, so it earns no wake.
			continue
		}
		poke(wake)
	}
}

// Poke never blocks, and the wake channel buffers exactly one. A wake
// already queued says everything a second one would say, because the
// pass that answers it reads the whole collection.
func poke(wake chan<- struct{}) {
	select {
	case wake <- struct{}{}:
	default:
	}
}
