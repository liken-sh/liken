package main

// Some objects this operator reads are one named object that changes
// rarely and matters the moment it changes: a Secret that holds a
// certificate, and the ConfigMap that holds the cluster's client
// authority. A read on a clock would find each change up to one
// period late and cost a request every period in between. A watch on
// the one object costs nothing while the object stands, and delivers
// a change as it lands.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"time"
)

// The first wait after a failed listing or watch, and the longest
// that wait grows to. A failure here is an API server that is down or
// a grant that is missing, and neither one is fixed faster by a
// request every few seconds.
const (
	objectWatchRetry = time.Second
	objectWatchLimit = time.Minute
)

// watchNamed calls seen with the object each time it changes, and with
// nil when the object does not exist. It runs until the context ends.
//
// A listing gives the whole truth and a resource version, and the
// watch that follows starts at that version, so no change between the
// two is missed. A watch that the API server ends on its timeout
// resumes at the last version it delivered. A version the API server
// no longer holds answers 410 Gone, and the loop lists again. The
// field selector names the object, which is also how RBAC matches a
// list and a watch against a rule's resourceNames.
func watchNamed[T any](ctx context.Context, c *Client, collection, name string, what string, seen func(held *T)) {
	selector := "fieldSelector=" + url.QueryEscape("metadata.name="+name)
	version := ""
	delay := objectWatchRetry
	for ctx.Err() == nil {
		if version == "" {
			listed, err := listNamed[T](c, collection+"?"+selector)
			if err != nil {
				fmt.Fprintf(os.Stderr, "listing %s: %v\n", what, err)
				delay = pauseWatch(ctx, delay)
				continue
			}
			version = listed.version
			seen(listed.held)
		}
		next, err := streamNamed(ctx, c, collection+"?"+selector, version, seen)
		switch {
		case ctx.Err() != nil:
			return
		case err == errWatchExpired:
			version = ""
			delay = objectWatchRetry
		case err != nil:
			fmt.Fprintf(os.Stderr, "watching %s: %v\n", what, err)
			version = ""
			delay = pauseWatch(ctx, delay)
		default:
			version = next
			delay = objectWatchRetry
		}
	}
}

// Wait out one failure, and answer the wait after the next one.
func pauseWatch(ctx context.Context, delay time.Duration) time.Duration {
	select {
	case <-ctx.Done():
	case <-time.After(delay):
	}
	return nextDialDelay(delay, objectWatchLimit)
}

// What a listing of one name answers: the object, or nil when the
// list is empty, and the version the watch starts at.
type namedListing[T any] struct {
	held    *T
	version string
}

func listNamed[T any](c *Client, path string) (namedListing[T], error) {
	list, err := get[struct {
		Metadata struct {
			ResourceVersion string `json:"resourceVersion"`
		} `json:"metadata"`
		Items []T `json:"items"`
	}](c, path)
	if err != nil {
		return namedListing[T]{}, err
	}
	listing := namedListing[T]{version: list.Metadata.ResourceVersion}
	if len(list.Items) > 0 {
		listing.held = &list.Items[0]
	}
	return listing, nil
}

// errWatchExpired is the API server's 410 Gone: the version the watch
// asked for is older than any it holds, and only a new listing gives
// a version to start from.
var errWatchExpired = fmt.Errorf("the watch's resource version expired")

// One watch connection. It answers the last version it delivered, so
// the next connection resumes there.
func streamNamed[T any](ctx context.Context, c *Client, path, version string, seen func(*T)) (string, error) {
	stream := fmt.Sprintf("%s&watch=true&allowWatchBookmarks=true&resourceVersion=%s&timeoutSeconds=%d",
		path, url.QueryEscape(version), int(displayWatchTimeout.Seconds()))
	body, err := c.Watch(ctx, stream)
	if err != nil {
		return version, err
	}
	defer drain(body)

	events := json.NewDecoder(body)
	for {
		var event struct {
			Type   string          `json:"type"`
			Object json.RawMessage `json:"object"`
		}
		if err := events.Decode(&event); err != nil {
			if err == io.EOF {
				return version, nil
			}
			return version, err
		}
		var meta struct {
			Code     int `json:"code"`
			Metadata struct {
				ResourceVersion string `json:"resourceVersion"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(event.Object, &meta); err != nil {
			return version, err
		}
		switch event.Type {
		case "ERROR":
			if meta.Code == 410 {
				return version, errWatchExpired
			}
			return version, fmt.Errorf("the watch ended with %s", event.Object)
		case "BOOKMARK":
		case "DELETED":
			seen(nil)
		default:
			held := new(T)
			if err := json.Unmarshal(event.Object, held); err != nil {
				return version, err
			}
			seen(held)
		}
		version = meta.Metadata.ResourceVersion
	}
}
