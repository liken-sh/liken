package main

// A run's pod can stop while its Play goes on. A Player edit makes the
// operator recreate it, mpv crashes and the operator resumes the run, or
// something else deletes the pod, such as the taint manager on an
// eviction. The new pod takes the old pod's name, and the old pod still
// sends messages: its sidecar reports the ending on its SIGTERM, and the
// broker publishes its Last Will when its keepalive runs out, which can
// be long after the new pod plays. Each of those messages names the run
// by its topic alone, so without the pod's UID the operator reads the
// old pod's ending as the Play's, and the unit reads Idle while the Play
// goes on.
//
// So each message carries the UID of the pod that sent it, and the desk
// takes a message only from the run's pod. The pod store is what names
// that pod. The store can be a moment behind the API server, so the desk
// also remembers the pods it knows are gone: the pod the operator
// deletes to replace it, a pod the store showed with a deletion mark,
// and a pod the store held before that the store no longer holds.

// runPods holds, per run, what tells the run's pod from an older one. The
// report desk's mutex covers it.
//
// current is the UID of the run's pod as the pass last read it from the
// store, standing and not deleting. gone holds the UIDs of the run's pods
// known gone or replaced. lookup reads one pod by its namespace and name
// from the pod store, and answers nil for a pod the store does not hold.
type runPods struct {
	current map[string]string
	gone    map[string]map[string]bool
	lookup  func(namespace, name string) *Pod
}

func newRunPods() runPods {
	return runPods{current: map[string]string{}, gone: map[string]map[string]bool{}}
}

// fromRunPod answers whether a message that names this pod UID counts as
// the run's. A message with no UID comes from a sidecar that sets none,
// and the desk cannot tell its pod, so it counts. A pod known gone does
// not. A pod the store holds counts while it stands, and once it is
// deleting it does not. While the store holds a deleting pod, a pod with
// another UID is the new pod the store has not delivered yet, so it
// counts. With no pod in the store, a pod not known gone counts too,
// because the new pod can report before the store holds it.
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

// observe records the run's pod as the store holds it. A standing pod is
// the run's pod, and a pod it replaced is gone. A deleting pod is gone,
// and so is the pod the store held before when the store holds none now.
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

// back takes one pod off the gone list, for a delete that failed.
func (p *runPods) back(key, uid string) {
	delete(p.gone[key], uid)
}

// markGone records one pod as gone.
func (p *runPods) markGone(key, uid string) {
	if uid == "" {
		return
	}
	if p.gone[key] == nil {
		p.gone[key] = map[string]bool{}
	}
	p.gone[key][uid] = true
}

// retain forgets the runs the pass no longer lists.
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

// forget drops one run.
func (p *runPods) forget(key string) {
	delete(p.current, key)
	delete(p.gone, key)
}
