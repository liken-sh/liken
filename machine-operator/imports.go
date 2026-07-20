package main

// The operator's half of crash-safe image imports: proving a trial.
//
// Init stages an imported-images record before k3s first sees new
// tarballs, and discards the container store when it finds a record
// still staged from a boot that died (init's imports.go describes
// that half). This file provides the proof that finishes the work.
// The record cannot prove itself. Only something that watches
// containers actually run from the imported images can confirm the
// unpacks worked, and this operator does exactly that: its own pod
// runs from the tarball most worth proving.
//
// The proof rests on two observations and one barrier. First, every
// OS container on this node, meaning every container running a
// liken.sh/ image, must be Ready. This is the kubelet's own verdict,
// and it fails for a torn image the same way it fails for a crash
// loop, so a half-unpacked logs relay holds back the whole
// promotion. Second, the operator runs syncfs on the container
// store's filesystem. The OS pods only prove the images that run on
// this node, and a tarball whose image never schedules here (this
// includes the cluster operator, on most machines) could still carry
// a latent tear, until its dirty pages are written to disk. At that
// point no tear is possible at all. Only then does the record
// promote. A promotion that never happens is itself the signal: the
// condition stays False, the phase shows it, and the next reboot
// discards the store and tries again.

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/machine"
)

// osImagePrefix marks the container images that arrive by tarball.
// Everything liken builds is named under the project's domain, and
// nothing else is.
const osImagePrefix = "liken.sh/"

// importsInputs holds everything settleImportsLifecycle observed, so
// the decision itself stays a pure function.
type importsInputs struct {
	stagedHash string // identity of the staged record, "" when none
	provenHash string // identity of the proven record, "" when none
	storeErr   error  // reading the store failed
	pods       []kubernetes.Pod
	podsErr    error
}

// importsVerdict is the decision: whether to promote now, and the
// ImportsConverged condition to publish either way.
type importsVerdict struct {
	promote   bool
	condition api.Condition
}

// decideImportsPromotion judges one pass of the imports lifecycle,
// based on what was observed. The order of checks mirrors the other
// convergence decisions: no facts, nothing tracked, already settled,
// and then the proof itself.
func decideImportsPromotion(in importsInputs, facts *machine.MachineStatus) importsVerdict {
	condType := "ImportsConverged"
	if facts == nil {
		return importsVerdict{condition: convergenceUnknown(condType, "FactsIncomplete",
			"no facts published yet; the boot record's imports entry decides what to prove")}
	}
	switch facts.Boot.ImportsSource {
	case "":
		// Init did not run the lifecycle. An ephemeral machineState
		// has nowhere to remember a trial. An ephemeral container
		// store resets with every boot and cannot get stuck. An
		// image from before the record existed reports the same way.
		return importsVerdict{condition: converged(condType, "NotTracked",
			"this boot tracks no imports; ephemeral state cannot wedge and needs no proof")}
	case machine.ManifestSourceProven:
		return importsVerdict{condition: converged(condType, "Converged",
			fmt.Sprintf("the container store serves the proven imports (%.12s)", facts.Boot.ImportsHash))}
	}

	// A trial is in progress. The store is read fresh on each pass,
	// because the facts cannot change after boot, but the store can.
	// This operator's own earlier pass may have already promoted it.
	if in.storeErr != nil {
		return importsVerdict{condition: convergenceUnknown(condType, "MachineStateUnavailable",
			fmt.Sprintf("reading the imports store: %v", in.storeErr))}
	}
	if in.stagedHash == "" {
		if in.provenHash == facts.Boot.ImportsHash {
			return importsVerdict{condition: converged(condType, "Converged",
				fmt.Sprintf("this boot's imports were proven (%.12s)", facts.Boot.ImportsHash))}
		}
		return importsVerdict{condition: convergenceUnknown(condType, "MachineStateUnavailable",
			"the boot record names a trial the store no longer holds; the next boot re-stages it")}
	}
	if in.stagedHash != facts.Boot.ImportsHash {
		return importsVerdict{condition: convergenceUnknown(condType, "FactsIncomplete",
			"the staged record is not the one this boot ran; waiting for fresh facts")}
	}

	// The proof: every OS container on this node must be serving.
	// The kubelet's Ready condition covers every way a container can
	// fail, including the one this lifecycle exists for: a torn
	// image whose binary will not run.
	if in.podsErr != nil {
		return importsVerdict{condition: convergenceUnknown(condType, "ClusterUnavailable",
			fmt.Sprintf("listing this node's pods: %v", in.podsErr))}
	}
	observed := 0
	var waiting []string
	for _, pod := range in.pods {
		if pod.Completed() {
			continue
		}
		for _, container := range pod.Status.ContainerStatuses {
			if !strings.HasPrefix(container.Image, osImagePrefix) {
				continue
			}
			observed++
			if !container.Ready {
				waiting = append(waiting, fmt.Sprintf("%s/%s", pod.Metadata.Name, container.Name))
			}
		}
	}
	if observed == 0 {
		return importsVerdict{condition: notConverged(condType, "Proving",
			"no OS containers observed on this node yet; the trial is still proving")}
	}
	if len(waiting) > 0 {
		return importsVerdict{condition: notConverged(condType, "Proving",
			fmt.Sprintf("waiting for OS containers to serve the trialed imports: %s", strings.Join(waiting, ", ")))}
	}
	return importsVerdict{promote: true, condition: converged(condType, "Converged",
		fmt.Sprintf("%d OS containers serve the trialed imports (%.12s); proven", observed, facts.Boot.ImportsHash))}
}

