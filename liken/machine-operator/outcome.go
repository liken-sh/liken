package main

// A passOutcome records which steps of one reconcile pass failed, what
// the pass wrote, and when it needs to run again.
//
// A pass continues after a failed step, so one broken step does not
// hide what the other steps observed. The loop sets one retry timer
// from all the failures of the pass (retry.go).
//
// Steps do not report API failures themselves. The pass's client has
// an observer (apiclient.WithObserver) that receives the final result
// of every request, including a failure that the calling step only
// logs, so every failed request reaches the outcome. A failure on the
// machine, such as a store write or a sysctl write, does not go
// through the client. The step that makes it calls fail.

import (
	"errors"
	"io/fs"
	"net/http"
	"slices"
	"time"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// failureKind records whether retrying the same step can succeed.
type failureKind int

const (
	// transient is a failure that can clear by itself: a 5xx, a 429, a
	// timeout, a failed connection, or a busy device. The loop retries
	// soon.
	transient failureKind = iota

	// lasting is a failure that repeats on every try until someone
	// changes something: a 403, a 422, or a sysctl name the kernel
	// does not have. The loop retries later. A change to the
	// Machine's spec starts a pass through the watch. A change that
	// the operator does not watch, such as an RBAC grant or an
	// admission webhook, takes effect at the next retry, up to five
	// minutes later.
	lasting
)

func (k failureKind) String() string {
	if k == lasting {
		return "lasting"
	}
	return "transient"
}

// passFailure is one failed step of a pass.
type passFailure struct {
	step string
	kind failureKind
	err  error
}

// passOutcome is the record of one pass. Its methods do nothing on a
// nil outcome, so a caller that runs one step alone can pass nil.
type passOutcome struct {
	failures []passFailure

	// writes lists each write the pass made, to the API server and to
	// the machine.
	writes []string

	// sysctls is the value the kernel reported for each parameter the
	// pass applied, which the loop's periodic sysctl check reads
	// (backstop.go). It is nil when the pass did not reach the sysctl
	// step.
	sysctls map[string]string

	// sysctlsMissing lists the parameters the pass could not apply
	// because their file does not exist. The same check reads it.
	sysctlsMissing []string

	// requestSizes is the max_sectors_kb that the pass read back from
	// each disk it set (disksettings.go), which the same check reads.
	requestSizes map[string]uint64

	// wake is the earliest time that a step asked for another pass, or
	// zero when no step asked.
	wake time.Time

	// notBefore is the earliest time to try again, taken from the
	// Retry-After of a 429, or zero. The client waits out a 429 for up
	// to 10 seconds in total. When the API server still answers 429
	// after that, the client returns ErrThrottled with the wait the
	// server asked for, and the retry schedule honors it.
	notBefore time.Time
}

func (o *passOutcome) failureCount() int {
	if o == nil {
		return 0
	}
	return len(o.failures)
}

// fail records a failure on the machine itself. The failure is lasting
// when its errno is one that lastingErrno lists, or when it is an
// io/fs not-exist, permission, or invalid-argument error, which Go can
// return before any system call. Every other error is transient.
func (o *passOutcome) fail(step string, err error) {
	if o == nil || err == nil {
		return
	}
	kind := transient
	var errno unix.Errno
	if errors.As(err, &errno) && lastingErrno(errno) ||
		errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) || errors.Is(err, fs.ErrInvalid) {
		kind = lasting
	}
	o.failures = append(o.failures, passFailure{step: step, kind: kind, err: err})
}

// lastingErrno reports whether a call that failed with errno fails the
// same way until someone changes the machine or the spec: a missing
// path, a value or name the kernel refuses, a sysctl name that goes
// below a parameter or names a directory, a permission error, or a
// read-only file system. Other errnos, such as EIO and EBUSY, can
// clear.
func lastingErrno(errno unix.Errno) bool {
	switch errno {
	case unix.ENOENT, unix.EINVAL, unix.ENOTDIR, unix.EISDIR, unix.EACCES, unix.EPERM, unix.EROFS:
		return true
	}
	return false
}

// failSoon records a transient failure whatever the error is. It is
// for a read of a file that another process writes, such as the facts
// that init writes. That read fails with ENOENT until init writes the
// file, and it must not wait up to five minutes the way a missing
// kernel parameter does.
func (o *passOutcome) failSoon(step string, err error) {
	if o == nil || err == nil {
		return
	}
	o.failures = append(o.failures, passFailure{step: step, kind: transient, err: err})
}

// wrote records a write to the machine.
func (o *passOutcome) wrote(step string) {
	if o == nil {
		return
	}
	o.writes = append(o.writes, step)
}

// wakeBy asks for another pass no later than at.
func (o *passOutcome) wakeBy(at time.Time) {
	if o == nil {
		return
	}
	if o.wake.IsZero() || at.Before(o.wake) {
		o.wake = at
	}
}

// observe records the result of one request from the pass's client. A
// 404 is not a failure, because the pass treats a missing object as
// state, such as a Lease it has to create. A 2xx response to a write
// counts as a write even when its body did not decode, because the API
// server stored the change.
//
// A successful request removes an earlier 409 for the same method and
// path, and no other failure. The status write and the heartbeat
// handle a 409 by reading the object again and writing a second time,
// so when the second write succeeds, nothing is left to retry. Other
// failures stay, because several writes share one path. The taints,
// labels, cordon, and uncordon each patch the Node, and one successful
// patch does not mean that a different patch succeeded.
func (o *passOutcome) observe(result apiclient.Outcome) {
	if o == nil {
		return
	}
	step := result.Method + " " + result.Path
	if result.Status >= 200 && result.Status <= 299 && result.Method != http.MethodGet {
		o.writes = append(o.writes, step)
	}
	switch {
	case result.Status == http.StatusNotFound:
	case result.Err == nil:
		o.failures = slices.DeleteFunc(o.failures, func(f passFailure) bool {
			return f.step == step && errors.Is(f.err, apiclient.ErrConflict)
		})
	default:
		o.failures = append(o.failures, passFailure{step: step, kind: statusKind(result.Status), err: result.Err})
		if seconds := apiclient.RetryAfterSeconds(result.Err); seconds > 0 {
			if at := time.Now().Add(time.Duration(seconds) * time.Second); at.After(o.notBefore) {
				o.notBefore = at
			}
		}
	}
}

// statusKind returns the failureKind for a failed request by its HTTP
// status. A 2xx with a body that did not decode means the connection
// broke partway through. A 409 means another writer changed the object
// after the pass read it, and the next pass writes from a fresh copy.
// A 401 means the token on disk expired, and the kubelet refreshes it.
// A 408, a 429, a 5xx, and status 0 (no response) are failures of the
// API server or the network. Every other 4xx refuses the request
// itself, and the same request gets the same response until someone
// changes the spec or the RBAC.
func statusKind(status int) failureKind {
	switch {
	case status == 0, status >= 500, status >= 200 && status <= 299,
		status == http.StatusConflict, status == http.StatusUnauthorized,
		status == http.StatusRequestTimeout, status == http.StatusTooManyRequests:
		return transient
	}
	return lasting
}
