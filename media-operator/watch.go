package main

// The collection watches wake the reconcile loop. They carry no object
// to the loop: every pass lists what it reads, so a change here is
// only a wake, and the loop decides what to read. watchloop.go holds
// the recovery they share with the watch on one named object.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// collectionWatch is one collection the loop is woken by: the watch
// path with its query, the list that sets the version after a 410 or a
// failure, and what each event does.
type collectionWatch struct {
	subject string
	path    string
	list    func(c *Client) (string, error)
	event   func(eventType string, object []byte, wake chan<- struct{}) error
}

// follow runs one collection's watch for the life of the process. A
// list wakes the loop too, because it can carry a change the watch
// never delivered, such as one inside the window a 410 lost.
//
// onRestart runs before each watch after the first, so
// media_watch_restarts_total counts what its name says: a watch that
// ended and that this loop opened again. A caller with nothing to count
// passes nil.
func (w collectionWatch) follow(c *Client, resourceVersion string, wake chan<- struct{}, onRestart func()) {
	loop := watchLoop{
		subject: w.subject,
		list: func() (string, error) {
			version, err := w.list(c)
			if err != nil {
				return "", fmt.Errorf("listing %s: %w", w.subject, err)
			}
			poke(wake)
			return version, nil
		},
		open: func(ctx context.Context, version string, live func()) watchEnd {
			return openWatch(ctx, c, w.path, version, live, func(eventType string, object []byte) error {
				return w.event(eventType, object, wake)
			})
		},
		restarted:    onRestart,
		backoffStart: watchBackoffStart,
		backoffMax:   watchBackoffMax,
		minLife:      watchMinLife,
		now:          time.Now,
		pause:        waiting,
		report:       func(line string) { fmt.Fprintln(os.Stderr, line) },
	}
	loop.run(context.Background(), resourceVersion)
}

// watchQuery asks for a watch with bookmarks, so the version moves
// while nothing changes and the next watch resumes inside the API
// server's window.
const watchQuery = "?watch=true&allowWatchBookmarks=true"

// wakeOnChange wakes the loop on every change. A bookmark moves the
// resume point and reconciles nothing, so it earns no wake.
func wakeOnChange(eventType string, _ []byte, wake chan<- struct{}) error {
	if eventType != "BOOKMARK" {
		poke(wake)
	}
	return nil
}

// wakeOnPodEnd wakes the loop when k8s removes a playback pod or when
// one turns Failed, and on nothing else, because a routine update to a
// running pod needs no pass.
func wakeOnPodEnd(eventType string, object []byte, wake chan<- struct{}) error {
	var pod struct {
		Status struct {
			Phase string `json:"phase"`
		} `json:"status"`
	}
	if err := json.Unmarshal(object, &pod); err != nil {
		return err
	}
	switch {
	case eventType == "DELETED":
		poke(wake)
	case eventType == "MODIFIED" && pod.Status.Phase == podFailed:
		poke(wake)
	}
	return nil
}

// watchPlays wakes the loop on a Play change.
func watchPlays(c *Client, resourceVersion string, wake chan<- struct{}, onRestart func()) {
	collectionWatch{
		subject: "plays",
		path:    playsPath + watchQuery,
		list: func(c *Client) (string, error) {
			list, err := ListPlays(c)
			if err != nil {
				return "", err
			}
			return list.Metadata.ResourceVersion, nil
		},
		event: wakeOnChange,
	}.follow(c, resourceVersion, wake, onRestart)
}

// watchRemotes wakes the loop on a Remote change, and the loop
// reconciles a standing pod for every Remote on the pass that wake
// triggers.
func watchRemotes(c *Client, resourceVersion string, wake chan<- struct{}, onRestart func()) {
	collectionWatch{
		subject: "remotes",
		path:    remotesAllPath + watchQuery,
		list: func(c *Client) (string, error) {
			list, err := ListAllRemotes(c)
			if err != nil {
				return "", err
			}
			return list.Metadata.ResourceVersion, nil
		},
		event: wakeOnChange,
	}.follow(c, resourceVersion, wake, onRestart)
}

// watchPlayers wakes the loop on a Player change. The wake carries no
// object, so the pass it triggers reconciles every Play again, and a
// Play whose Player reshaped its pod is recreated then.
func watchPlayers(c *Client, resourceVersion string, wake chan<- struct{}, onRestart func()) {
	collectionWatch{
		subject: "players",
		path:    playersPath + watchQuery,
		list: func(c *Client) (string, error) {
			list, err := ListPlayers(c)
			if err != nil {
				return "", err
			}
			return list.Metadata.ResourceVersion, nil
		},
		event: wakeOnChange,
	}.follow(c, resourceVersion, wake, onRestart)
}

// watchPeripherals wakes the loop on a Peripheral change. A controller
// that connects, disconnects, or reports a new charge changes its
// Peripheral, and the pass that wake triggers republishes the Player
// status the idle screen draws.
func watchPeripherals(c *Client, resourceVersion string, wake chan<- struct{}, onRestart func()) {
	collectionWatch{
		subject: "peripherals",
		path:    peripheralsPath + watchQuery,
		list: func(c *Client) (string, error) {
			list, err := ListPeripherals(c)
			if err != nil {
				return "", err
			}
			return list.Metadata.ResourceVersion, nil
		},
		event: wakeOnChange,
	}.follow(c, resourceVersion, wake, onRestart)
}

// watchKeymaps wakes the loop on a Keymap change, so the pass it
// triggers compiles and publishes every Remote's table again, and an
// edit reaches a running standing pod within one pass.
func watchKeymaps(c *Client, resourceVersion string, wake chan<- struct{}, onRestart func()) {
	collectionWatch{
		subject: "keymaps",
		path:    keymapsPath + watchQuery,
		list: func(c *Client) (string, error) {
			list, err := ListKeymaps(c)
			if err != nil {
				return "", err
			}
			return list.Metadata.ResourceVersion, nil
		},
		event: wakeOnChange,
	}.follow(c, resourceVersion, wake, onRestart)
}

// watchMediaPreferences wakes the loop on a MediaPreferences edit, so
// the resolved fields on a running Play's status refresh within one
// pass.
func watchMediaPreferences(c *Client, resourceVersion string, wake chan<- struct{}, onRestart func()) {
	collectionWatch{
		subject: "media preferences",
		path:    mediaPrefsPath + watchQuery,
		list: func(c *Client) (string, error) {
			list, err := ListMediaPreferences(c)
			if err != nil {
				return "", err
			}
			return list.Metadata.ResourceVersion, nil
		},
		event: wakeOnChange,
	}.follow(c, resourceVersion, wake, onRestart)
}

// watchPods wakes the loop when k8s removes a playback pod or when one
// turns Failed, so an eviction or a crash reaches the reconcile at once
// instead of waiting for the backstop tick.
func watchPods(c *Client, resourceVersion string, wake chan<- struct{}, onRestart func()) {
	collectionWatch{
		subject: "playback pods",
		path:    podsAllPath + watchQuery + "&" + playbackPodsQuery,
		list: func(c *Client) (string, error) {
			list, err := ListPlaybackPods(c)
			if err != nil {
				return "", err
			}
			return list.Metadata.ResourceVersion, nil
		},
		event: wakeOnPodEnd,
	}.follow(c, resourceVersion, wake, onRestart)
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
