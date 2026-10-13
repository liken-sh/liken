package main

// The counts of a report, by sequence, and the names of the Jobs.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/catalog"
	"github.com/liken-sh/liken/astrophotography-operator/promote"
)

func int32p(v int32) *int32 { return &v }

func float(v float64) *float64 { return &v }

// A frame counts toward a sequence with the same filter, exposure,
// binning, gain, and offset. A field the sequence leaves out matches
// any value, because the camera's own value took its place.
func TestAFrameCountsTowardASequenceByItsSettings(t *testing.T) {
	frame := catalog.Settings{Filter: "Red", ExposureSeconds: 0.5, Binning: 2, Gain: float(100), Offset: float(10)}
	cases := []struct {
		name     string
		sequence astrophotography.Sequence
		want     bool
	}{
		{"every field the same",
			astrophotography.Sequence{Filter: "Red", Exposure: "500ms", Binning: int32p(2), Gain: int32p(100), Offset: int32p(10)}, true},
		{"only the exposure stated", astrophotography.Sequence{Exposure: "0.5s"}, true},
		{"another filter", astrophotography.Sequence{Filter: "Green", Exposure: "500ms"}, false},
		{"another exposure", astrophotography.Sequence{Filter: "Red", Exposure: "1s"}, false},
		{"another binning", astrophotography.Sequence{Exposure: "500ms", Binning: int32p(1)}, false},
		{"another gain", astrophotography.Sequence{Exposure: "500ms", Gain: int32p(200)}, false},
		{"an exposure that does not parse", astrophotography.Sequence{Exposure: "soon"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := countsToward(c.sequence, frame); got != c.want {
				t.Errorf("countsToward = %v, want %v", got, c.want)
			}
		})
	}
	noGain := catalog.Settings{ExposureSeconds: 0.5, Binning: 1}
	if countsToward(astrophotography.Sequence{Exposure: "500ms", Gain: int32p(100)}, noGain) {
		t.Error("a frame with no GAIN counts toward a sequence that states one")
	}
}

