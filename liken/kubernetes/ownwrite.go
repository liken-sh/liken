package kubernetes

import "sync"

// An OwnWrite remembers the resourceVersion that the API server
// answered for a writer's last write to one object, so the writer's
// watch can tell the echo of that write from another writer's change.
// A watch that woke the loop for its own echo would run a second pass
// for each write, and a pass whose write never compares equal would
// write as fast as the API server answers.
//
// The watch can deliver the echo before the write's answer reaches the
// writer, because the two travel on different connections. So Send
// holds the writer's turn until it has recorded the answer, and Wrote
// waits for the turn. The turn is a channel, not a mutex, so a watch
// handler that waits for it is durably blocked inside a synctest
// bubble, and a test can hold a write at that point.
//
// The zero OwnWrite remembers no write.
type OwnWrite struct {
	once    sync.Once
	turn    chan struct{}
	version string
}

func (o *OwnWrite) lock() {
	o.once.Do(func() { o.turn = make(chan struct{}, 1) })
	o.turn <- struct{}{}
}

func (o *OwnWrite) unlock() { <-o.turn }

// Send runs one write and remembers the version it answers. A write
// that fails remembers no version, because a request that timed out
// can still have landed, and its echo must then wake the loop.
func (o *OwnWrite) Send(write func() (version string, err error)) error {
	o.lock()
	defer o.unlock()
	version, err := write()
	if err != nil {
		version = ""
	}
	o.version = version
	return err
}

// Forget drops the remembered write, for a write that leaves no
// version, such as a delete.
func (o *OwnWrite) Forget() {
	o.lock()
	defer o.unlock()
	o.version = ""
}

// Wrote answers whether version is the one the API server answered
// for the last write. It waits for a write in flight to record its
// answer.
func (o *OwnWrite) Wrote(version string) bool {
	o.lock()
	defer o.unlock()
	return version != "" && version == o.version
}
