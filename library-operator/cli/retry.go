package main

// The retry verb. It patches a Library's spec.retry so the operator's next
// pass reopens the titles a fact missed or failed on, and leaves the titles
// it answered. With --only it touches one fact; with none it touches every
// fact retry holds.

import (
	"context"
	"fmt"
	"io"
	"time"

	"k8s.io/cli-runtime/pkg/genericclioptions"
)

// The retry verb's flags and its one positional argument.
type retryOptions struct {
	Name  string
	Only  string
	Force bool
}

// retryFacts is every fact spec.retry takes when --only names none, or the
// one it names.
func retryFacts(only string) ([]string, error) {
	return factsOf(retryFactVocabulary, only)
}

// retryPatch builds the merge patch that sets each named fact's spec.retry
// entry to now.
func retryPatch(only string, now time.Time) ([]byte, error) {
	facts, err := retryFacts(only)
	if err != nil {
		return nil, err
	}
	return timesPatch("retry", facts, now), nil
}

// runRetry validates the flags, builds the cluster clients, and patches the
// Library named.
func runRetry(ctx context.Context, getter genericclioptions.RESTClientGetter, opts retryOptions, stderr io.Writer) error {
	if opts.Name == "" {
		return fmt.Errorf("retry needs a library")
	}
	patch, err := retryPatch(opts.Only, time.Now())
	if err != nil {
		return err
	}

	clientset, dyn, namespace, err := libraryClients(getter)
	if err != nil {
		return err
	}

	return patchLibraryRefresh(ctx, clientset, dyn, namespace, opts.Name, opts.Force, patch, stderr)
}
