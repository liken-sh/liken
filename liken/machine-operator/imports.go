package main

// Before k3s imports new image tarballs, init writes a staged
// imported-images record. When init finds a staged record left by a
// boot that did not promote it, init discards the container store and
// imports again (init's imports.go). The operator promotes the staged
// record to proven when the imports are intact, and only containers
// that run from the imported images can show that.
//
// The proof has two parts. First, every container on this node whose
// image starts with liken.sh/ must be Ready. The kubelet reports a
// container whose image was half unpacked as not Ready, as it does for
// a crash loop, so one torn image blocks the promotion. Second, the
// operator calls syncfs on the container store's filesystem, because
// the containers on this node test only the images that run here. The
// image of a tarball that does not run on this node, such as the
// cluster operator's on most machines, can still be torn until its
// dirty pages reach the disk.
//
// Until the promotion, the ImportsConverged condition is False or
// Unknown and gives the reason.

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
)

// osImagePrefix is the name prefix of the images that liken builds and
// init imports from tarballs. The proof counts every container whose
// image has this prefix as one that runs from the imports.
const osImagePrefix = "liken.sh/"

// importsInputs is what settleImportsLifecycle read, so that
// decideImportsPromotion needs no I/O.
type importsInputs struct {
	stagedHash string // identity of the staged record, "" when none
	provenHash string // identity of the proven record, "" when none
	storeErr   error
	pods       []kubernetes.Pod
	podsErr    error
}

// importsVerdict is whether to promote the record now, and the
// ImportsConverged condition to publish either way.
type importsVerdict struct {
	promote   bool
	condition api.Condition
}

// decideImportsPromotion decides, from what one pass read, whether the
// staged imports are proven. The checks run in the order of the other
// convergence decisions: no facts, nothing tracked, already proven,
// and then the proof.
func decideImportsPromotion(in importsInputs, facts *machine.MachineStatus) importsVerdict {
	condType := "ImportsConverged"
	if facts == nil {
		return importsVerdict{condition: convergenceUnknown(condType, "FactsIncomplete",
			"No facts are published yet. The imports entry of the boot record names the record to prove")}
	}
	switch facts.Boot.ImportsSource {
	case "":
		// Init did not track the imports. With an ephemeral
		// machineState, the record has nowhere to persist, and the
		// container store is empty at every boot, so it cannot hold a
		// torn image. An OS image built before the record existed also
		// reports no source.
		return importsVerdict{condition: converged(condType, "NotTracked",
			"Init tracked no imports at this boot, so there is no record to prove")}
	case machine.ManifestSourceProven:
		return importsVerdict{condition: converged(condType, "Converged",
			fmt.Sprintf("This boot runs the proven imports (%.12s)", facts.Boot.ImportsHash))}
	}

	if in.storeErr != nil {
		return importsVerdict{condition: convergenceUnknown(condType, "MachineStateUnavailable",
			fmt.Sprintf("The imports store cannot be read: %v", in.storeErr))}
	}
	if in.stagedHash == "" {
		if in.provenHash == facts.Boot.ImportsHash {
			return importsVerdict{condition: converged(condType, "Converged",
				fmt.Sprintf("The imports of this boot were proven (%.12s)", facts.Boot.ImportsHash))}
		}
		return importsVerdict{condition: convergenceUnknown(condType, "MachineStateUnavailable",
			"The boot record names a staged imports record that the store no longer has. The next boot stages it again")}
	}
	if in.stagedHash != facts.Boot.ImportsHash {
		return importsVerdict{condition: convergenceUnknown(condType, "FactsIncomplete",
			"The staged record is not the one that this boot ran, so the operator waits for new facts")}
	}

	if in.podsErr != nil {
		return importsVerdict{condition: convergenceUnknown(condType, "ClusterUnavailable",
			fmt.Sprintf("The pods of this Node cannot be listed: %v", in.podsErr))}
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
			"No OS container is observed on this Node yet, so the imports of this boot are not proven yet")}
	}
	if len(waiting) > 0 {
		return importsVerdict{condition: notConverged(condType, "Proving",
			fmt.Sprintf("Waiting for the OS containers that run from the imports of this boot to be Ready: %s", strings.Join(waiting, ", ")))}
	}
	return importsVerdict{promote: true, condition: converged(condType, "Converged",
		fmt.Sprintf("%d OS containers that run from the imports of this boot (%.12s) are Ready, so the imports are proven", observed, facts.Boot.ImportsHash))}
}

// settleImportsLifecycle reads the store and the node's pods, and
// promotes the record when decideImportsPromotion says to. It syncs
// the container store before it promotes the record, because in the
// other order a power cut could leave a proven record over unpacked
// files that never reached the disk.
func settleImportsLifecycle(r *reader, root, nodeName string, facts *machine.MachineStatus, out *passOutcome) api.Condition {
	store := machine.ImportedImagesStore(root)
	in := importsInputs{}
	if facts != nil && facts.Boot.ImportsSource == machine.ManifestSourceStaged {
		staged, err := store.LoadStaged()
		in.storeErr = err
		switch {
		case staged != nil:
			in.stagedHash = machine.ManifestHash(staged)
			if in.storeErr == nil && in.stagedHash == facts.Boot.ImportsHash {
				in.pods, in.podsErr = r.nodePods(nodeName)
			}
		case in.storeErr == nil:
			// The facts do not change after boot, but the store does.
			// Nothing staged now usually means an earlier pass promoted
			// the record, and the proven record's hash confirms it.
			proven, err := store.LoadProven()
			in.storeErr = err
			if proven != nil {
				in.provenHash = machine.ManifestHash(proven)
			}
		}
	}
	out.fail("reading the imports record", in.storeErr)
	v := decideImportsPromotion(in, facts)
	if !v.promote {
		return v.condition
	}
	if err := syncContainerStore(); err != nil {
		out.fail("syncing the container store", err)
		return convergenceUnknown(v.condition.Type, "PromotionFailed",
			fmt.Sprintf("The container store was not synced before promotion: %v", err))
	}
	if err := store.Promote(); err != nil {
		out.fail("promoting the imports record", err)
		return convergenceUnknown(v.condition.Type, "PromotionFailed",
			fmt.Sprintf("The imports record was not promoted: %v", err))
	}
	out.wrote("promoting the imports record")
	fmt.Printf("promoted the imports record of this boot (%.12s), so the next boot keeps the container store\n", facts.Boot.ImportsHash)
	return v.condition
}

// containerStoreDir is a variable so that a test can point it at a
// temporary directory instead of syncing a host filesystem.
var containerStoreDir = machine.K3sAgentDir

// syncContainerStore writes every pending write on the container
// store's filesystem to disk. The pod mounts the store read-only as a
// hostPath, which is enough, because syncfs needs only an open file on
// the filesystem.
func syncContainerStore() error {
	f, err := os.Open(containerStoreDir)
	if err != nil {
		return err
	}
	defer f.Close()
	return unix.Syncfs(int(f.Fd()))
}
