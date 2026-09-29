//go:build !pod

package main

// What this operator adds to the shared informer package's reads of a
// watch's store. versionmemo.go says what the memo guards and why the
// operator's shared code is split across the two files.
//
// A list comes from a store only after the store holds the whole first
// read. Before that, watch.go answers the error of the informer's last
// read, as it does for every other collection, so a pass never reads a
// store that holds part of the collection as the whole collection, and a
// kind the cluster does not serve costs no request on each pass. While
// the API server forbids a watch, its store is not ready, and a list
// reads each object the store holds from the API server. A copy in a
// store that does not convert is read from the API server too.
//
// The operator creates none of the Libraries, the Catalogs, and the
// MetadataProviders: a person declares each one. So none of their
// stores sets Whole, and a list reads no object that the store does not
// hold yet.

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// currentOrCached answers informer.CurrentList, or the store's copies where a
// read from the API server fails. This operator alone needs it. The pass
// reads a failed read of the MetadataProviders as no provider at all,
// and every Library then reports each of its sources as missing, so one
// provider whose read fails must not fail the list. The store's copy of
// that provider can be older than the operator's own write. A status
// write from it goes through memo.SettleStatus, which reads the provider
// again on a 409.
func currentOrCached[T any, P informer.Object[T]](ctx context.Context, c *apiclient.Client, held informer.Held, path func(key string) string) []T {
	items, err := informer.CurrentList[T, P](c.WithContext(ctx), held, path)
	if err == nil {
		return items
	}
	fmt.Fprintf(os.Stderr, "reading one object from the API server: %v; the pass reads the watch's copies\n", err)
	return informer.CachedList[T](held.View)
}

// readStood answers one object this operator stands by name: the store's
// copy when it holds a current one, and the API server's copy when it does
// not. This operator alone needs it. The store holds every object the
// watch selects, so once it holds its first read, an object it does not
// hold and the memo has not noted does not exist, and the read costs the
// API server nothing. An object the memo noted, such as one this operator
// created a moment ago or one whose create met a conflict, is read from
// the API server until the store holds it or the API server answers that
// it is gone.
func readStood[T any, P informer.Object[T]](ctx context.Context, c *apiclient.Client, held informer.Held, key, path string) (*T, error) {
	if held.View.Ready() {
		if _, stored, _ := held.View.Store.GetByKey(key); !stored && !held.Versions.Noted(key) {
			return nil, apiclient.ErrNotFound
		}
	}
	found, err := informer.ReadOne[T, P](c.WithContext(ctx), held, key, path)
	if errors.Is(err, apiclient.ErrNotFound) {
		// The object is gone, and the store holds no copy of it or
		// holds the last one until the watch delivers the delete. The
		// memo forgets it, so the next read answers from the store and
		// sends nothing.
		held.Versions.Forget(key)
	}
	return found, err
}
