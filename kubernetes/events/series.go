package events

import (
	"container/list"
	"time"
)

// window is how long after a series' last repeat the next repeat still
// patches the same Event. After a longer gap the recorder creates a new
// Event, so its firstTimestamp shows when the new occurrence began.
const window = 10 * time.Minute

// seriesLimit is the most series the recorder keeps, the same limit as
// client-go's recorder. Past it, the recorder drops the least recently
// used series, and that series' next repeat creates a new Event.
const seriesLimit = 4096

// entry is the state needed to patch a series' Event: the Event's name,
// its count, and the time of the last repeat.
type entry struct {
	name  string
	count int32
	last  time.Time
}

// series maps each series key to its entry, and drops the least
// recently used entry when it exceeds seriesLimit.
type series struct {
	order *list.List
	byKey map[string]*list.Element
}

type seriesItem struct {
	key   string
	entry entry
}

func newSeries() *series {
	return &series{order: list.New(), byKey: map[string]*list.Element{}}
}

// keyOf returns the key of an Event's series: the object, type, reason,
// and message. The UID separates an object from a later object that was
// created with the same name after the first was deleted.
func keyOf(event Event) string {
	object := event.InvolvedObject
	return object.Kind + "\x00" + object.Namespace + "\x00" + object.Name + "\x00" + object.UID + "\x00" +
		event.Type + "\x00" + event.Reason + "\x00" + event.Message
}

func (s *series) get(key string) (entry, bool) {
	element, ok := s.byKey[key]
	if !ok {
		return entry{}, false
	}
	s.order.MoveToFront(element)
	return element.Value.(*seriesItem).entry, true
}

func (s *series) put(key string, e entry) {
	if element, ok := s.byKey[key]; ok {
		element.Value.(*seriesItem).entry = e
		s.order.MoveToFront(element)
		return
	}
	s.byKey[key] = s.order.PushFront(&seriesItem{key: key, entry: e})
	if s.order.Len() > seriesLimit {
		oldest := s.order.Back()
		s.order.Remove(oldest)
		delete(s.byKey, oldest.Value.(*seriesItem).key)
	}
}

func (s *series) remove(key string) {
	if element, ok := s.byKey[key]; ok {
		s.order.Remove(element)
		delete(s.byKey, key)
	}
}
