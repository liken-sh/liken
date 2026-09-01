package main

// One pass over one Library: resolve the storage it names, stand the
// scanner pod it becomes, and write what both say into its status.
//
// The order is the order of the conditions. A Library that names a
// claim nothing has bound has no volume to mount, so it gets no pod,
// and the Bound condition alone says why. Only a bound Library reaches
// the pod.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"slices"
	"strconv"
	"time"
)

// Binding is what a Library's storage resolved to: the volume behind
// the claim, and the reason and message the Bound condition carries. A
// binding with no volume is a Library that cannot be scanned, and the
// reason names which of the three ways it failed.
type binding struct {
	volume  *LibraryVolume
	reason  string
	message string
}

// Reconcile brings one Library into line and reports on it. It reads
// the whole state every pass rather than acting on what an event
// carried, so the same facts reach the same status whatever order the
// events arrived in.
//
// The catalog is a precondition beside the storage. A Library stands a
// scanner pod only when its storage is bound and its namespace holds
// exactly one Catalog, because the pod's catalog agent joins the cluster
// the Catalog stands and takes a volume the Catalog sizes.
func (o *operator) reconcile(ctx context.Context, library *Library, choice catalogChoice) error {
	if err := o.holdLibrary(ctx, library); err != nil {
		return err
	}

	bound, err := resolveStorage(ctx, o.client, library)
	if err != nil {
		return err
	}

	// A Library with no volume, or in a namespace with no single
	// Catalog, gets no pod. There would be nothing to mount, or no
	// cluster to join, and the Ready condition says which.
	//
	// It gets no webhook Service either. The Service selects the scanner
	// pod, and an address with no pod behind it answers nothing.
	var pod *Pod
	if scannerStands(bound, choice) {
		if err := o.standCatalogClaim(ctx, library, choice.catalog); err != nil {
			return err
		}
		if err := o.standWebhookService(ctx, library); err != nil {
			return err
		}
		desired := buildScannerPod(library, o.scannerImage, o.corrosionImage, o.busAddress, o.topicBase)
		pod, err = o.standPod(ctx, desired)
		if err != nil {
			return err
		}
	} else if err := o.stopScannerPod(ctx, library); err != nil {
		return err
	}

	namespace, name := library.Metadata.Namespace, library.Metadata.Name
	report := o.reports.latestFor(namespace, name)
	online := o.reports.onlineFor(namespace, name)
	return writeLibraryStatus(ctx, o.client, library,
		deriveLibraryStatus(library, bound, choice, pod, report, online, time.Now().UTC()))
}

// HoldLibrary puts the finalizer on a Library that does not carry
// it, so a later delete waits for the departure in depart.go instead
// of taking the rows' only sweeper with the object. A Library from
// before this operator held finalizers adopts one here on its next
// pass. The patch produces a new resourceVersion, and the copy
// carries it forward so the status write later in this pass states
// the version the server now holds.
func (o *operator) holdLibrary(ctx context.Context, library *Library) error {
	if library.Metadata.holds(libraryFinalizer) && !library.Metadata.holds(formerLibraryFinalizer) {
		return nil
	}
	// The former name goes in the same patch that puts the current
	// one on, so a Library from the release that named it swaps in
	// one write.
	finalizers := library.Metadata.without(formerLibraryFinalizer)
	if !slices.Contains(finalizers, libraryFinalizer) {
		finalizers = append(finalizers, libraryFinalizer)
	}
	version, err := PatchLibraryFinalizers(ctx, o.client, library.Metadata.Namespace,
		library.Metadata.Name, library.Metadata.ResourceVersion, finalizers)
	if errors.Is(err, ErrConflict) {
		// A write between the list and this patch wakes the
		// libraries watch, and the next pass patches again.
		return nil
	}
	if err != nil {
		return err
	}
	library.Metadata.Finalizers = finalizers
	library.Metadata.ResourceVersion = version
	return nil
}

// StopScannerPod removes the pod of a Library that no longer stands one.
// The pass is level-triggered, so a Library whose claim or Catalog went
// away loses its scanner on the next pass, rather than keeping a pod that
// walks a volume the Library no longer reports. On every later pass the
// pod is already absent, and an absent pod is success.
func (o *operator) stopScannerPod(ctx context.Context, library *Library) error {
	return DeletePod(ctx, o.client, library.Metadata.Namespace,
		scannerPodName(library.Metadata.Name))
}

// ScannerStands reports the one condition a Library's scanner needs: its
// storage is bound, and its namespace holds exactly one Catalog. The pass
// reads it to create the objects, and the status derivation reads it to
// report the webhook address, so the two cannot answer differently.
func scannerStands(bound binding, choice catalogChoice) bool {
	return bound.volume != nil && choice.catalog != nil
}

// ResolveStorage reads the claim a Library names and the volume behind
// it. Every answer that is the cluster's own state is a binding rather
// than a failure: a claim a person has not created, a claim still
// waiting on a volume, and a volume that has gone are all states to
// report. Only a request that fails is an error, because then the pass
// does not know what the storage is.
func resolveStorage(ctx context.Context, c *Client, library *Library) (binding, error) {
	namespace, name := library.Metadata.Namespace, library.Spec.Storage.Claim

	claim, err := GetPersistentVolumeClaim(ctx, c, namespace, name)
	if errors.Is(err, ErrNotFound) {
		return binding{
			reason: reasonClaimNotFound,
			message: fmt.Sprintf("the PersistentVolumeClaim %s does not exist in namespace %s",
				name, namespace),
		}, nil
	}
	if err != nil {
		return binding{}, fmt.Errorf("reading the claim %s: %w", name, err)
	}

	// The binder writes volumeName, so a claim is usable only once it
	// carries both the Bound phase and a volume to read.
	if claim.Status.Phase != claimBound || claim.Spec.VolumeName == "" {
		return binding{
			reason:  reasonClaimUnbound,
			message: fmt.Sprintf("the PersistentVolumeClaim %s is %s", name, claimState(claim)),
		}, nil
	}

	volume, err := GetPersistentVolume(ctx, c, claim.Spec.VolumeName)
	if errors.Is(err, ErrNotFound) {
		return binding{
			reason: reasonVolumeNotFound,
			message: fmt.Sprintf("the PersistentVolume %s the claim %s names does not exist",
				claim.Spec.VolumeName, name),
		}, nil
	}
	if err != nil {
		return binding{}, fmt.Errorf("reading the volume %s: %w", claim.Spec.VolumeName, err)
	}

	return binding{
		volume: libraryVolume(volume),
		reason: reasonBound,
		message: fmt.Sprintf("the claim %s is bound to the PersistentVolume %s",
			name, volume.Metadata.Name),
	}, nil
}

