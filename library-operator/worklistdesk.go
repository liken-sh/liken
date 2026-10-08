package main

// worklistdesk.go holds the count of every work list the broker retains, as
// the operator's subscription to the count topics delivers them. A count is
// the event that says a library Job has published a list, so the pass starts
// the fact's worker from it (factworkerjob.go), and the pass clears every
// list it holds no reason to keep (worklistsweep.go). The subscription
// delivers every retained count when the operator connects, so a restarted
// operator reads back the lists that an earlier process left.

import (
	"cmp"
	"fmt"
	"os"
	"slices"
	"strconv"
	"sync"
)

type workLists struct {
	mutex  sync.Mutex
	counts map[workList]int
	wake   chan<- struct{}
}

func newWorkLists(wake chan<- struct{}) *workLists {
	return &workLists{counts: map[workList]int{}, wake: wake}
}

// Folds one count message. An empty payload is a cleared list. A new or
// changed count wakes the loop, because the pass that answers it can start a
// worker. A clear wakes nothing, because the pass that made it has already
// dropped the list.
func (d *workLists) fold(list workList, payload []byte) {
	if len(payload) == 0 {
		d.drop(list)
		return
	}
	count, err := strconv.Atoi(string(payload))
	if err != nil || count < 0 {
		fmt.Fprintf(os.Stderr, "reading the count of the %s list from the job %s: %q is not a count\n",
			list.fact, list.run, payload)
		return
	}
	d.mutex.Lock()
	previous, had := d.counts[list]
	d.counts[list] = count
	d.mutex.Unlock()
	if !had || previous != count {
		poke(d.wake)
	}
}

// The count of one list, and whether the broker holds it.
func (d *workLists) countOf(list workList) (int, bool) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	count, held := d.counts[list]
	return count, held
}

// Every list the desk holds, in a fixed order, so a pass clears them in the
// same order each time.
func (d *workLists) held() []workList {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	lists := make([]workList, 0, len(d.counts))
	for list := range d.counts {
		lists = append(lists, list)
	}
	slices.SortFunc(lists, func(a, b workList) int {
		return cmp.Or(cmp.Compare(a.namespace, b.namespace), cmp.Compare(a.library, b.library),
			cmp.Compare(a.fact, b.fact), cmp.Compare(a.run, b.run))
	})
	return lists
}

func (d *workLists) drop(list workList) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	delete(d.counts, list)
}
