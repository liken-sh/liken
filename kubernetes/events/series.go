package events

import (
	"container/list"
	"time"
)

// window is how long after the last Event of a series a repeat still
// patches that Event. A repeat after a longer gap is a new occurrence,
// and a new Event shows where it began. client-go's recorder uses the
// same 10 minutes.
const window = 10 * time.Minute

// seriesLimit bounds the series the recorder remembers. A component
// with more open series than this forgets the oldest, and its next
// repeat starts a new Event. client-go's recorder uses the same 4096.
const seriesLimit = 4096

// entry is the Event that a series patches: its name, the count it
// holds, and the time of its last repeat.
type entry struct {
	name  string
	count int32
	last  time.Time
}

// series remembers the Event of each series, by key, and forgets the
// one used longest ago when it holds seriesLimit.
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

// keyOf answers the key of an Event's series: the object, the type,
// the reason, and the message. The UID tells apart two objects of one
// name, one deleted and one created after it.
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
