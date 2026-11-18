package main

// The trigger controller does not run a failed trigger again for the
// same transition. After a person fixes the cause, such as a driver
// that refused a dome's park, they add the observatory.liken.sh/retry
// annotation to the resource. The controller then reruns each failed
// run whose condition still holds with the same transition time,
// skips the actions that are already Done, and removes the annotation.
// A run whose transition has ended stays Failed.
//
// On a device, the annotation also reruns a failed activation that the
// trigger controller ran for it (lateactivation.go). Any other failed
// activation or deactivation is rerun by annotating the Reservation
// whose step failed.

import (
	"context"
	"encoding/json"
	"net/http"
)

// hasRetryAnnotation reports whether a resource has the retry annotation and
// the controller has not yet acted on this resourceVersion of it.
func (k *control) hasRetryAnnotation(r resource) bool {
	_, asked := r.meta.Annotations[annotationRetry]
	return asked && k.retried[r.key()] != r.meta.ResourceVersion
}

// removeRetryAnnotation removes a resource's retry annotation in its own
// goroutine, because send retries a refused write after a pause and
// the controller's pass must not wait for it.
func (o *operator) removeRetryAnnotation(ctx context.Context, r resource, k *control) {
	k.retried[r.key()] = r.meta.ResourceVersion
	k.group.Go(func() {
		path := objectPath(r.kind, o.namespace, r.name())
		_ = o.send(ctx, nil, "removing the retry annotation of "+r.String(), func() error {
			body, err := json.Marshal(map[string]any{"metadata": map[string]any{"annotations": map[string]any{annotationRetry: nil}}})
			if err != nil {
				return err
			}
			return o.client.Request(http.MethodPatch, path, mergePatch, body, nil)
		})
	})
	o.recorder.Normal(reference(r.kind, r.meta), "Retry", "Retrying each failed run of "+r.String()+" whose condition still holds")
}
