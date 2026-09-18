package main

// The reenrich verb. It patches a Library's spec.refresh so the
// operator's next pass reopens a fact's gap and asks the providers
// again, the field the operator's enricher watches. With --only it
// touches one fact; with none it touches every fact reenrich holds.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// The reenrich verb's flags and its one positional
// argument.
type reenrichOptions struct {
	Name  string
	Only  string
	Force bool
}

// The Library resource reenrich patches.
var libraryResource = schema.GroupVersionResource{
	Group:    "library.liken.sh",
	Version:  "v1alpha1",
	Resource: "libraries",
}

// refreshFacts is every fact when --only names none, or
// the one it names; an unknown fact is an error before the cluster, the
// way an unknown format is.
func refreshFacts(only string) ([]string, error) {
	if only == "" {
		return refreshFactVocabulary, nil
	}
	for _, fact := range refreshFactVocabulary {
		if fact == only {
			return []string{only}, nil
		}
	}
	return nil, fmt.Errorf("unknown fact %q", only)
}

// refreshPatch builds the merge patch that sets each
// named fact's spec.refresh entry to now. A merge patch adds the keys
// and leaves the rest of the Library untouched, so a person's own spec
// stands.
func refreshPatch(only string, now time.Time) ([]byte, error) {
	facts, err := refreshFacts(only)
	if err != nil {
		return nil, err
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	refresh := map[string]string{}
	for _, fact := range facts {
		refresh[fact] = stamp
	}
	return json.Marshal(map[string]any{"spec": map[string]any{"refresh": refresh}})
}

// reenrich reads the operator version and warns or
// refuses on drift, then patches spec.refresh so the operator's next
// pass reopens the named facts.
func reenrich(ctx context.Context, clientset kubernetes.Interface, dyn dynamic.Interface,
	namespace, name string, force bool, patch []byte, stderr io.Writer) error {
	operator, err := operatorVersion(ctx, clientset)
	if err != nil {
		fmt.Fprintf(stderr, "reading the operator version: %v\n", err)
	}
	switch action, message := decideVersionAction(version, operator, ""); action {
	case actionRefuse:
		return fmt.Errorf("%s", message)
	case actionWarn:
		if !force {
			fmt.Fprintln(stderr, message)
		}
	}

	_, err = dyn.Resource(libraryResource).Namespace(namespace).Patch(
		ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	return err
}

// runReenrich validates the flags, builds the cluster
// clients, and patches the Library named.
func runReenrich(ctx context.Context, getter genericclioptions.RESTClientGetter, opts reenrichOptions, stderr io.Writer) error {
	if opts.Name == "" {
		return fmt.Errorf("reenrich needs a library")
	}
	patch, err := refreshPatch(opts.Only, time.Now())
	if err != nil {
		return err
	}

	config, err := getter.ToRESTConfig()
	if err != nil {
		return err
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}
	dyn, err := dynamic.NewForConfig(config)
	if err != nil {
		return err
	}
	namespace, _, err := getter.ToRawKubeConfigLoader().Namespace()
	if err != nil {
		return err
	}

	return reenrich(ctx, clientset, dyn, namespace, opts.Name, opts.Force, patch, stderr)
}
