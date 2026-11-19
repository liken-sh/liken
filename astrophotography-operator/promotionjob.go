package main

// A promotion Job runs the binary's promote mode in the target's
// namespace. It mounts one outbox claim and the target's archive claim,
// and moves the target's frames from the outbox to the archive.
//
// The Job sets no node, so the scheduler places its pod on a node that
// can mount both claims. A local-path outbox exists on one machine
// only, so the archive must be mountable there too: a ReadWriteMany
// volume, or a local-path claim on the same node. When no node can
// mount both, the pod stays Pending, and the operator sets
// ArchiveAvailable to False with the reason Unschedulable and the
// scheduler's message.

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

// A label value is limited to 63 characters and a Target's name can
// have 253, so a promotion Job records its target and claim in
// annotations, not labels.
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

// promotionJobName returns a name such as m42-promote-3. The Job
// controller copies the Job's name into the job-name label of its pod,
// so the name must fit in 63 characters. A longer target name is cut
// short, and a hash of the full name keeps two long names apart.
func promotionJobName(target string, number int32) string {
	tail := "-promote-" + strconv.Itoa(int(number))
	if len(target)+len(tail) <= maxJobName {
		return target + tail
	}
	sum := sha256.Sum256([]byte(target))
	hash := "-" + hex.EncodeToString(sum[:])[:8]
	return target[:maxJobName-len(tail)-len(hash)] + hash + tail
}

// promotionJob builds the Job that moves a target's frames out of one
// outbox claim. pending is the number of the target's frames in the
// claim when the Job is created. When the Job ends, the operator sets
// the claim's pending count to the frames that the report says are
// left, plus the frames exposed since the Job was created.
func promotionJob(t *astrophotography.Target, claim string, number, pending int32, image string, cameras map[string]promote.Camera) jobObject {
	name := promotionJobName(t.Metadata.Name, number)
	same := claim == t.Spec.Archive.ClaimName
	encoded, _ := json.Marshal(cameras)
	args := []string{"promote", "--target", t.Metadata.Name, "--path", t.Spec.Archive.Path, "--cameras", string(encoded)}
	mounts := []volumeMount{{Name: outboxVolume, MountPath: outbox.Root}}
	volumes := []volume{{Name: outboxVolume, PersistentVolumeClaim: &claimName{ClaimName: claim}}}
	if same {
		args = append(args, "--same-claim", "--archive", outbox.Root)
	} else {
		mounts = append(mounts, volumeMount{Name: archiveVolume, MountPath: promote.ArchiveRoot})
		volumes = append(volumes, volume{Name: archiveVolume, PersistentVolumeClaim: &claimName{ClaimName: t.Spec.Archive.ClaimName}})
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
	// The operator's pod watch selects on labelManagedBy, and the
	// operator reads the Job's report from the pod's status.
	podMeta.Labels = map[string]string{labelManagedBy: managedBy, labelComponent: componentPromotion, labelPromotionJob: name}
	return jobObject{
		APIVersion: "batch/v1", Kind: "Job", Metadata: meta,
		Spec: jobSpec{
			BackoffLimit: promotionBackoff, ActiveDeadlineSeconds: promotionDeadline, TTLSecondsAfterFinished: promotionTTL,
			// A pod that the kubelet ends, such as for an eviction, a
			// preemption, or a node's shutdown, has the condition
			// DisruptionTarget. Its promote run did not fail, so the Job
			// controller runs a new pod without counting it against the
			// backoff limit. The API server accepts a podFailurePolicy
			// only with the restartPolicy Never below.
			PodFailurePolicy: &podFailurePolicy{Rules: []podFailurePolicyRule{{
				Action:          "Ignore",
				OnPodConditions: []podConditionMatch{{Type: "DisruptionTarget", Status: "True"}},
			}}},
			Template: podTemplate{Metadata: podMeta, Spec: podSpec{
				RestartPolicy: "Never",
				// The INDI driver pods of observatory-operator run as user
				// and group 1000 and write the frames, so the Job runs as
				// 1000 too. fsGroup makes a new archive claim writable.
				// OnRootMismatch changes ownership only when the claim's
				// root directory does not already belong to the group, so
				// the kubelet does not walk a large archive on every run.
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

// promotionNumber returns the number of a promotion Job, and false when
// the Job is not a promotion Job.
func promotionNumber(j jobObject) (int32, bool) {
	if j.Metadata.Labels[labelComponent] != componentPromotion {
		return 0, false
	}
	number, err := strconv.ParseInt(j.Metadata.Annotations[annotationNumber], 10, 32)
	if err != nil {
		return 0, false
	}
	return int32(number), true
}

// finished reports whether a Job has ended, and whether it succeeded.
func finished(j jobObject) (ended, succeeded bool) {
	if _, ok := condition(j.Status.Conditions, "Complete", "True"); ok {
		return true, true
	}
	_, failed := condition(j.Status.Conditions, "Failed", "True")
	return failed, false
}
