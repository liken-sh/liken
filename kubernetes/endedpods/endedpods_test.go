package endedpods_test

import (
	"testing"
	"time"

	"github.com/liken-sh/liken/kubernetes/endedpods"
)

// Only the kubelet puts a pod with restartPolicy Always in a final
// phase.
func TestAPodThatRestartsEndsOnlyInAFinalPhase(t *testing.T) {
	cases := []struct {
		phase string
		want  bool
	}{
		{"Failed", true},
		{"Succeeded", true},
		{"Running", false},
		{"Pending", false},
		{"", false},
	}
	for _, c := range cases {
		t.Run(c.phase, func(t *testing.T) {
			if got := endedpods.Ended(c.phase); got != c.want {
				t.Errorf("Ended(%q) = %t, want %t", c.phase, got, c.want)
			}
		})
	}
}

// The kubelet's end of a pod is a Failed phase with a DisruptionTarget
// condition, or with a pod-level reason other than DeadlineExceeded. A
// program that exits non-zero leaves the pod-level reason empty.
func TestTheKubeletsEndIsToldFromTheProgramsFailure(t *testing.T) {
	disrupted := []endedpods.Condition{{Type: "DisruptionTarget", Status: "True"}}
	cases := []struct {
		name string
		pod  endedpods.Pod
		want bool
	}{
		{"an eviction", endedpods.Pod{Phase: "Failed", Reason: "Evicted", Conditions: disrupted}, true},
		{"a rejection for CPU", endedpods.Pod{Phase: "Failed", Reason: "OutOfcpu"}, true},
		{"a rejection for memory", endedpods.Pod{Phase: "Failed", Reason: "OutOfmemory"}, true},
		{"a rejection for node affinity", endedpods.Pod{Phase: "Failed", Reason: "NodeAffinity"}, true},
		{"a graceful node shutdown", endedpods.Pod{Phase: "Failed", Reason: "Terminated"}, true},
		{"a preemption", endedpods.Pod{Phase: "Failed", Conditions: disrupted}, true},
		{"the pod's own deadline", endedpods.Pod{Phase: "Failed", Reason: "DeadlineExceeded"}, false},
		{"a program that exits non-zero", endedpods.Pod{Phase: "Failed"}, false},
		{"a DisruptionTarget that is False", endedpods.Pod{Phase: "Failed", Conditions: []endedpods.Condition{{Type: "DisruptionTarget", Status: "False"}}}, false},
		{"a pod that succeeded", endedpods.Pod{Phase: "Succeeded", Reason: "Evicted", Conditions: disrupted}, false},
		{"a pod that runs", endedpods.Pod{Phase: "Running", Reason: "Evicted"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.pod.EndedByKubelet(); got != c.want {
				t.Errorf("EndedByKubelet of %+v = %t, want %t", c.pod, got, c.want)
			}
		})
	}
}

// The count is a number of zero or more in the annotation, and anything
// else reads as no count.
func TestTheCountOfTriesReadsWhatTheAnnotationHolds(t *testing.T) {
	const key = "example.liken.sh/tries"
	cases := []struct {
		name        string
		annotations map[string]string
		want        int
	}{
		{"no annotations", nil, 0},
		{"no count", map[string]string{"other": "4"}, 0},
		{"a count", map[string]string{key: "3"}, 3},
		{"a count that is not a number", map[string]string{key: "three"}, 0},
		{"a negative count", map[string]string{key: "-2"}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := endedpods.Tries(c.annotations, key); got != c.want {
				t.Errorf("Tries(%v) = %d, want %d", c.annotations, got, c.want)
			}
		})
	}
}

// The next pod's count reads back as the count that SetTries wrote.
func TestTheNextPodsCountReadsBack(t *testing.T) {
	const key = "example.liken.sh/tries"
	annotations := map[string]string{key: "1"}

	endedpods.SetTries(annotations, key, 2)

	if got := endedpods.Tries(annotations, key); got != 2 || annotations[key] != "2" {
		t.Errorf("after SetTries(2), the annotation is %q and Tries reads %d, want \"2\" and 2", annotations[key], got)
	}
}

// The wait before the next pod doubles from 10 seconds for each pod
// that the kubelet ended, up to 5 minutes.
func TestTheWaitBeforeTheNextPodDoubles(t *testing.T) {
	cases := []struct {
		tries int
		want  time.Duration
	}{
		{0, 10 * time.Second},
		{1, 20 * time.Second},
		{4, 160 * time.Second},
		{5, 5 * time.Minute},
		{40, 5 * time.Minute},
	}
	for _, c := range cases {
		if got := endedpods.Wait(c.tries); got != c.want {
			t.Errorf("Wait(%d) = %s, want %s", c.tries, got, c.want)
		}
	}
}

