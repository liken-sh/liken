package main

// listwatch.go keeps the node's view of API objects current with
// events. A read of the whole state on a timer loads the API server and
// finds a change only at the next tick, so the node reads the whole
// state once, then follows every change after that read through a
// watch.

import (
	"context"
	"log/slog"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

// defaultRetry is how long the node waits after a call to the API
// server failed before it makes the call again. It bounds the load a
// refusing API server takes from each list and watch to one call per
// wait.
const defaultRetry = 30 * time.Second

// listWatch is one list and the watch that continues from it. The watch
// opens at the list's resourceVersion, so the API server sends every
// change made after the list, and a change between the list and the
// watch is not lost.
type listWatch struct {
	// kind labels git_csi_watch_restarts_total and the log lines.
	kind string
	// list reads the whole state, acts on each object in it, and
	// returns the list's resourceVersion.
	list func(ctx context.Context) (string, error)
	// watch opens a watch with the options the loop gives it.
	watch func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error)
	// act reads one event. An error from act means the node could not
	// finish reading the state, so the loop waits and lists again.
	act func(ctx context.Context, event watch.Event) error

	retry    time.Duration
	logger   *slog.Logger
	readings *metrics

	// opened is true once a watch has opened, so every later watch
	// counts as a restart.
	opened bool
}

// versioned is an object that carries a resourceVersion. Every object
// an API server sends does, including the bookmark that only moves the
// version forward.
type versioned interface {
	GetResourceVersion() string
}

// shortWatch is the shortest life of a healthy watch. A watch that
// closes sooner failed, whatever it sent, so the next one waits out the
// retry first. Without the wait, a server that closes every watch at
// once takes a watch call as fast as it answers.
const shortWatch = time.Second

// follow holds the list and the watch until the context ends. An empty
// version means the next pass lists, and any other version is where
// the next watch resumes.
//
// A 410 Gone lists again at once, but only once in a row. A server
// that answers 410 to the version of a list it just made answers 410
// to the next one too, so the loop waits out the retry before that
// list, or it lists and watches as fast as the server answers.
func (l *listWatch) follow(ctx context.Context) {
	version := ""
	relisted := false
	for ctx.Err() == nil {
		if version == "" {
			listed, err := l.list(ctx)
			if err != nil {
				l.logger.WarnContext(ctx, "the list failed", "kind", l.kind, "error", err)
				waitOut(ctx, l.retry)
				continue
			}
			version = listed
		}
		next, gone := l.hold(ctx, version)
		if gone && relisted {
			waitOut(ctx, l.retry)
		}
		relisted = gone
		version = next
	}
}

// hold watches from the version until the watch ends. It returns the
// version the next watch resumes from, or an empty version when the
// next pass must list, and whether the watch ended on a 410 Gone.
func (l *listWatch) hold(ctx context.Context, from string) (string, bool) {
	// A bookmark carries the newest resourceVersion while no object
	// changes, so a watch that the API server closes resumes from a
	// version the server still serves.
	watching, err := l.watch(ctx, metav1.ListOptions{
		ResourceVersion:     from,
		AllowWatchBookmarks: true,
	})
	if expired(err) {
		// 410 Gone: the server no longer holds the changes after this
		// version, so only a new list reads them.
		l.logger.InfoContext(ctx, "the watch version expired", "kind", l.kind, "error", err)
		return "", true
	}
	if err != nil {
		l.logger.WarnContext(ctx, "the watch failed", "kind", l.kind, "error", err)
		waitOut(ctx, l.retry)
		return from, false
	}
	defer watching.Stop()
	if l.opened {
		l.readings.watchRestarted(l.kind)
	}
	l.opened = true
	began := time.Now()

	version := from
	for {
		select {
		case <-ctx.Done():
			return version, false
		case event, open := <-watching.ResultChan():
			if !open {
				// The API server closes a healthy watch after its
				// request timeout, and the next watch resumes at once.
				if time.Since(began) < shortWatch {
					waitOut(ctx, l.retry)
				}
				return version, false
			}
			if event.Type == watch.Error {
				// A 410 Gone arrives as an error event, and so does any
				// other failure on the stream. The node lists again,
				// because it cannot tell which changes it missed. Only
				// a 410 lists at once: any other error waits first, so
				// a server that fails every watch takes one list per
				// wait.
				err := apierrors.FromObject(event.Object)
				l.logger.InfoContext(ctx, "the watch ended with an error", "kind", l.kind,
					"error", err)
				if expired(err) {
					return "", true
				}
				waitOut(ctx, l.retry)
				return "", false
			}
			held, decoded := event.Object.(versioned)
			if !decoded {
				// An object the client could not decode carries no
				// version to resume from, so the event counts as an
				// error. Without the wait, the loop opens the watch at
				// the same version again and reads the same event.
				l.logger.WarnContext(ctx, "the event did not decode", "kind", l.kind,
					"type", event.Type)
				waitOut(ctx, l.retry)
				return "", false
			}
			if held.GetResourceVersion() != "" {
				version = held.GetResourceVersion()
			}
			if err := l.act(ctx, event); err != nil {
				l.logger.WarnContext(ctx, "the event was not read", "kind", l.kind, "error", err)
				waitOut(ctx, l.retry)
				return "", false
			}
		}
	}
}

// expired reports whether the error is a 410 Gone, which the API server
// sends with the reason Expired or the reason Gone.
func expired(err error) bool {
	return apierrors.IsResourceExpired(err) || apierrors.IsGone(err)
}

// waitOut waits out the retry after a call that failed, and ends early
// when the context does.
func waitOut(ctx context.Context, retry time.Duration) {
	timer := time.NewTimer(retry)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