// ClaimState names what a claim is doing, for the message the Bound
// condition carries. Pending is a claim no volume has answered, and
// Lost is a claim whose volume has gone.
func claimState(claim *PersistentVolumeClaim) string {
	if claim.Status.Phase == "" {
		return "not bound to a volume"
	}
	return claim.Status.Phase
}

// LibraryVolume reports what serves the storage. The type is the name
// of the volume's own source key, so a cluster that serves its movies
// through a driver this operator knows nothing about still reports
// which one. The NFS pair is filled for an NFS volume alone, because
// a media reference over NFS is built from the server and the export.
func libraryVolume(volume *PersistentVolume) *LibraryVolume {
	reported := &LibraryVolume{Name: volume.Metadata.Name, Type: volume.Spec.Source}
	if volume.Spec.NFS != nil {
		reported.Server = volume.Spec.NFS.Server
		reported.Path = volume.Spec.NFS.Path
	}
	return reported
}

// StandPod brings the cluster into line with the pod a pass built, and
// returns the pod that stands after it: the live pod when it matches the
// template, the created pod when there was none, and nil when this pass
// deleted a stale one or another writer created it first. Every pod this
// operator stands and rebuilds goes through here: a Library's scanner pod and
// a Player's screen pod both.
//
// A Deployment finds a stale pod by stamping a hash of the template it
// built and comparing that hash, never by comparing live specs,
// because the API server defaults fields the builder never set and a
// live comparison would either roll on every pass or grow a
// field-by-field allowlist. This operator does the same with one
// annotation on the pod it creates.
func (o *operator) standPod(ctx context.Context, desired *Pod) (*Pod, error) {
	if err := stampTemplateHash(&desired.Metadata, desired.Spec); err != nil {
		return nil, err
	}
	namespace, name := desired.Metadata.Namespace, desired.Metadata.Name

	live, err := GetPod(ctx, o.client, namespace, name)
	if errors.Is(err, ErrNotFound) {
		created, err := CreatePod(ctx, o.client, desired)
		if errors.Is(err, ErrConflict) {
			// Another pass, or another copy of this operator, created
			// the pod first, which is success. The next pass reads it.
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return created, nil
	}
	if err != nil {
		return nil, err
	}

	// A pod on its way out counts as still present. The delete this
	// operator sent is in progress, and the pass leaves it alone until
	// it completes, so one divergence causes one delete and not one
	// delete per pass.
	//
	// this wait is also the ReadWriteOnce handoff for a scanner pod,
	// whose catalog claim admits one pod at a time: the pass creates the
	// replacement only after the old pod releases the claim, which is the
	// create on the not-found branch above. A screen pod's catalog is an
	// emptyDir and needs no handoff.
	if live.Metadata.DeletionTimestamp != "" {
		return live, nil
	}

	// A live pod stamped with a different hash is stale, and so is one
	// carrying no stamp at all. The delete is the whole replacement:
	// the next pass finds no pod and creates the one it built.
	if !sameTemplate(&live.Metadata, &desired.Metadata) {
		if err := DeletePod(ctx, o.client, namespace, name); err != nil {
			return nil, err
		}
		return nil, nil
	}
	return live, nil
}

// TemplateHash reduces one built spec to the string the annotation
// carries. fnv-1a is enough, because the whole job is to tell one
// pass's output from another's. Nothing signs the value and nothing
// outside this operator reads it, so the hash needs no collision
// resistance against an attacker.
//
// The input is the spec alone and never the metadata, so the
// annotation is not part of what it hashes and a stamped pod hashes to
// the same value as the pod before the stamp.
func templateHash(spec any) (string, error) {
	body, err := json.Marshal(spec)
	if err != nil {
		return "", err
	}
	sum := fnv.New64a()
	// A hash never fails a write, so the error is the interface's and
	// not a state this code can reach.
	_, _ = sum.Write(body)
	return strconv.FormatUint(sum.Sum64(), 16), nil
}

// StampTemplateHash writes the hash of one built spec onto the object
// that carries it. The caller hands in the metadata and the spec of
// the same object, and the stamp is what a later pass compares
// against.
func stampTemplateHash(metadata *ObjectMeta, spec any) error {
	hash, err := templateHash(spec)
	if err != nil {
		return err
	}
	if metadata.Annotations == nil {
		metadata.Annotations = map[string]string{}
	}
	metadata.Annotations[templateHashAnnotation] = hash
	return nil
}

// SameTemplate reports whether a live pod carries the hash the pass
// just stamped on the pod it built. An absent annotation reads as an
// empty string, which never equals a hash, so a pod created by
// anything but this operator counts as diverged.
func sameTemplate(live, desired *ObjectMeta) bool {
	return live.Annotations[templateHashAnnotation] == desired.Annotations[templateHashAnnotation]
}
