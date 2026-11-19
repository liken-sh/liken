package main

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"slices"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// A response from the API server is classified as a transient failure,
// a lasting failure, or no failure.
func TestAnAPIResponseIsClassifiedByWhetherARetryCanClearIt(t *testing.T) {
	cases := []struct {
		name    string
		outcome apiclient.Outcome
		want    []failureKind
	}{
		{"a 200 to a read", apiclient.Outcome{Method: http.MethodGet, Status: 200}, nil},
		{"a 404, an absent object", apiclient.Outcome{Method: http.MethodGet, Status: 404, Err: apiclient.ErrNotFound}, nil},
		{"a 409", apiclient.Outcome{Method: http.MethodPut, Status: 409, Err: apiclient.ErrConflict}, []failureKind{transient}},
		{"a 429", apiclient.Outcome{Method: http.MethodPut, Status: 429, Err: apiclient.ErrThrottled}, []failureKind{transient}},
		{"a 401, a token the kubelet has not refreshed yet", apiclient.Outcome{Method: http.MethodGet, Status: 401, Err: errors.New("401")}, []failureKind{transient}},
		{"a 503", apiclient.Outcome{Method: http.MethodGet, Status: 503, Err: errors.New("503")}, []failureKind{transient}},
		{"no response", apiclient.Outcome{Method: http.MethodGet, Err: errors.New("connection refused")}, []failureKind{transient}},
		{"a 403", apiclient.Outcome{Method: http.MethodGet, Status: 403, Err: errors.New("403")}, []failureKind{lasting}},
		{"a 422", apiclient.Outcome{Method: http.MethodPatch, Status: 422, Err: errors.New("422")}, []failureKind{lasting}},
		{"a 200 whose body did not decode", apiclient.Outcome{Method: http.MethodGet, Status: 200, Err: errors.New("unexpected EOF")}, []failureKind{transient}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := &passOutcome{}
			out.observe(c.outcome)
			if got := failureKinds(out); !slices.Equal(got, c.want) {
				t.Errorf("kinds = %v, want %v", got, c.want)
			}
		})
	}
}

func failureKinds(out *passOutcome) []failureKind {
	var kinds []failureKind
	for _, f := range out.failures {
		kinds = append(kinds, f.kind)
	}
	return kinds
}

// A failed write on the machine is lasting when its errno means the
// same write will fail again, such as a name or value the kernel
// refuses. Any other error is transient.
func TestAFailureOnTheMachineIsClassifiedByItsErrno(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want failureKind
	}{
		{"a missing file", fmt.Errorf("writing: %w", fs.ErrNotExist), lasting},
		{"a value the kernel refuses", &os.PathError{Op: "write", Path: "/proc/sys/x", Err: unix.EINVAL}, lasting},
		{"a read-only file system", &os.PathError{Op: "open", Path: "/host/etc/hosts", Err: unix.EROFS}, lasting},
		{"a permission refused", &os.PathError{Op: "open", Path: "/proc/sys/x", Err: unix.EACCES}, lasting},
		{"a name under a parameter file", &os.PathError{Op: "open", Path: "/proc/sys/net/ipv4/ip_forward/x", Err: unix.ENOTDIR}, lasting},
		{"a name that is a directory", &os.PathError{Op: "open", Path: "/proc/sys/net/ipv4", Err: unix.EISDIR}, lasting},
		{"a name that escapes the tree", fmt.Errorf("sysctl name %q escapes /proc/sys: %w", "../x", fs.ErrInvalid), lasting},
		{"a busy device", &os.PathError{Op: "write", Path: "/proc/sys/x", Err: unix.EBUSY}, transient},
		{"an I/O error", &os.PathError{Op: "write", Path: "/var/lib/liken", Err: unix.EIO}, transient},
		{"an error with no errno", errors.New("the record does not parse"), transient},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := &passOutcome{}
			out.fail("sysctls", c.err)
			if got := failureKinds(out); !slices.Equal(got, []failureKind{c.want}) {
				t.Errorf("kinds = %v, want %v", got, []failureKind{c.want})
			}
		})
	}
}

