package main

// match.go decides which PersistentVolumes a push moves, and writes
// the demand on them. The Secret the path named narrows the list
// first, so a push that verifies against one team's Secret can only
// ever mark that team's volumes.

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// mark writes the demand on every PersistentVolume the push matches
// and answers how many it marked. A person who configures a webhook
// reads that count, so a list the API server refuses answers the error
// and not a count of zero.
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
		held := &volumes.Items[i]
		if !matchesPush(held, namespace, secret, keys, pushed.ref) {
			continue
		}
		if err := w.demandPull(ctx, held.Name); err != nil {
			w.logger.WarnContext(ctx, "the volume was not marked",
				"volume", held.Name, "error", err)
			continue
		}
		marked++
	}
	w.readings.webhookMarked(marked)
	return marked, nil
}

// demandAll marks every read-only volume of this driver. A webhook
// that arrived while the Deployment restarted is lost, and a volume
// that pulls on demand has no timer to cover the loss, so the
// controller's start is a demand on every volume.
func (w *webhook) demandAll(ctx context.Context) {
	if w.client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, restartDeadline)
	defer cancel()

	volumes, err := w.client.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		w.logger.WarnContext(ctx, "the volumes were not listed", "error", err)
		return
	}
	marked := 0
	for i := range volumes.Items {
		held := &volumes.Items[i]
		source := held.Spec.CSI
		if source == nil || source.Driver != driverName || !readOnlyClaimVolume(held) {
			continue
		}
		if err := w.demandPull(ctx, held.Name); err != nil {
			w.logger.WarnContext(ctx, "the volume was not marked",
				"volume", held.Name, "error", err)
			continue
		}
		marked++
	}
	w.logger.InfoContext(ctx, "the controller demanded a pull on start", "marked", marked)
}

// demandPull writes the annotation the node plugins watch. The merge
// patch names the one annotation, so nothing else on the object
// moves.
func (w *webhook) demandPull(ctx context.Context, name string) error {
	patch := fmt.Sprintf(`{"metadata":{"annotations":{%q:%q}}}`,
		demandAnnotation, w.now().UTC().Format(time.RFC3339))
	_, err := w.client.CoreV1().PersistentVolumes().
		Patch(ctx, name, types.MergePatchType, []byte(patch), metav1.PatchOptions{})
	return err
}

// matchesPush reads one PersistentVolume against the push. It matches
// when the volume is this driver's, names this Secret, is bound to a
// claim in this namespace, is read-only, and follows a URL and a ref
// the push names.
func matchesPush(
	held *corev1.PersistentVolume, namespace, secret string, keys map[string]bool, ref string,
) bool {
	source := held.Spec.CSI
	if source == nil || source.Driver != driverName {
		return false
	}
	if source.VolumeAttributes[webhookSecretAttribute] != secret {
		return false
	}
	if held.Spec.ClaimRef == nil || held.Spec.ClaimRef.Namespace != namespace {
		return false
	}
	// While a pod holds a writeable tree, only the application changes
	// it, so a demand never reaches one.
	if !readOnlyClaimVolume(held) {
		return false
	}
	url := source.VolumeAttributes["url"]
	if url == "" || !keys[comparisonKey(url)] {
		return false
	}
	return refMatches(ref, volumeRef(source.VolumeAttributes))
}

// volumeRef is the ref the volume follows, which is main when the
// attributes name none, the way parseVolumeContext reads it.
func volumeRef(attributes map[string]string) string {
	if ref := attributes["ref"]; ref != "" {
		return ref
	}
	return defaultRef
}

// readOnlyClaimVolume reports the kind the way stageKind does at the
// node. The kubelet sends ReadOnlyMany as MULTI_NODE_READER_ONLY, which
// stages a read-only claim. Every other mode a PersistentVolume carries
// stages a writeable volume.
func readOnlyClaimVolume(held *corev1.PersistentVolume) bool {
	if len(held.Spec.AccessModes) == 0 {
		return false
	}
	for _, mode := range held.Spec.AccessModes {
		if mode != corev1.ReadOnlyMany {
			return false
		}
	}
	return true
}

// refMatches reads the payload's full ref against the branch or tag
// name a volume's ref attribute carries.
func refMatches(pushed, followed string) bool {
	return pushed == followed ||
		pushed == "refs/heads/"+followed ||
		pushed == "refs/tags/"+followed
}

// comparisonKeys is the key of every URL the payload named.
func comparisonKeys(urls []string) map[string]bool {
	keys := map[string]bool{}
	for _, url := range urls {
		if key := comparisonKey(url); key != "" {
			keys[key] = true
		}
	}
	return keys
}

// comparisonKey reduces a URL to the lowercased host and the path. The
// scheme, the userinfo, the port, a leading and a trailing slash, and a
// trailing .git are removed, so the https, ssh, and scp-like forms of
// one repository make one key. The port goes because a volume often
// clones over one port while the forge advertises another.
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
	// which is git's scp-like form. With a scheme it starts the port.
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
