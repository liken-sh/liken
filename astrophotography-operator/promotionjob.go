package main

// The promotion Job of plan 03: one run of the binary's promote mode,
// in the target's namespace, that mounts the outbox claim and the
// archive claim and moves the target's frames from one to the other.
// One Job serves one target and one outbox claim.
//
// The Job names no node. The scheduler places its pod where both
// claims can be mounted, so a local-path outbox puts it on the
// machine that holds the outbox, and the archive must be mountable
// there too: a ReadWriteMany share, or a local-path claim on the same
// node. When no node can mount both, the pod stays Pending, and the
// loop reports the scheduler's message as Unschedulable.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/outbox"
	"github.com/liken-sh/liken/astrophotography-operator/promote"
)

// The labels and annotations of a promotion Job and its pod. A label
// value holds at most 63 characters and a Target's name can hold 253,
// so the Job's target and claim are annotations, and the Target is the
// Job's owner.
const (
	labelComponent      = "app.kubernetes.io/component"
	componentPromotion  = "promotion"
	labelPromotionJob   = astrophotography.Group + "/promotion-job"
	annotationTarget    = astrophotography.Group + "/target"
	annotationClaim     = astrophotography.Group + "/claim"
	annotationNumber    = astrophotography.Group + "/number"
	annotationPending   = astrophotography.Group + "/pending"
	promoteContainer    = "promote"
	promotionUser       = 1000
	maxJobName          = 63
	promotionDeadline   = int64(time.Hour / time.Second)
	promotionTTL        = int32(time.Hour / time.Second)
	promotionBackoff    = 2
	outboxVolume        = "outbox"
	archiveVolume       = "archive"
	promotionMemory     = "64Mi"
	promotionMemoryCap  = "128Mi"
	promotionCPURequest = "10m"
)

// promotionJobName answers the name of a target's Job of a number, such
// as m42-promote-3. A long target name is cut and takes a hash of the
// whole name, so the Job's name stays a DNS label that its pod's
// job-name label can hold.
func promotionJobName(target string, number int32) string {
	tail := "-promote-" + strconv.Itoa(int(number))
	if len(target)+len(tail) <= maxJobName {
		return target + tail
	}
	sum := sha256.Sum256([]byte(target))
	hash := "-" + hex.EncodeToString(sum[:])[:8]
	return target[:maxJobName-len(tail)-len(hash)] + hash + tail
}

// promotionJob answers the Job that promotes a target's frames from one
// outbox claim. pending is the claim's count of the target's frames
// when the Job is created. The report's frames left, with the frames
// counted since then, become the claim's count when the Job ends
// (promotionstatus.go).
func promotionJob(t *astrophotography.Target, claim string, number, pending int32, image string, cameras map[string]promote.Camera) jobObject {
	name := promotionJobName(t.Metadata.Name, number)
	same := claim == t.Spec.Archive.ClaimName
	encoded, _ := json.Marshal(cameras)
	args := []string{"promote", "--target", t.Metadata.Name, "--path", t.Spec.Archive.Path, "--cameras", string(encoded)}
	mounts := []volumeMount{{Name: outboxVolume, MountPath: outbox.Root}}
	volumes := []volume{{Name: outboxVolume, PersistentVolumeClaim: &claimName{claim}}}
	if same {
		args = append(args, "--same-claim", "--archive", outbox.Root)
	} else {
		mounts = append(mounts, volumeMount{Name: archiveVolume, MountPath: promote.ArchiveRoot})
		volumes = append(volumes, volume{Name: archiveVolume, PersistentVolumeClaim: &claimName{t.Spec.Archive.ClaimName}})
	}
	owner := ownerReference{APIVersion: astrophotography.APIVersion, Kind: astrophotography.TargetKind.Name,
		Name: t.Metadata.Name, UID: t.Metadata.UID, Controller: true}
	meta := objectMeta{OwnerReferences: []ownerReference{owner}}
	meta.Name, meta.Namespace = name, t.Metadata.Namespace
	meta.Labels = map[string]string{labelManagedBy: managedBy, labelComponent: componentPromotion}
	meta.Annotations = map[string]string{
		annotationTarget: t.Metadata.Name, annotationClaim: claim,
		annotationNumber: strconv.Itoa(int(number)), annotationPending: strconv.Itoa(int(pending)),
	}
	var podMeta objectMeta
	// The pod carries the operator's label, so the pod watch holds it
	// and the loop reads the report from its status.
	podMeta.Labels = map[string]string{labelManagedBy: managedBy, labelComponent: componentPromotion, labelPromotionJob: name}
	return jobObject{
		APIVersion: "batch/v1", Kind: "Job", Metadata: meta,
		Spec: jobSpec{
			BackoffLimit: promotionBackoff, ActiveDeadlineSeconds: promotionDeadline, TTLSecondsAfterFinished: promotionTTL,
			Template: podTemplate{Metadata: podMeta, Spec: podSpec{
				RestartPolicy: "Never",
				// The driver writes the frames as user and group 1000, so
				// the Job does too. fsGroup lets it write a new archive
				// claim, and OnRootMismatch changes the owner only when the
				// claim's root is not the group's, so a large archive is
				// not walked on every run.
				SecurityContext: &podSecurity{
					RunAsUser: promotionUser, RunAsGroup: promotionUser, RunAsNonRoot: true,
					FSGroup: promotionUser, FSGroupChangePolicy: "OnRootMismatch",
					SeccompProfile: &seccompProfile{Type: "RuntimeDefault"},
				},
				Containers: []container{{
					Name: promoteContainer, Image: image, Args: args, VolumeMounts: mounts,
					SecurityContext: &containerSecurity{ReadOnlyRootFilesystem: true, Capabilities: &capabilities{Drop: []string{"ALL"}}},
					Resources: &resources{
						Requests: map[string]string{"cpu": promotionCPURequest, "memory": promotionMemory},
						Limits:   map[string]string{"memory": promotionMemoryCap},
					},
				}},
				Volumes: volumes,
			}},
		},
	}
}

// promotionNumber answers the number of a promotion Job, and false for a
// Job that is not one.
func promotionNumber(j jobObject) (int32, bool) {
	if j.Metadata.Labels[labelComponent] != componentPromotion {
		return 0, false
	}
	number, err := strconv.Atoi(j.Metadata.Annotations[annotationNumber])
	return int32(number), err == nil
}

// finished reports whether a Job ended, and whether it succeeded.
func finished(j jobObject) (ended, succeeded bool) {
	if _, ok := condition(j.Status.Conditions, "Complete", "True"); ok {
		return true, true
	}
	_, failed := condition(j.Status.Conditions, "Failed", "True")
	return failed, false
}
