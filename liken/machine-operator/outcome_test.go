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

// An answer from the API server sorts into a failure that a retry can
// clear, a failure that will not clear by itself, or no failure.
func TestAnAPIAnswerSortsByWhetherARetryCanClearIt(t *testing.T) {
	cases := []struct {
		name   string
		answer apiclient.Outcome
		want   []failureKind
	}{
		{"a 200 to a read", apiclient.Outcome{Method: http.MethodGet, Status: 200}, nil},
		{"a 404, an absent object", apiclient.Outcome{Method: http.MethodGet, Status: 404, Err: apiclient.ErrNotFound}, nil},
		{"a 409", apiclient.Outcome{Method: http.MethodPut, Status: 409, Err: apiclient.ErrConflict}, []failureKind{transient}},
		{"a 429", apiclient.Outcome{Method: http.MethodPut, Status: 429, Err: apiclient.ErrThrottled}, []failureKind{transient}},
		{"a 401, a token the kubelet has not refreshed yet", apiclient.Outcome{Method: http.MethodGet, Status: 401, Err: errors.New("401")}, []failureKind{transient}},
		{"a 503", apiclient.Outcome{Method: http.MethodGet, Status: 503, Err: errors.New("503")}, []failureKind{transient}},
		{"no answer", apiclient.Outcome{Method: http.MethodGet, Err: errors.New("connection refused")}, []failureKind{transient}},
		{"a 403", apiclient.Outcome{Method: http.MethodGet, Status: 403, Err: errors.New("403")}, []failureKind{lasting}},
		{"a 422", apiclient.Outcome{Method: http.MethodPatch, Status: 422, Err: errors.New("422")}, []failureKind{lasting}},
		{"a 200 whose body did not decode", apiclient.Outcome{Method: http.MethodGet, Status: 200, Err: errors.New("unexpected EOF")}, []failureKind{transient}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := &passOutcome{}
			out.observe(c.answer)
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

// A write to the machine sorts by its errno: a name or a value the
// kernel refuses fails the same way every time, and anything else can
// clear.
func TestALocalFailureSortsByItsErrno(t *testing.T) {
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

// A read of the facts that fails is retried soon whatever its errno,
// because init writes the facts, and a file that is missing now is a
// file init has not written yet.
func TestAFailedFactsReadRetriesSoon(t *testing.T) {
	out := &passOutcome{}
	out.failSoon("reading the facts", fmt.Errorf("reading: %w", fs.ErrNotExist))

	if got := failureKinds(out); !slices.Equal(got, []failureKind{transient}) {
		t.Errorf("kinds = %v, want transient", got)
	}
}

// A successful write to the API server and a local write each leave a
// note, and a read leaves none.
func TestAPassNotesEachWriteItMade(t *testing.T) {
	out := &passOutcome{}
	out.observe(apiclient.Outcome{Method: http.MethodGet, Path: "/api/v1/nodes/node-1", Status: 200})
	out.observe(apiclient.Outcome{Method: http.MethodPatch, Path: "/api/v1/nodes/node-1", Status: 200})
	out.wrote("hosts")

	if want := []string{"PATCH /api/v1/nodes/node-1", "hosts"}; !slices.Equal(out.writes, want) {
		t.Errorf("writes = %q, want %q", out.writes, want)
	}
}

// A write that met a conflict and then landed, as a status write does
// after it reads the Machine again, is a write and not a failure.
func TestAWriteThatLandsAfterAConflictIsNotAFailure(t *testing.T) {
	out := &passOutcome{}
	path := "/apis/liken.sh/v1alpha1/machines/node-1/status"
	out.observe(apiclient.Outcome{Method: http.MethodPut, Path: path, Status: 409, Err: apiclient.ErrConflict})
	out.observe(apiclient.Outcome{Method: http.MethodGet, Path: "/apis/liken.sh/v1alpha1/machines/node-1", Status: 200})
	out.observe(apiclient.Outcome{Method: http.MethodPut, Path: path, Status: 200})

	if len(out.failures) != 0 || !slices.Equal(out.writes, []string{"PUT " + path}) {
		t.Errorf("failures %v and writes %q, want no failure and one write", out.failures, out.writes)
	}
}

// Several writes share one path: the taints, the labels, the cordon,
// and the uncordon each patch the Node. One that lands does not erase
// another's failure, so a failed taint patch still retries after the
// label patch in the same pass lands.
func TestAWriteThatLandsKeepsAnotherWritesFailureOnTheSamePath(t *testing.T) {
	out := &passOutcome{}
	path := "/api/v1/nodes/node-1"
	out.observe(apiclient.Outcome{Method: http.MethodPatch, Path: path, Status: 503, Err: errors.New("503")})
	out.observe(apiclient.Outcome{Method: http.MethodPatch, Path: path, Status: 200})

	if got := failureKinds(out); !slices.Equal(got, []failureKind{transient}) {
		t.Errorf("kinds = %v, want the taint patch's failure kept", got)
	}
}

// A write whose answer did not decode still landed, so it counts as a
// write as well as a failure.
func TestAWriteWhoseAnswerDidNotDecodeIsAWrite(t *testing.T) {
	out := &passOutcome{}
	out.observe(apiclient.Outcome{Method: http.MethodPatch, Path: "/api/v1/nodes/node-1", Status: 200, Err: errors.New("unexpected EOF")})

	if !slices.Equal(out.writes, []string{"PATCH /api/v1/nodes/node-1"}) {
		t.Errorf("writes = %q, want the patch", out.writes)
	}
}

// A pass keeps the earliest wake a step asked for.
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

// A nil outcome records nothing, so a test that drives one step alone
// can pass nil.
func TestANilOutcomeRecordsNothing(t *testing.T) {
	var out *passOutcome
	out.fail("sysctls", unix.EIO)
	out.wrote("hosts")
	out.wakeBy(time.Now())
	out.observe(apiclient.Outcome{Status: 503, Err: errors.New("503")})
}
