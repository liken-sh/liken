package main

// The retry annotation on a resource with procedures. A trigger's run
// that failed is not run again for the same transition, so a person
// who fixed the cause, such as a dome whose park a driver refused,
// asks for the run again with observatory.liken.sh/retry, as on a
// Reservation. The trigger controller runs again each failed run of
// the resource's triggers whose condition still holds with the same
// transition time, and skips its Done actions. It then removes the
// annotation. A run whose transition is over stays Failed, because
// its condition no longer asks for it. A lifecycle run, activation or
// deactivation, runs again through the retry annotation of the
// Reservation whose step it failed.

import (
	"context"
	"encoding/json"
	"net/http"
)

// retryAsked reports whether a resource carries the retry annotation
// and the controller has not acted on this version of it.
func (k *control) retryAsked(r resource) bool {
	_, asked := r.meta.Annotations[annotationRetry]
	return asked && k.retried[r.key()] != r.meta.ResourceVersion
}

// clearRetryOf removes a resource's retry annotation. The write goes
// out on its own goroutine, so a refused write, which send tries again
// after a pause, does not hold the controller's pass.
func (o *operator) clearRetryOf(ctx context.Context, r resource, k *control) {
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