func TestSequenceProgress(t *testing.T) {
	l := astrophotography.Sequence{Filter: "L", Exposure: "300s", Count: 10}
	r := astrophotography.Sequence{Filter: "R", Exposure: "300s", Count: 5}
	total := func(filter string, frames int32) catalog.Total {
		return catalog.Total{Settings: catalog.Settings{Filter: filter, ExposureSeconds: 300, Binning: 1}, Frames: frames}
	}
	cases := []struct {
		name      string
		spec      []astrophotography.Sequence
		held      []astrophotography.SequenceProgress
		totals    []catalog.Total
		truncated bool
		want      []astrophotography.SequenceProgress
	}{
		{"pending drops by the frames archived since the last report",
			[]astrophotography.Sequence{l}, []astrophotography.SequenceProgress{{Sequence: l, Archived: 2, Pending: 3}},
			[]catalog.Total{total("L", 4)}, false,
			[]astrophotography.SequenceProgress{{Sequence: l, Archived: 4, Pending: 1}}},
		{"pending never goes below zero",
			[]astrophotography.Sequence{l}, nil, []catalog.Total{total("L", 4)}, false,
			[]astrophotography.SequenceProgress{{Sequence: l, Archived: 4}}},
		{"a new count keeps the counts of its settings",
			[]astrophotography.Sequence{{Filter: "L", Exposure: "300s", Count: 20}},
			[]astrophotography.SequenceProgress{{Sequence: l, Archived: 4, Pending: 1}}, []catalog.Total{total("L", 4)}, false,
			[]astrophotography.SequenceProgress{{Sequence: astrophotography.Sequence{Filter: "L", Exposure: "300s", Count: 20}, Archived: 4, Pending: 1}}},
		{"a frame that matches no sequence counts nowhere",
			[]astrophotography.Sequence{l}, nil, []catalog.Total{total("H", 9)}, false,
			[]astrophotography.SequenceProgress{{Sequence: l}}},
		{"a truncated report keeps the count of a sequence it does not hold",
			[]astrophotography.Sequence{l, r}, []astrophotography.SequenceProgress{{Sequence: l, Archived: 1}, {Sequence: r, Archived: 3}},
			[]catalog.Total{total("L", 2)}, true,
			[]astrophotography.SequenceProgress{{Sequence: l, Archived: 2}, {Sequence: r, Archived: 3}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sequenceProgress(c.spec, c.held, c.totals, c.truncated); !reflect.DeepEqual(got, c.want) {
				t.Errorf("sequenceProgress = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestAJobNameIsADNSLabel(t *testing.T) {
	cases := []struct{ target, want string }{
		{"m42", "m42-promote-3"},
		{strings.Repeat("a", 60), strings.Repeat("a", 44) + "-11ee3912-promote-3"},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			if got := promotionJobName(c.target, 3); got != c.want || len(got) > maxJobName {
				t.Errorf("promotionJobName = %q, want %q", got, c.want)
			}
		})
	}
}

// The report names its failures with the reasons of ArchiveAvailable,
// which the loop copies into the condition.
func TestTheReportsReasonsAreArchiveAvailablesReasons(t *testing.T) {
	got := []string{promote.ReasonCopyFailed, promote.ReasonVerifyFailed, promote.ReasonNameConflict}
	want := []string{astrophotography.ReasonCopyFailed, astrophotography.ReasonVerifyFailed, astrophotography.ReasonNameConflict}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the report's reasons are %v, want %v", got, want)
	}
}

// A report of a Job that succeeded but does not decode is a failure,
// not a wait.
func TestAReportThatDoesNotReadIsAFailure(t *testing.T) {
	job := jobObject{}
	job.Metadata.Name = "m42-promote-1"
	pod := podObject{}
	pod.Metadata.Labels = map[string]string{labelPromotionJob: "m42-promote-1"}
	pod.Status.ContainerStatuses = []containerStatus{{Name: promoteContainer, State: containerState{Terminated: &terminated{Message: "{"}}}}

	result, ready := resultOf(job, 1, true, []podObject{pod})

	if !ready || result.report != nil || !strings.HasPrefix(result.failure, "the report of the promotion Job m42-promote-1 does not read") {
		t.Errorf("resultOf = %+v, %v", result, ready)
	}
}

// A Job's pod that failed an attempt holds no report. The report is
// in the pod of the attempt that succeeded.
func TestTheReportIsInThePodThatSucceeded(t *testing.T) {
	job := jobObject{}
	job.Metadata.Name = "m42-promote-1"
	attempt := func(exitCode int32, message string) podObject {
		pod := podObject{}
		pod.Metadata.Labels = map[string]string{labelPromotionJob: "m42-promote-1"}
		pod.Status.ContainerStatuses = []containerStatus{{Name: promoteContainer,
			State: containerState{Terminated: &terminated{ExitCode: exitCode, Message: message}}}}
		return pod
	}
	other := attempt(0, `{"promoted":9}`)
	other.Metadata.Labels[labelPromotionJob] = "m43-promote-1"

	result, ready := resultOf(job, 1, true, []podObject{other, attempt(1, ""), attempt(0, `{"promoted":2}`)})

	if !ready || result.report == nil || result.report.Promoted != 2 {
		t.Errorf("resultOf = %+v, %v", result, ready)
	}
}

func TestPromotionNumber(t *testing.T) {
	job := func(component, number string) jobObject {
		j := jobObject{}
		j.Metadata.Labels = map[string]string{labelComponent: component}
		j.Metadata.Annotations = map[string]string{annotationNumber: number}
		return j
	}
	cases := []struct {
		name string
		job  jobObject
		want int32
		ok   bool
	}{
		{"a promotion Job", job(componentPromotion, "3"), 3, true},
		{"another Job of the operator", job("worker", "3"), 0, false},
		{"a number that does not parse", job(componentPromotion, "three"), 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, ok := promotionNumber(c.job); got != c.want || ok != c.ok {
				t.Errorf("promotionNumber = %d, %v", got, ok)
			}
		})
	}
}

// A report is applied once. Its Job's number goes into the status in
// the same write, so a second pass over the same Job changes nothing.
func TestAReportIsAppliedOnce(t *testing.T) {
	target := withPending(m42(), 2, 0)
	result := promotionResult{job: "m42-promote-1", number: 1, claim: "frames", pending: 2,
		report: &promote.Report{Totals: luminance(2), Promoted: 2}}

	_, first := result.apply(&target)
	after := target.Status.Sequences[0]
	_, second := result.apply(&target)

	if !first || second || target.Status.Sequences[0] != after || after.Archived != 2 || after.Pending != 0 {
		t.Errorf("the applies answered %v and %v, and the sequence is %+v", first, second, target.Status.Sequences[0])
	}
}

// A Job that succeeded is not applied until its pod's report reaches the
// store, because the two watches deliver their changes apart.
func TestASucceededJobWaitsForItsPod(t *testing.T) {
	job := jobObject{}
	job.Metadata.Name = "m42-promote-1"
	if _, ready := resultOf(job, 1, true, nil); ready {
		t.Error("the result is ready with no pod")
	}
}

// ArchiveAvailable's message counts frames in the singular for one.
func TestTheArchiveMessageCountsFrames(t *testing.T) {
	failed := []promote.Frame{{File: "m42/a.fits", Reason: astrophotography.ReasonCopyFailed, Message: "disk full"}}
	cases := []struct {
		name   string
		report promote.Report
		want   string
	}{
		{"one moved", promote.Report{Promoted: 1}, "the promotion Job m42-promote-1 moved 1 frame to the archive"},
		{"two moved", promote.Report{Promoted: 2}, "the promotion Job m42-promote-1 moved 2 frames to the archive"},
		{"one left", promote.Report{Left: 1, Failures: failed},
			"the promotion Job m42-promote-1 left 1 frame in the outbox; the first failure is m42/a.fits: disk full"},
		{"two left", promote.Report{Left: 2, Failures: failed},
			"the promotion Job m42-promote-1 left 2 frames in the outbox; the first failure is m42/a.fits: disk full"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result := promotionResult{job: "m42-promote-1", report: &c.report}
			if got := result.availability().Message; got != c.want {
				t.Errorf("message %q, want %q", got, c.want)
			}
		})
	}
}