// The wait counts from the ended pod's creation.
func TestTheNextPodStartsAfterTheWaitFromTheCreation(t *testing.T) {
	created := time.Date(2026, 11, 19, 22, 0, 0, 0, time.UTC)
	p := endedpods.Pod{Phase: "Failed", Created: created, Tries: 2}

	if got, want := p.ReplaceAt(), created.Add(40*time.Second); !got.Equal(want) {
		t.Errorf("ReplaceAt = %s for 2 tries, want %s", got, want)
	}
}

// ended builds a pod with a count of tries, created at created, that
// the kubelet ended at ended. A zero ended builds a pod that the kubelet
// rejected at admission, so it has no DisruptionTarget condition and no
// container that ran.
func ended(tries int, created, ended time.Time) endedpods.Pod {
	p := endedpods.Pod{Phase: "Failed", Tries: tries, Created: created}
	if !ended.IsZero() {
		p.Conditions = []endedpods.Condition{{Type: "DisruptionTarget", Status: "True", LastTransitionTime: ended}}
	}
	return p
}

// A pod ran until its DisruptionTarget condition, or else until the last
// of its containers stopped. A pod with neither never ran.
func TestHowLongAnEndedPodRan(t *testing.T) {
	created := time.Date(2026, 11, 19, 22, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		pod  endedpods.Pod
		want time.Duration
	}{
		{"a pod rejected at admission", ended(0, created, time.Time{}), 0},
		{"an evicted pod", ended(0, created, created.Add(time.Hour)), time.Hour},
		{"a pod whose containers stopped", endedpods.Pod{Phase: "Succeeded", Created: created,
			Finished: []time.Time{created.Add(2 * time.Minute), created.Add(3 * time.Minute), {}}}, 3 * time.Minute},
		{"a DisruptionTarget with no time", endedpods.Pod{Phase: "Failed", Created: created,
			Conditions: []endedpods.Condition{{Type: "DisruptionTarget", Status: "True"}},
			Finished:   []time.Time{created.Add(time.Minute)}}, time.Minute},
		{"an end before the creation", ended(0, created, created.Add(-time.Second)), 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.pod.RanFor(); got != c.want {
				t.Errorf("RanFor = %s, want %s", got, c.want)
			}
		})
	}
}

// The replacement of an ended pod posts an Event when the pod is the
// first of its name to end, or when it ran for longer than 5 minutes
// before the kubelet ended it. The age of the pod at its replacement
// does not count, because a pod of a long loop of rejections is always
// older than 5 minutes when the operator replaces it.
func TestAReplacementPostsAnEventOnlyAfterAPodRanLong(t *testing.T) {
	now := time.Date(2026, 11, 19, 22, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		pod  endedpods.Pod
		want bool
	}{
		{"the first pod to end", ended(0, now.Add(-time.Second), time.Time{}), true},
		{"the seventh try, rejected 5 minutes and 1 second ago", ended(6, now.Add(-5*time.Minute-time.Second), time.Time{}), false},
		{"a pod that ran for 6 minutes", ended(3, now.Add(-7*time.Minute), now.Add(-time.Minute)), true},
		{"a pod that ran for a minute", ended(3, now.Add(-7*time.Minute), now.Add(-6*time.Minute)), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.pod.FirstOfLoop(); got != c.want {
				t.Errorf("FirstOfLoop = %t, want %t", got, c.want)
			}
		})
	}
}

// The kubelet's words join the reason and the message, with no white
// space and no period at the end of either.
func TestTheKubeletsWordsEndWithNoPeriod(t *testing.T) {
	cases := []struct {
		name    string
		reason  string
		message string
		want    string
	}{
		{"an eviction whose message ends in a period and a space", "Evicted",
			"The node was low on resource: memory. Threshold quantity: 100Mi, available: 50Mi. ",
			"Evicted: The node was low on resource: memory. Threshold quantity: 100Mi, available: 50Mi"},
		{"a rejection whose message has no period", "OutOfcpu",
			"Pod was rejected: Node didn't have enough resource: cpu, requested: 500, used: 3800, capacity: 4000",
			"OutOfcpu: Pod was rejected: Node didn't have enough resource: cpu, requested: 500, used: 3800, capacity: 4000"},
		{"a message that is empty", "OutOfmemory", "", "OutOfmemory"},
		{"a reason that is empty", "", "Pod was terminated in response to imminent node shutdown.",
			"Pod was terminated in response to imminent node shutdown"},
		{"neither", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := endedpods.Words(c.reason, c.message); got != c.want {
				t.Errorf("Words(%q, %q) = %q, want %q", c.reason, c.message, got, c.want)
			}
		})
	}
}
