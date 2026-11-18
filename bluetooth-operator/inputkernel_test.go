package main

import (
	"fmt"
	"io"
	"slices"
	"sync"
	"testing"
)

// fakeKernel implements inputKernel without /dev/uinput or real evdev
// nodes, so the relay tests run on any machine. It opens each real node
// as an io.Pipe and records what the relay does to each node.
type fakeKernel struct {
	mu sync.Mutex

	// capabilities is what readCapabilities returns for each node path.
	capabilities map[string]evdevCapabilities

	// writers holds the write end of each opened node. A test writes
	// events into the relay through it, and closes it to end the read.
	writers map[string]*io.PipeWriter

	// opens holds each opened node, so a test can read back the masks
	// and axis ranges that the relay set on it.
	opens map[string]*fakeNode

	// virtual is every device the relay created, newest last.
	virtual []*fakeVirtual

	// nextNode is the number in the path of the next virtual device.
	nextNode int

	// createErr is the error createVirtual returns, as when the kernel
	// refuses the container access to /dev/uinput.
	createErr error
}

func newFakeKernel() *fakeKernel {
	return &fakeKernel{
		capabilities: map[string]evdevCapabilities{},
		writers:      map[string]*io.PipeWriter{},
		opens:        map[string]*fakeNode{},
	}
}

// register makes path a node that reports the given name.
func (k *fakeKernel) register(path, name string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.capabilities[path] = evdevCapabilities{
		Name:  name,
		ID:    evdevID{Bus: 0x0005, Vendor: 0x054c, Product: 0x0ce6},
		Codes: map[string][]uint16{"EV_KEY": {0x130}},
	}
}

func (k *fakeKernel) readCapabilities(path string) (evdevCapabilities, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	caps, found := k.capabilities[path]
	if !found {
		return evdevCapabilities{}, fmt.Errorf("the fake kernel has no device %s", path)
	}
	return caps, nil
}

func (k *fakeKernel) open(path string) (realNode, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, found := k.capabilities[path]; !found {
		return nil, fmt.Errorf("the fake kernel has no device %s", path)
	}
	reader, writer := io.Pipe()
	k.writers[path] = writer
	// Each axis starts with the range the device declares, so a test
	// can tell whether the relay changed it.
	ranges := map[uint16]absInfo{}
	for _, axis := range k.capabilities[path].Axes {
		ranges[axis.Code] = absInfo{
			Minimum:    axis.Minimum,
			Maximum:    axis.Maximum,
			Fuzz:       axis.Fuzz,
			Flat:       axis.Flat,
			Resolution: axis.Resolution,
		}
	}
	node := &fakeNode{ReadCloser: reader, ranges: ranges}
	k.opens[path] = node
	return node, nil
}

// node returns the opened node at path, and fails the test if the relay
// never opened it.
func (k *fakeKernel) node(t *testing.T, path string) *fakeNode {
	t.Helper()
	k.mu.Lock()
	defer k.mu.Unlock()
	node, found := k.opens[path]
	if !found {
		t.Fatalf("the relay opened no node at %s, want one", path)
	}
	return node
}

// fakeNode is one opened node. Each call to setMasks replaces the
// masks of the previous call.
type fakeNode struct {
	io.ReadCloser
	mu    sync.Mutex
	masks []eventMask
	calls int

	// ranges is the current range of each absolute axis.
	ranges map[uint16]absInfo

	// written counts setAxisRange calls, so a test can check that a
	// node with no tuning gets none.
	written int
}

func (n *fakeNode) setMasks(masks []eventMask) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.masks, n.calls = masks, n.calls+1
	return nil
}

// passes reports whether the first mask lets events of this type
// through. A node with no mask lets every type through.
func (n *fakeNode) passes(event uint16) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.masks) == 0 {
		return true
	}
	return slices.Contains(n.masks[0].codes, event)
}

// maskWrites returns how many masks the relay has set on this node.
func (n *fakeNode) maskWrites() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.calls
}

func (n *fakeNode) axisRange(code uint16) (absInfo, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.ranges[code], nil
}

func (n *fakeNode) setAxisRange(code uint16, info absInfo) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.ranges[code], n.written = info, n.written+1
	return nil
}

// axis returns the current range of one axis.
func (n *fakeNode) axis(code uint16) absInfo {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.ranges[code]
}

// writes returns how many axis ranges the relay has written.
func (n *fakeNode) writes() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.written
}

func (k *fakeKernel) createVirtual(caps evdevCapabilities, phys string) (virtualDevice, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.createErr != nil {
		return nil, k.createErr
	}
	device := &fakeVirtual{path: fmt.Sprintf("/dev/input/event%d", k.nextNode), phys: phys, caps: caps}
	k.nextNode++
	k.virtual = append(k.virtual, device)
	return device, nil
}

// opened returns how many nodes the relay has opened.
func (k *fakeKernel) opened() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.writers)
}

// writer returns the write end of the pipe of the opened node at path.
func (k *fakeKernel) writer(t *testing.T, path string) *io.PipeWriter {
	t.Helper()
	k.mu.Lock()
	defer k.mu.Unlock()
	writer, found := k.writers[path]
	if !found {
		t.Fatalf("the relay opened no node at %s, want one", path)
	}
	return writer
}

// fakeVirtual is a virtual device that records the events written to it.
type fakeVirtual struct {
	path  string
	phys  string
	caps  evdevCapabilities
	mu    sync.Mutex
	got   []byte
	ended bool
}

func (d *fakeVirtual) node() string { return d.path }

func (d *fakeVirtual) write(events []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ended {
		return fmt.Errorf("the virtual device %s is closed", d.path)
	}
	d.got = append(d.got, events...)
	return nil
}

func (d *fakeVirtual) close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ended = true
	return nil
}

// closed reports whether the relay closed this device.
func (d *fakeVirtual) closed() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.ended
}

func (d *fakeVirtual) received() []byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]byte(nil), d.got...)
}
