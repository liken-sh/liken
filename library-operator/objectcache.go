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
