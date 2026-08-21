package main

// A watch is an ordinary GET with watch=true whose response never
// ends: the API server holds the connection open and writes one JSON
// event per change, the same protocol liken's own operators speak.
//
// This watch carries no object to the loop. Every pass re-lists, so
// a change here is only a wake, and the loop decides what to read.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// watchRetryPause is how long the watch waits before it re-lists
// after a dropped stream, and a variable so a test drives a
// reconnect in milliseconds.
var watchRetryPause = 2 * time.Second

// watchPlays resumes each stream from a resourceVersion, so no
// change is missed between reconnects. A 410 Gone and a routine
// stream end recover the same way: list the collection, wake the
// loop, and watch again from the list's own version.
//
// The list after every ended stream is what keeps the resume point
// current, so a bookmark's version matters only when that list
// itself fails. The loop asks for bookmarks anyway because they cost
// one line each and make that failure window resumable, where a
// relist costs one full read of the collection, small here, and the
// pass the wake triggers.
func watchPlays(c *Client, resourceVersion string, wake chan<- struct{}) {
	for {
		path := playsPath + "?watch=true&allowWatchBookmarks=true&resourceVersion=" + resourceVersion
		resp, err := c.Do(http.MethodGet, path, nil)
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
		list, err := ListPlays(c)
		if err != nil {
			fmt.Fprintf(os.Stderr, "listing plays to resume the watch: %v\n", err)
			continue
		}
		resourceVersion = list.Metadata.ResourceVersion
		poke(wake)
	}
}

// watchRemotes mirrors watchPlays for the Remote collection: a Remote
// change is a wake, and the loop reconciles a standing pod for every
// Remote on the pass that wake triggers. The recovery is the same as
// watchPlays: a dropped stream or a 410 Gone re-lists the collection,
// wakes the loop, and resumes the watch from the list's version.
func watchRemotes(c *Client, resourceVersion string, wake chan<- struct{}) {
	for {
		path := remotesAllPath + "?watch=true&allowWatchBookmarks=true&resourceVersion=" + resourceVersion
		resp, err := c.Do(http.MethodGet, path, nil)
		if err == nil && resp.StatusCode == http.StatusOK {
			resourceVersion = readWatchStream(resp, resourceVersion, wake)
		}
		if resp != nil {
			drain(resp.Body)
		}

		time.Sleep(watchRetryPause)
		list, err := ListAllRemotes(c)
		if err != nil {
			fmt.Fprintf(os.Stderr, "listing remotes to resume the watch: %v\n", err)
			continue
		}
		resourceVersion = list.Metadata.ResourceVersion
		poke(wake)
	}
}

// watchPlayers wakes the loop on a Player change, the way watchPlays
// and watchRemotes wake it on theirs. The wake carries no object, so
// the pass it triggers re-reconciles every Play, and a Play whose
// Player reshaped its pod is recreated then. A dropped stream or a 410
// Gone recovers the same way: list the collection, wake the loop, and
// resume the watch from the list's version.
func watchPlayers(c *Client, resourceVersion string, wake chan<- struct{}) {
	for {
		path := playersPath + "?watch=true&allowWatchBookmarks=true&resourceVersion=" + resourceVersion
		resp, err := c.Do(http.MethodGet, path, nil)
		if err == nil && resp.StatusCode == http.StatusOK {
			resourceVersion = readWatchStream(resp, resourceVersion, wake)
		}
		if resp != nil {
			drain(resp.Body)
		}

		time.Sleep(watchRetryPause)
		list, err := ListPlayers(c)
		if err != nil {
			fmt.Fprintf(os.Stderr, "listing players to resume the watch: %v\n", err)
			continue
		}
		resourceVersion = list.Metadata.ResourceVersion
		poke(wake)
	}
}

// watchKeymaps wakes the loop on a Keymap change, so the pass it
// triggers recompiles and republishes every Keymap, and an edit reaches
// a running translator within one pass rather than one backstop tick.
// The recovery mirrors the other watchers: a dropped stream or a 410
// Gone lists the collection, wakes the loop, and resumes the watch from
// the list's version.
func watchKeymaps(c *Client, resourceVersion string, wake chan<- struct{}) {
	for {
		path := keymapsPath + "?watch=true&allowWatchBookmarks=true&resourceVersion=" + resourceVersion
		resp, err := c.Do(http.MethodGet, path, nil)
		if err == nil && resp.StatusCode == http.StatusOK {
			resourceVersion = readWatchStream(resp, resourceVersion, wake)
		}
		if resp != nil {
			drain(resp.Body)
		}

		time.Sleep(watchRetryPause)
		list, err := ListKeymaps(c)
		if err != nil {
			fmt.Fprintf(os.Stderr, "listing keymaps to resume the watch: %v\n", err)
			continue
		}
		resourceVersion = list.Metadata.ResourceVersion
		poke(wake)
	}
}

// readWatchStream reads one connection's worth of events. The
// returned version is where the next watch resumes.
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

// poke never blocks, and the wake channel buffers exactly one. A
// wake already queued says everything a second one would say,
// because the pass that answers it reads the whole collection.
func poke(wake chan<- struct{}) {
	select {
	case wake <- struct{}{}:
	default:
	}
}