// A failed read of the facts is transient whatever its errno, because
// a missing file means init has not written it yet.
func TestAFailedFactsReadRetriesSoon(t *testing.T) {
	out := &passOutcome{}
	out.failSoon("reading the facts", fmt.Errorf("reading: %w", fs.ErrNotExist))

	if got := failureKinds(out); !slices.Equal(got, []failureKind{transient}) {
		t.Errorf("kinds = %v, want transient", got)
	}
}

// A successful write to the API server and a write on the machine are
// both recorded in writes. A read is not.
func TestAPassRecordsEachWriteItMade(t *testing.T) {
	out := &passOutcome{}
	out.observe(apiclient.Outcome{Method: http.MethodGet, Path: "/api/v1/nodes/node-1", Status: 200})
	out.observe(apiclient.Outcome{Method: http.MethodPatch, Path: "/api/v1/nodes/node-1", Status: 200})
	out.wrote("hosts")

	if want := []string{"PATCH /api/v1/nodes/node-1", "hosts"}; !slices.Equal(out.writes, want) {
		t.Errorf("writes = %q, want %q", out.writes, want)
	}
}

// A write that got a 409 and then succeeded, as a status write does
// after it reads the Machine again, is recorded as a write and not as
// a failure.
func TestAWriteThatSucceedsAfterAConflictIsNotAFailure(t *testing.T) {
	out := &passOutcome{}
	path := "/apis/liken.sh/v1alpha1/machines/node-1/status"
	out.observe(apiclient.Outcome{Method: http.MethodPut, Path: path, Status: 409, Err: apiclient.ErrConflict})
	out.observe(apiclient.Outcome{Method: http.MethodGet, Path: "/apis/liken.sh/v1alpha1/machines/node-1", Status: 200})
	out.observe(apiclient.Outcome{Method: http.MethodPut, Path: path, Status: 200})

	if len(out.failures) != 0 || !slices.Equal(out.writes, []string{"PUT " + path}) {
		t.Errorf("failures %v and writes %q, want no failure and one write", out.failures, out.writes)
	}
}

// Several writes share one path, because the taints, labels, cordon,
// and uncordon each patch the Node. A successful patch does not clear
// another patch's failure, so a failed taint patch is still retried
// after the label patch in the same pass succeeds.
func TestASuccessfulWriteKeepsAnotherWritesFailureOnTheSamePath(t *testing.T) {
	out := &passOutcome{}
	path := "/api/v1/nodes/node-1"
	out.observe(apiclient.Outcome{Method: http.MethodPatch, Path: path, Status: 503, Err: errors.New("503")})
	out.observe(apiclient.Outcome{Method: http.MethodPatch, Path: path, Status: 200})

	if got := failureKinds(out); !slices.Equal(got, []failureKind{transient}) {
		t.Errorf("kinds = %v, want the taint patch's failure kept", got)
	}
}

// A 2xx write whose response did not decode is recorded as a write,
// because the API server stored it, and as a failure.
func TestAWriteWhoseResponseDidNotDecodeIsAWrite(t *testing.T) {
	out := &passOutcome{}
	out.observe(apiclient.Outcome{Method: http.MethodPatch, Path: "/api/v1/nodes/node-1", Status: 200, Err: errors.New("unexpected EOF")})

	if !slices.Equal(out.writes, []string{"PATCH /api/v1/nodes/node-1"}) {
		t.Errorf("writes = %q, want the patch", out.writes)
	}
}

// The outcome keeps the earliest wake time that a step asked for.
func TestAPassKeepsTheEarliestWake(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	out := &passOutcome{}
	out.wakeBy(now.Add(time.Minute))
	out.wakeBy(now.Add(10 * time.Second))
	out.wakeBy(now.Add(time.Hour))

	if want := now.Add(10 * time.Second); !out.wake.Equal(want) {
		t.Errorf("wake = %s, want %s", out.wake, want)
	}
}

// The methods of a nil passOutcome do nothing and do not panic.
func TestANilOutcomeRecordsNothing(t *testing.T) {
	var out *passOutcome
	out.fail("sysctls", unix.EIO)
	out.wrote("hosts")
	out.wakeBy(time.Now())
	out.observe(apiclient.Outcome{Status: 503, Err: errors.New("503")})
}
