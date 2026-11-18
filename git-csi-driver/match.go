package main

// match.go finds the PersistentVolumes that a push applies to and
// annotates them with a demand. The Secret named in the request path
// narrows the list first, so a push verified against one team's Secret
// can annotate only that team's volumes.

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// mark annotates every PersistentVolume that matches the push with a
// demand and returns how many it annotated. A list that the API server
// refuses returns the error and not zero, because the person who
// configures a webhook reads the count.
//
// mark lists PersistentVolumes on every push and does not watch them. A
// watch would keep a copy of every PersistentVolume in the cluster for
// the controller's whole life, to serve requests that come only when
// someone pushes.
func (w *webhook) mark(
	ctx context.Context, namespace, secret string, pushed push,
) (int, error) {
	volumes, err := w.client.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return 0, err
	}
	keys := comparisonKeys(pushed.urls)
	marked := 0
	for i := range volumes.Items {
		pv := &volumes.Items[i]
		if !matchesPush(pv, namespace, secret, keys, pushed.ref) {
			continue
		}
		if err := w.demandPull(ctx, pv.Name); err != nil {
			w.logger.WarnContext(ctx, "The pull demand was not set on the PersistentVolume",
				"volume", pv.Name, "error", err)
			continue
		}
		marked++
	}
	w.metrics.webhookMarked(marked)
	return marked, nil
}

// demandAll demands a pull on every read-only volume of this driver
// when the controller starts. A webhook that arrives while the
// Deployment restarts is lost, and a volume with pull set to on-demand
// has no timer to make up for it.
func (w *webhook) demandAll(ctx context.Context) {
	if w.client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, restartDeadline)
	defer cancel()

	volumes, err := w.client.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		w.logger.WarnContext(ctx, "The PersistentVolumes were not listed, so the controller demanded no pull on start", "error", err)
		return
	}
	marked := 0
	for i := range volumes.Items {
		pv := &volumes.Items[i]
		source := pv.Spec.CSI
		if source == nil || source.Driver != driverName || !readOnlyClaimVolume(pv) {
			continue
		}
		if err := w.demandPull(ctx, pv.Name); err != nil {
			w.logger.WarnContext(ctx, "The pull demand was not set on the PersistentVolume",
				"volume", pv.Name, "error", err)
			continue
		}
		marked++
	}
	w.logger.InfoContext(ctx, "The controller demanded a pull on the read-only volumes at start", "marked", marked)
}

// demandPull sets the annotation that the node plugins watch. It sends
// a merge patch of that one annotation, so no other field of the object
// changes.
func (w *webhook) demandPull(ctx context.Context, name string) error {
	patch := fmt.Sprintf(`{"metadata":{"annotations":{%q:%q}}}`,
		demandAnnotation, w.now().UTC().Format(time.RFC3339Nano))
	_, err := w.client.CoreV1().PersistentVolumes().
		Patch(ctx, name, types.MergePatchType, []byte(patch), metav1.PatchOptions{})
	return err
}

// matchesPush reports whether the push applies to the PersistentVolume.
// It does when the volume belongs to this driver, names this Secret as
// its webhookSecret, is bound to a claim in this namespace, is read-
// only, and follows a URL and a ref that the push names.
func matchesPush(
	pv *corev1.PersistentVolume, namespace, secret string, keys map[string]bool, ref string,
) bool {
	source := pv.Spec.CSI
	if source == nil || source.Driver != driverName {
		return false
	}
	if source.VolumeAttributes[webhookSecretAttribute] != secret {
		return false
	}
	if pv.Spec.ClaimRef == nil || pv.Spec.ClaimRef.Namespace != namespace {
		return false
	}
	// Only the application changes a writeable tree, so a push never
	// demands a pull on one.
	if !readOnlyClaimVolume(pv) {
		return false
	}
	url := source.VolumeAttributes["url"]
	if url == "" || !keys[comparisonKey(url)] {
		return false
	}
	return refMatches(ref, volumeRef(source.VolumeAttributes))
}

// volumeRef returns the ref the volume follows. It is main when the ref
// attribute is unset, the same rule as parseVolumeContext.
func volumeRef(attributes map[string]string) string {
	if ref := attributes["ref"]; ref != "" {
		return ref
	}
	return defaultRef
}

// readOnlyClaimVolume reports whether the volume stages as a read-only
// claim, by the same rule as stageKind on the node. The kubelet sends
// ReadOnlyMany as MULTI_NODE_READER_ONLY, which stages a read-only
// claim. Of the other access modes, ReadWriteOncePod stages a writeable
// volume and stageKind refuses the rest.
func readOnlyClaimVolume(pv *corev1.PersistentVolume) bool {
	if len(pv.Spec.AccessModes) == 0 {
		return false
	}
	for _, mode := range pv.Spec.AccessModes {
		if mode != corev1.ReadOnlyMany {
			return false
		}
	}
	return true
}

// refMatches reports whether the ref in the push payload is the branch
// or tag that a volume's ref attribute follows. The payload has the
// full ref, such as refs/heads/main, and the attribute has the
// short name.
func refMatches(pushed, followed string) bool {
	return pushed == followed ||
		pushed == "refs/heads/"+followed ||
		pushed == "refs/tags/"+followed
}

func comparisonKeys(urls []string) map[string]bool {
	keys := map[string]bool{}
	for _, url := range urls {
		if key := comparisonKey(url); key != "" {
			keys[key] = true
		}
	}
	return keys
}

// comparisonKey reduces a URL to its lowercased host and path. It
// removes the scheme, the userinfo, the port, leading and trailing
// slashes, and a trailing .git, so the https, ssh, and scp-like URLs of
// one repository give one key. It removes the port because a volume
// often clones over one port while the forge advertises another.
func comparisonKey(raw string) string {
	rest := raw
	_, after, scheme := strings.Cut(rest, "://")
	if scheme {
		rest = after
	}
	if _, after, userinfo := strings.Cut(rest, "@"); userinfo {
		rest = after
	}
	// Without a scheme, a colon before the first slash starts the path,
	// which is git's scp-like form. With a scheme, the colon starts the
	// port.
	if host, path, colon := strings.Cut(rest, ":"); !scheme && colon && !strings.Contains(host, "/") {
		rest = host + "/" + path
	}
	host, path, _ := strings.Cut(rest, "/")
	if named, _, port := strings.Cut(host, ":"); port {
		host = named
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	if path == "" {
		return strings.ToLower(host)
	}
	return strings.ToLower(host) + "/" + path
}
