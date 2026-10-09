package main

// A pass's outcome: what it could not finish, what it wrote, and when
// it asks to run again.
//
// A pass carries on past a failure. A write to the API server that
// fails is logged, a store write that fails becomes a condition, and
// the rest of the pass still runs, because one broken step must not
// hide what the others observed. The pass still needs to try the step
// again. The outcome collects each failure in one place, so the loop
// can set one timer for the retry (retry.go), and a step that waits for
// a deadline can ask the loop to wake it then.
//
// The API helpers do not report into the outcome one by one. The
// pass's client carries an observer (apiclient.WithObserver), which
// hears the final answer to every request the pass sends, including a
// failure that the step that sent it only logs. So no request can fail
// without a record in the outcome. A failure on the machine itself, such as
// a store write or a sysctl, comes from a call that the client never
// sees, and its step reports it with fail.

import (
	"errors"
	"io/fs"
	"net/http"
	"slices"
	"time"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// failureKind sorts a failure by whether a retry can clear it.
type failureKind int

const (
	// transient is a failure that can clear by itself: a 5xx, a 429, a
	// timeout, a connection that failed, a busy device. The next try
	// comes soon.
	transient failureKind = iota

	// lasting is a failure that fails the same way on every try until
	// somebody changes something: a 403, a 422, a sysctl name the
	// kernel does not have. The next try comes later. A fix to the
	// Machine's spec sends a watch event that runs a pass at once, and
	// a fix the operator does not watch, such as an RBAC grant or a
	// webhook, waits for the next try, up to five minutes.
	lasting
)

func (k failureKind) String() string {
	if k == lasting {
		return "lasting"
	}
	return "transient"
}

// passFailure is one step that did not finish.
type passFailure struct {
	step string
	kind failureKind
	err  error
}

// passOutcome is one pass's record. A nil outcome records nothing, so
// a test that drives one step alone can pass nil.
type passOutcome struct {
	failures []passFailure

	// writes names each write the pass made, to the API server and to
	// the machine.
	writes []string

	// sysctls is what the kernel reported for each parameter the pass
	// applied, for the loop's check of the sysctls (backstop.go). Nil
	// means the pass did not reach the step.
	sysctls map[string]string

	// sysctlsMissing names the parameters the pass could not apply
	// because their file does not exist, for the same check.
	sysctlsMissing []string

	// wake is the earliest time a step asked the loop to run a pass
	// again, or zero.
	wake time.Time

	// notBefore is the earliest time the API server allows the next
	// try, from the Retry-After of a 429, or zero. liken's client
	// answers a 429 at once, with no wait (kubernetes/apiclient.go), so
	// the wait it asked for falls to the retry.
	notBefore time.Time
}

// failureCount answers how many failures the pass has recorded so far.
// A nil outcome records none.
func (o *passOutcome) failureCount() int {
	if o == nil {
		return 0
	}
	return len(o.failures)
}

// fail records a failure on the machine itself. The kind comes from the
// errno, when the error carries one (lastingErrno), or from the io/fs
// error that a check of the code's own answers: a missing file, or a
// name that the code refuses before it reaches the kernel.
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

// lastingErrno answers whether a failed call fails the same way on each
// try. A path that does not exist, a value or a name the kernel
// refuses, a sysctl name that runs under a parameter or names a
// directory, a permission, and a read-only file system stay that way
// until somebody changes the machine or the spec. Anything else, such
// as EIO or EBUSY, can clear, and so can an error with no errno at all,
// which retries at the pace it always had.
func lastingErrno(errno unix.Errno) bool {
	switch errno {
	case unix.ENOENT, unix.EINVAL, unix.ENOTDIR, unix.EISDIR, unix.EACCES, unix.EPERM, unix.EROFS:
		return true
	}
	return false
}

// failSoon records a failure that a retry can clear whatever its error
// says. A read of a file that another process writes, such as init's
// facts, fails with ENOENT until that process writes it, and that
// failure must not wait the five minutes that a missing kernel
// parameter waits.
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

// wakeBy asks the loop to run a pass no later than at.
func (o *passOutcome) wakeBy(at time.Time) {
	if o == nil {
		return
	}
	if o.wake.IsZero() || at.Before(o.wake) {
		o.wake = at
	}
}

// observe is the pass client's observer. A 404 is not a failure: an
// absent object is a state the pass reads, such as a lease to create.
// A 2xx to a write is a write, even when its answer did not decode,
// because the API server stored it.
//
// A request that succeeds withdraws an earlier 409 on the same method
// and path, and nothing else. The status write and the heartbeat each
// answer a 409 by reading the object again and writing once more, and
// when that second write lands, nothing is left to retry. Any other
// failure stays, because several writes share one path: the taints,
// the labels, the cordon, and the uncordon each patch the Node, and
// one of them that lands says nothing about another that failed.
func (o *passOutcome) observe(answer apiclient.Outcome) {
	if o == nil {
		return
	}
	step := answer.Method + " " + answer.Path
	if answer.Status >= 200 && answer.Status <= 299 && answer.Method != http.MethodGet {
		o.writes = append(o.writes, step)
	}
	switch {
	case answer.Status == http.StatusNotFound:
	case answer.Err == nil:
		o.failures = slices.DeleteFunc(o.failures, func(f passFailure) bool {
			return f.step == step && errors.Is(f.err, apiclient.ErrConflict)
		})
	default:
		o.failures = append(o.failures, passFailure{step: step, kind: statusKind(answer.Status), err: answer.Err})
		if seconds := apiclient.RetryAfterSeconds(answer.Err); seconds > 0 {
			if at := time.Now().Add(time.Duration(seconds) * time.Second); at.After(o.notBefore) {
				o.notBefore = at
			}
		}
	}
}

// statusKind sorts a failed answer by its status. A 2xx whose body did
// not decode met a connection that failed partway. A 409 means the
// pass wrote from a copy another writer changed, and the next pass
// writes from a fresh one. A 401 means the token on disk expired, and
// the kubelet refreshes it. A 408, a 429, a 5xx, and no answer at all
// are the API server's or the network's trouble. Every other 4xx
// refuses the request itself, and the same request gets the same
// answer until somebody changes the spec or the RBAC.
func statusKind(status int) failureKind {
	switch {
	case status == 0, status >= 500, status >= 200 && status <= 299,
		status == http.StatusConflict, status == http.StatusUnauthorized,
		status == http.StatusRequestTimeout, status == http.StatusTooManyRequests:
		return transient
	}
	return lasting
}
