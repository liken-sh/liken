package main

import (
	"context"
	"fmt"
	"maps"
	"os"
	"time"
)

// confirmProviderKeys reads, before a Job is created, the Secret of each
// Ready provider the Library names. A check call is due only hourly
// while a provider is Reachable, and the operator has no watch on
// Secrets, because a person names them and no label selects them. A
// Job that names a Secret that is gone never starts: the kubelet holds
// its pod in CreateContainerConfigError, and the Job holds every other
// Job of the Library until its deadline. One read for each Job is cheap
// next to that. A provider whose Secret or key is gone gets the NoSecret
// verdict now, through the same note and write as a check call, and the
// answer leaves it out of this Job. A read that fails for any other
// reason says nothing about the Secret, so the provider stays.
func (o *operator) confirmProviderKeys(ctx context.Context, library *Library, providers providerSet, now time.Time) providerSet {
	confirmed := maps.Clone(providers)
	for _, name := range library.Spec.Sources {
		key := libraryKey(library.Metadata.Namespace, name)
		provider, exists := confirmed[key]
		if !exists || !provider.ready() || provider.secretRef() == nil {
			continue
		}
		_, verdict, err := o.providerKey(ctx, provider)
		if err != nil || verdict.reason == "" {
			continue
		}
		o.noteProviderCall(provider, now, verdict)
		if err := o.writeProviderVerdict(ctx, provider, verdict, now); err != nil {
			fmt.Fprintf(os.Stderr, "writing the metadata provider %s/%s: %v\n",
				provider.Metadata.Namespace, provider.Metadata.Name, err)
		}
		delete(confirmed, key)
	}
	return confirmed
}
