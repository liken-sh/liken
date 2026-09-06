package main

// The marks desk is the boundary between the progress store and the
// pass, in both directions. The store holds no API credential and the
// operator reads no store, so everything the two say to each other
// crosses the bus, retained. The bus handler folds each mark the store
// publishes onto this desk, and the pass reads it: a recorded mark
// releases a Play's finalizer, a Watch's projection becomes its
// status, and a namespace's forgotten answer counts toward releasing
// a Person. The pass publishes back what only the API server can say,
// and only when it changed, so a pass that changed nothing costs
// nothing on the bus.

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"sync"
)

// storeMarks holds the newest mark of each kind and the wake the loop
// reads. One mutex covers the three maps, because the bus handler runs
// on the bus reader's goroutine and the pass runs on the loop's.
type storeMarks struct {
	mutex sync.Mutex
	// What the store last wrote for one Play, and one Watch's
	// projection, both keyed by namespace and name.
	recorded map[string]playRecorded
	progress map[string]watchProgress
	// The namespaces that answered that one person's rows are gone.
	forgotten map[string]map[string]bool
	wake      chan<- struct{}
}

func newStoreMarks(wake chan<- struct{}) *storeMarks {
	return &storeMarks{
		recorded:  map[string]playRecorded{},
		progress:  map[string]watchProgress{},
		forgotten: map[string]map[string]bool{},
		wake:      wake,
	}
}

// markRecorded folds what the store wrote for one Play. A nil mark
// drops the entry, because an empty retained payload is how the store
// says it holds nothing for that Play any more.
func (m *storeMarks) markRecorded(namespace, name string, recorded *playRecorded) {
	key := libraryKey(namespace, name)
	m.mutex.Lock()
	if recorded == nil {
		delete(m.recorded, key)
	} else {
		m.recorded[key] = *recorded
	}
	m.mutex.Unlock()
	poke(m.wake)
}

// recordedFor answers what the store wrote for one Play, and false
// where it has written nothing. A Play with no mark is one the store
// has not seen, and its finalizer stays on.
func (m *storeMarks) recordedFor(namespace, name string) (playRecorded, bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	recorded, held := m.recorded[libraryKey(namespace, name)]
	return recorded, held
}

// retainRecorded drops every mark whose Play the pass did not see and
// answers with the keys it dropped, sorted, because the pass is the
// only reader that holds the whole Play list and so the only one that
// can clear the topics of a Play that is gone.
func (m *storeMarks) retainRecorded(live map[string]bool) []string {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	keys := []string{}
	for key := range m.recorded {
		if !live[key] {
			delete(m.recorded, key)
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}

// dropRecorded forgets one Play, which the pass does once it has
// released that Play and cleared its topics.
func (m *storeMarks) dropRecorded(namespace, name string) {
	m.mutex.Lock()
	delete(m.recorded, libraryKey(namespace, name))
	m.mutex.Unlock()
}

// markProgress folds one Watch's projection, on the same terms as a
// Play's mark.
func (m *storeMarks) markProgress(namespace, name string, progress *watchProgress) {
	key := libraryKey(namespace, name)
	m.mutex.Lock()
	if progress == nil {
		delete(m.progress, key)
	} else {
		m.progress[key] = *progress
	}
	m.mutex.Unlock()
	poke(m.wake)
}

// progressFor answers one Watch's projection, and false where the store
// has published none. A Watch with no projection keeps the status it
// carries.
func (m *storeMarks) progressFor(namespace, name string) (watchProgress, bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	progress, held := m.progress[libraryKey(namespace, name)]
	return progress, held
}

// markForgotten folds one namespace's answer about one person. False
// drops the answer, because the operator clears these topics itself and
// its own clear comes back to it.
func (m *storeMarks) markForgotten(person, namespace string, forgotten bool) {
	m.mutex.Lock()
	if !forgotten {
		delete(m.forgotten[person], namespace)
	} else {
		if m.forgotten[person] == nil {
			m.forgotten[person] = map[string]bool{}
		}
		m.forgotten[person][namespace] = true
	}
	m.mutex.Unlock()
	poke(m.wake)
}

// forgottenBy answers the namespaces that have dropped one person's
// rows, as a copy, so the pass reads it without the lock.
func (m *storeMarks) forgottenBy(person string) map[string]bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return maps.Clone(m.forgotten[person])
}

// dropForgotten forgets one person, which the pass does once it has
// released that Person and cleared the topics.
func (m *storeMarks) dropForgotten(person string) {
	m.mutex.Lock()
	delete(m.forgotten, person)
	m.mutex.Unlock()
}

// foldMark decodes one retained mark and hands it to the fold. An empty
// payload is how a retained topic is cleared, so it folds nothing and
// the desk drops the entry, and a payload that does not decode is
// reported and folded nowhere.
func foldMark[T any](topic string, payload []byte, fold func(*T)) {
	if len(payload) == 0 {
		fold(nil)
		return
	}
	mark := new(T)
	if err := json.Unmarshal(payload, mark); err != nil {
		fmt.Fprintf(os.Stderr, "reading the mark on %s: %v\n", topic, err)
		return
	}
	fold(mark)
}

// publishMark publishes one retained payload, and only where it differs
// from what this operator last put on the topic, so a pass that changed
// nothing costs nothing on the bus. The map it compares against is the
// pass's own, read and written on the loop's goroutine alone.
func (o *operator) publishMark(topic string, mark any) {
	payload, err := json.Marshal(mark)
	if err != nil {
		fmt.Fprintf(os.Stderr, "publishing on %s: %v\n", topic, err)
		return
	}
	if held, standing := o.published[topic]; standing && held == string(payload) {
		return
	}
	o.published[topic] = string(payload)
	o.bus.Publish(topic, payload, true)
}

// publishStanding publishes a retained payload only where this operator
// has put none on the topic. It is for a payload that carries the time
// of the ask: a republish on every pass would rewrite the ask with a
// later time and say nothing new.
func (o *operator) publishStanding(topic string, mark any) {
	if _, standing := o.published[topic]; standing {
		return
	}
	o.publishMark(topic, mark)
}

// clearTopic drops a retained message with an empty payload, which is
// how MQTT clears one, so a store that subscribes later reads nothing
// for a Play or a person that is gone.
func (o *operator) clearTopic(topic string) {
	delete(o.published, topic)
	o.bus.Publish(topic, nil, true)
}
