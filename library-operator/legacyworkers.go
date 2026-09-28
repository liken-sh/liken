package main

// legacyworkers.go removes what an earlier release stood for each Library:
// the CronJob that ran the full walk, and the separate catalog claims of the
// enrich, trickplay, and trailers Jobs. Every Job of a Library runs on the
// one catalog claim, behind the operator's gate, and the operator starts the
// walk itself. The old claims hold copies of the catalog that no Job mounts,
// so they only take space.

import (
	"context"
)

// The names an earlier release gave the objects of one Library.
func legacyCronJobName(library string) string {
	return library + "-scan"
}

func legacyClaimNames(library string) []string {
	return []string{
		library + "-enrich-catalog",
		library + "-trickplay-catalog",
		library + "-trailers-catalog",
	}
}

// The deletes run once for each Library in the life of this operator
// process, because an absent object is success and a pass need not repeat
// them. A claim that a running Job of an earlier release still mounts stays
// until that pod ends, under the claim's protection finalizer. The volume of
// a per-node claim becomes Released, and the volume sweep deletes it.
func (o *operator) retireLegacyWorkers(ctx context.Context, library *Library) error {
	namespace, name := library.Metadata.Namespace, library.Metadata.Name
	key := libraryKey(namespace, name)
	if o.legacyRetired[key] {
		return nil
	}
	if err := DeleteCronJob(ctx, o.client, namespace, legacyCronJobName(name)); err != nil {
		return err
	}
	for _, claim := range legacyClaimNames(name) {
		if err := DeletePersistentVolumeClaim(ctx, o.client, namespace, claim); err != nil {
			return err
		}
	}
	o.legacyRetired[key] = true
	return nil
}