// settleImportsLifecycle observes, decides, and promotes when the
// proof succeeds. The syncfs barrier runs before the promotion
// write. If the promotion write ran first, a badly-timed power cut
// could prove a store whose latent unpacks are still dirty, which is
// the exact false claim this lifecycle exists to prevent.
func settleImportsLifecycle(c *kubernetes.Client, root, nodeName string, facts *machine.MachineStatus) api.Condition {
	store := machine.ImportedImagesStore(root)
	in := importsInputs{}
	if facts != nil && facts.Boot.ImportsSource == machine.ManifestSourceStaged {
		staged, err := store.LoadStaged()
		in.storeErr = err
		switch {
		case staged != nil:
			in.stagedHash = machine.ManifestHash(staged)
			if in.storeErr == nil && in.stagedHash == facts.Boot.ImportsHash {
				in.pods, in.podsErr = kubernetes.ListPodsOnNode(c, nodeName)
			}
		case in.storeErr == nil:
			// Nothing staged under a Staged boot usually means an
			// earlier pass already promoted it. The proven record's
			// identity confirms this.
			proven, err := store.LoadProven()
			in.storeErr = err
			if proven != nil {
				in.provenHash = machine.ManifestHash(proven)
			}
		}
	}
	v := decideImportsPromotion(in, facts)
	if !v.promote {
		return v.condition
	}
	if err := syncContainerStore(); err != nil {
		return convergenceUnknown(v.condition.Type, "PromotionFailed",
			fmt.Sprintf("syncing the container store before promotion: %v", err))
	}
	if err := store.Promote(); err != nil {
		return convergenceUnknown(v.condition.Type, "PromotionFailed",
			fmt.Sprintf("promoting the imports record: %v", err))
	}
	fmt.Printf("proved this boot's imports (%.12s); the container store is trusted\n", facts.Boot.ImportsHash)
	return v.condition
}

// syncContainerStore flushes everything on the container store's
// filesystem to disk. The store is reachable inside this pod as a
// read-only hostPath of the same tree init discards
// (machine.K3sAgentDir names it for both halves). Here, its only use
// is as a handle for the syncfs call. One syscall turns the fact
// that the OS pods we can see are serving into the fact that every
// byte the imports wrote is durable. After it returns, no image on
// this store, including images whose pods never schedule here, can
// be torn by a crash.
func syncContainerStore() error {
	f, err := os.Open(machine.K3sAgentDir)
	if err != nil {
		return err
	}
	defer f.Close()
	return unix.Syncfs(int(f.Fd()))
}
