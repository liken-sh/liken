package main

// A run's pod can be replaced while its Play continues. The operator
// replaces it after a Player edit or after mpv crashes, and something
// else can delete it, such as the taint manager on an eviction. The new
// pod has the old pod's name, and the old pod can still send messages:
// its sidecar reports an ending when it receives SIGTERM, and the broker
// publishes its Last Will when its keepalive expires, which can be long
// after the new pod starts playing. A message's topic names the run and
// not the pod. Without a check, the operator would read the old pod's
// ending as the Play's, and the Player would show Idle during the film.
//
// So each message carries the UID of the pod that sent it, and
// `reports` accepts a message only from the run's current pod. The pod
// store can lag behind the API server, so runPods also records the pods
// that are known to be gone: a pod the operator deleted to replace it,
// a pod the store showed with a deletion timestamp, and a pod that left
// the store.

// runPods records, for each run, which pod is current and which pods are
// gone. reports.mutex guards it.
//
// current maps a run to the UID of the pod that the pod store last
// showed for it, when that pod was not being deleted. gone maps a run to
// the UIDs of its pods that were deleted or replaced. lookup reads a pod
// from the pod store, and returns nil when the store does not have it.
type runPods struct {
	current map[string]string
	gone    map[string]map[string]bool
	lookup  func(namespace, name string) *Pod
}

func newRunPods() runPods {
	return runPods{current: map[string]string{}, gone: map[string]map[string]bool{}}
}

// fromRunPod reports whether a message from the pod with this UID
// belongs to the run.
//
//   - A message with no UID counts, because a sidecar that sets none
//     cannot be told from the run's pod.
//   - A pod recorded as gone does not count.
//   - When the store has a pod for the run that is not being deleted,
//     only a message from that pod counts.
//   - When the store's pod is being deleted, a message from any other
//     UID counts, because it comes from the replacement that the store
//     does not have yet.
//   - Before the pod store is read, or when it has no pod for the run,
//     a message from a pod not recorded as gone counts, because a new
//     pod can report before the store has it.
func (p *runPods) fromRunPod(namespace, name, uid string) bool {
	if uid == "" {
		return true
	}
	key := runKey(namespace, name)
	if p.gone[key][uid] {
		return false
	}
	if p.lookup == nil {
		return true
	}
	pod := p.lookup(namespace, podName(name))
	if pod == nil {
		return true
	}
	if pod.Metadata.UID == uid {
		return !pod.Metadata.deleting()
	}
	return pod.Metadata.deleting()
}

// observe records the run's pod as the store now shows it. A pod that
// is not being deleted becomes the current pod, and the pod it replaces
// is recorded as gone. A pod that is being deleted is recorded as gone.
// When the store holds no pod, the previously current pod is recorded as
// gone.
func (p *runPods) observe(key string, pod *Pod) {
	if pod != nil && !pod.Metadata.deleting() {
		if held := p.current[key]; held != "" && held != pod.Metadata.UID {
			p.markGone(key, held)
		}
		p.current[key] = pod.Metadata.UID
		return
	}
	if held := p.current[key]; held != "" {
		p.markGone(key, held)
		delete(p.current, key)
	}
	if pod != nil {
		p.markGone(key, pod.Metadata.UID)
	}
}

// unmarkGone removes a pod from the gone record, after its delete failed.
func (p *runPods) unmarkGone(key, uid string) {
	delete(p.gone[key], uid)
}

// markGone records a pod as gone.
func (p *runPods) markGone(key, uid string) {
	if uid == "" {
		return
	}
	if p.gone[key] == nil {
		p.gone[key] = map[string]bool{}
	}
	p.gone[key][uid] = true
}

// retain drops the records of runs that are not in live.
func (p *runPods) retain(live map[string]bool) {
	for key := range p.current {
		if !live[key] {
			delete(p.current, key)
		}
	}
	for key := range p.gone {
		if !live[key] {
			delete(p.gone, key)
		}
	}
}

// forget drops the records of one run.
func (p *runPods) forget(key string) {
	delete(p.current, key)
	delete(p.gone, key)
}
