package main

// The rescan verb. A full walk of a Library runs on the CronJob
// the operator creates from spec.scan.schedule, or on the webhook the
// *arr tools and Jellyfin post to. Neither is a field a person patches
// on the Library, and this CLI writes only the Library resource, so
// this verb is a stub. Design the server side, a field the reconcile
// path watches, before this stub becomes a client.

import (
	"fmt"
	"io"

	"k8s.io/cli-runtime/pkg/genericclioptions"
)

// The rescan verb's one positional argument.
type rescanOptions struct {
	Name string
}

// runRescan refuses until the operator watches a field
// that requests a full walk. No spec field or annotation triggers a
// walk today, so this verb names no server mechanism and returns a
// stub error.
func runRescan(_ genericclioptions.RESTClientGetter, opts rescanOptions, _ io.Writer) error {
	if opts.Name == "" {
		return fmt.Errorf("rescan needs a library")
	}
	return fmt.Errorf("rescan is not yet implemented")
}
