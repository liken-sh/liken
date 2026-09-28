package main

// The fake kernel every test of the relay drives, which is the
// inputKernel seam with no /dev/uinput and no real evdev node behind
// it. It answers with the capabilities a test chose, hands out an
// io.Pipe for each real node, and records what the relay asked the
// kernel for: the virtual devices it created and what reached them,
// the mask on each real node, and the absinfo it wrote to each axis.
// So the relay's whole policy tests on any machine.

import (
	"fmt"
	"io"
	"slices"
	"sync"
	"testing"
)

// fakeKernel stands in for /dev/uinput and the real evdev nodes.
type fakeKernel struct {
	mu sync.Mutex

	// capabilities answers readCapabilities for each real node path.
	capabilities map[string]evdevCapabilities

	// writers holds the write end of each opened real node, so a test
	// can send events into the relay and can end the read.
	writers map[string]*io.PipeWriter

	// opens holds each opened real node, so a test can read back what
	// the relay told the kernel to queue on it.
	opens map[string]*fakeNode

	// virtual is every device the relay created, newest last.
	virtual []*fakeVirtual

	// nextNode numbers the nodes this kernel hands out.
	nextNode int
}

func newFakeKernel() *fakeKernel {
	return &fakeKernel{
		capabilities: map[string]evdevCapabilities{},
		writers:      map[string]*io.PipeWriter{},
		opens:        map[string]*fakeNode{},
	}
}

// register declares what a real node reports for itself.
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
		return evdevCapabilities{}, fmt.Errorf("no such device %s", path)
	}
	return caps, nil
}

func (k *fakeKernel) open(path string) (realNode, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, found := k.capabilities[path]; !found {
		return nil, fmt.Errorf("no such device %s", path)
	}
	reader, writer := io.Pipe()
	k.writers[path] = writer
	// The kernel reports each axis the way the device declares it, so
	// a test sees this operator's own writes and nothing else.
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

// node answers with one opened real node, so a test reads the mask
// the relay set on it.
func (k *fakeKernel) node(t *testing.T, path string) *fakeNode {
	t.Helper()
	k.mu.Lock()
	defer k.mu.Unlock()
	node, found := k.opens[path]
	if !found {
		t.Fatalf("the relay never opened %s", path)
	}
	return node
}

// fakeNode is one open real node: the read end of its pipe, and the
// mask the relay last set on it. The kernel keeps a mask until
// another one replaces it, and so does this.
type fakeNode struct {
	io.ReadCloser
	mu    sync.Mutex
	masks []eventMask
	calls int

	// ranges is what the kernel reports for each absolute axis of this
	// node, which is what this operator last wrote when it has written
	// one.
	ranges map[uint16]absInfo

	// written counts the axes this operator wrote, so a test can show
	// that a node no claim tunes takes no write at all.
	written int
}

func (n *fakeNode) narrow(masks []eventMask) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.masks, n.calls = masks, n.calls+1
	return nil
}

// delivers reports whether the mask on this node lets one event type
// through. A node with no mask delivers every type.
func (n *fakeNode) delivers(event uint16) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.masks) == 0 {
		return true
	}
	return slices.Contains(n.masks[0].codes, event)
}

// narrowings is how many masks the relay has set on this node.
func (n *fakeNode) narrowings() int {
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

// axis is what the kernel reports for one axis of this node now.
func (n *fakeNode) axis(code uint16) absInfo {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.ranges[code]
}

// writes is how many axes this operator has written on this node.
func (n *fakeNode) writes() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.written
}

func (k *fakeKernel) createVirtual(caps evdevCapabilities, phys string) (virtualDevice, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	device := &fakeVirtual{path: fmt.Sprintf("/dev/input/event%d", k.nextNode), phys: phys, caps: caps}
	k.nextNode++
	k.virtual = append(k.virtual, device)
	return device, nil
}

// opened is how many real nodes the relay has opened so far.
func (k *fakeKernel) opened() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.writers)
}

// writer answers with the write end of one opened real node.
func (k *fakeKernel) writer(t *testing.T, path string) *io.PipeWriter {
	t.Helper()
	k.mu.Lock()
	defer k.mu.Unlock()
	writer, found := k.writers[path]
	if !found {
		t.Fatalf("the relay never opened %s", path)
	}
	return writer
}

// fakeVirtual is one virtual device, and the events that reached it.
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
		return fmt.Errorf("%s is closed", d.path)
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

// closed reports whether the relay destroyed this device.
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
