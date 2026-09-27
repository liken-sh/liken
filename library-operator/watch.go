package main

// A watch is an ordinary GET with watch=true whose response never
// ends: the API server holds the connection open and writes one JSON
// event per change, the same protocol liken's own operators speak.
//
// A watch carries no object to the loop. Every pass re-lists, so a
// change here is only a wake, and the loop decides what to read.
// watchloop.go holds the recovery all eight watchers share.

import (
	"context"
	"sync/atomic"
	"time"
)

// A watch is a request whose response never ends, so it carries a context
// with no deadline and no cancel; the bounded contexts belong to the
// passes in operate.go. A watcher runs for the life of the process, and
// the process ending is what ends it.
func watchContext() context.Context {
	return context.Background()
}

// WatchRetryPause is the first wait of a watcher's backoff, and a
// variable so a test drives a reconnect in milliseconds. It is an atomic because a watcher has no stop: the
// watchers one test's operator started outlive that test and read the
// pause while a later test writes it.
var watchRetryPause = newPause(2 * time.Second)

// A duration that goroutines read while a test writes it.
type pause struct {
	nanos atomic.Int64
}

func newPause(d time.Duration) *pause {
	p := &pause{}
	p.set(d)
	return p
}

func (p *pause) get() time.Duration {
	return time.Duration(p.nanos.Load())
}

func (p *pause) set(d time.Duration) {
	p.nanos.Store(int64(d))
}

// WatchLibraries wakes the loop on every Library change.
func watchLibraries(c *Client, resourceVersion string, wake chan<- struct{}, m *metrics) {
	watchCollection(c, collectionWatch{kind: kindLibrary, path: librariesPath + "?", noun: "libraries",
		list: func(ctx context.Context, c *Client) (string, error) {
			list, err := ListLibraries(ctx, c)
			if err != nil {
				return "", err
			}
			return list.Metadata.ResourceVersion, nil
		}}, resourceVersion, wake, m)
}

// WatchCatalogs wakes the loop on every Catalog change, so a Library
// waiting on its namespace's Catalog proceeds on the next pass, and a
// second Catalog is marked Blocked without a backstop tick's delay.
func watchCatalogs(c *Client, resourceVersion string, wake chan<- struct{}, m *metrics) {
	watchCollection(c, collectionWatch{kind: kindCatalog, path: catalogsPath + "?", noun: "catalogs",
		list: func(ctx context.Context, c *Client) (string, error) {
			list, err := ListCatalogs(ctx, c)
			if err != nil {
				return "", err
			}
			return list.Metadata.ResourceVersion, nil
		}}, resourceVersion, wake, m)
}

// WatchPlayers wakes the loop on every Player change, so a Player that
// names this operator as its idle controller gets a screen pod without a
// backstop tick's delay, and one that names another controller loses its
// screen pod as fast. A cluster with no media-operator fails the watch and
// the list on every turn, and the backoff keeps that from spinning.
func watchPlayers(c *Client, resourceVersion string, wake chan<- struct{}, m *metrics) {
	watchCollection(c, collectionWatch{kind: kindPlayer, path: playersPath + "?", noun: "players",
		list: func(ctx context.Context, c *Client) (string, error) {
			list, err := ListPlayers(ctx, c)
			if err != nil {
				return "", err
			}
			return list.Metadata.ResourceVersion, nil
		}}, resourceVersion, wake, m)
}

// This watcher wakes the loop on every MediaPreferences change, so a zone
// the household just set rolls the screen pods without a backstop tick's
// delay. A cluster with no media-operator fails it the way it fails
// watchPlayers.
func watchMediaPreferences(c *Client, resourceVersion string, wake chan<- struct{}, m *metrics) {
	watchCollection(c, collectionWatch{kind: kindMediaPreferences, path: mediaPreferencesPath + "?",
		noun: "media preferences",
		list: func(ctx context.Context, c *Client) (string, error) {
			list, err := ListMediaPreferences(ctx, c)
			if err != nil {
				return "", err
			}
			return list.Metadata.ResourceVersion, nil
		}}, resourceVersion, wake, m)
}

// This watcher wakes the loop on every MetadataProvider change, so a key a
// person has just declared is checked without a backstop tick's delay. A
// cluster that has not applied the CRD fails the list on every turn.
func watchMetadataProviders(c *Client, resourceVersion string, wake chan<- struct{}, m *metrics) {
	watchCollection(c, collectionWatch{kind: kindMetadataProvider, path: metadataProvidersPath + "?",
		noun: "metadata providers",
		list: func(ctx context.Context, c *Client) (string, error) {
			list, err := ListMetadataProviders(ctx, c)
			if err != nil {
				return "", err
			}
			return list.Metadata.ResourceVersion, nil
		}}, resourceVersion, wake, m)
}

// This watcher wakes the loop on every Play change, so a Play that
// media-operator created, finished, or is deleting is held, published,
// or released without a backstop tick's delay.
func watchPlays(c *Client, resourceVersion string, wake chan<- struct{}, m *metrics) {
	watchCollection(c, collectionWatch{kind: kindPlay, path: playsAllPath + "?", noun: "plays",
		list: func(ctx context.Context, c *Client) (string, error) {
			list, err := ListPlays(ctx, c)
			if err != nil {
				return "", err
			}
			return list.Metadata.ResourceVersion, nil
		}}, resourceVersion, wake, m)
}

// This watcher wakes the loop on every Person change, so a person a
// house just declared is held, and one on the way out is asked for,
// without a backstop tick's delay. A cluster with no people-operator
// fails the list on every turn.
func watchPeople(c *Client, resourceVersion string, wake chan<- struct{}, m *metrics) {
	watchCollection(c, collectionWatch{kind: kindPerson, path: peoplePath + "?", noun: "people",
		list: func(ctx context.Context, c *Client) (string, error) {
			list, err := ListPeople(ctx, c)
			if err != nil {
				return "", err
			}
			return list.Metadata.ResourceVersion, nil
		}}, resourceVersion, wake, m)
}

// WatchPods wakes the loop on every change to a pod that holds a
// catalog agent, and the label selector keeps the stream to those pods.
// Every event earns a wake here, because a Library is Ready only while
// its namespace's catalog pod runs with every container ready: the
// update that turns a container ready is as much a change to report as
// a delete.
func watchPods(c *Client, resourceVersion string, wake chan<- struct{}, m *metrics) {
	watchCollection(c, collectionWatch{kind: kindPod, path: podsAllPath + "?" + catalogMemberQuery + "&",
		noun: "catalog member pods",
		list: func(ctx context.Context, c *Client) (string, error) {
			list, err := ListCatalogMemberPods(ctx, c)
			if err != nil {
				return "", err
			}
			return list.Metadata.ResourceVersion, nil
		}}, resourceVersion, wake, m)
}
