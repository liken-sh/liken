package main

// The rescan verb. It patches a Library's spec.refresh so the operator walks
// the whole library once, the way reenrich reopens a fact's gap. The key is
// the scan worker's own name, and a walk that starts at or after the time
// answers the request.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"k8s.io/cli-runtime/pkg/genericclioptions"
)

// The rescan verb's one positional argument.
type rescanOptions struct {
	Name string
}

// walkRefreshKey is the spec.refresh key that asks for a full walk. It
// copies the operator's refreshWalk, which the CLI cannot import.
const walkRefreshKey = "scan"

// rescanPatch builds the merge patch that sets the walk's spec.refresh entry
// to now. A merge patch adds the key and leaves the rest of the Library and
// the other targets' times untouched.
func rescanPatch(now time.Time) ([]byte, error) {
	return json.Marshal(map[string]any{"spec": map[string]any{"refresh": map[string]string{
		walkRefreshKey: now.UTC().Format(time.RFC3339Nano),
	}}})
}

// runRescan validates the argument, builds the cluster clients, and patches
// the Library named.
func runRescan(ctx context.Context, getter genericclioptions.RESTClientGetter,
	opts rescanOptions, force bool, stderr io.Writer) error {
	if opts.Name == "" {
		return fmt.Errorf("rescan needs a library")
	}
	patch, err := rescanPatch(time.Now())
	if err != nil {
		return err
	}

	clientset, dyn, namespace, err := libraryClients(getter)
	if err != nil {
		return err
	}

	return patchLibraryRefresh(ctx, clientset, dyn, namespace, opts.Name, force, patch, stderr)
}
