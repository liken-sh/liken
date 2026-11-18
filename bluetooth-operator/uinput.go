package main

// A claim on a controller gives the pod a virtual input device that
// this operator creates through uinput. The kernel creates an evdev
// node from the device's capabilities and removes it when the uinput fd
// closes. This file and evdev.go make every call into the kernel's
// input layer, behind the inputKernel interface, so the tests of
// relay.go run without /dev/uinput.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unsafe"
)

// The adapter claim of this operator's pod creates /dev/input/event0
// through event31 in the container (deploy/operator.yaml).
// containerInputNodes is the size of that range. A virtual device that
// the kernel numbers above event31 has no node in the container.
const (
	uinputPath          = "/dev/uinput"
	containerInputNodes = 32
)

// inputKernel is implemented by linuxInput and by a fake in the tests.
type inputKernel interface {
	readCapabilities(path string) (evdevCapabilities, error)
	open(path string) (realNode, error)
	createVirtual(caps evdevCapabilities, phys string) (virtualDevice, error)
}

// realNode is one open evdev node of a controller. The mask and axis
// calls are methods of the node because the kernel applies them to the
// fd they are sent on.
type realNode interface {
	io.ReadCloser
	setMasks(masks []eventMask) error
	axisRange(code uint16) (absInfo, error)
	setAxisRange(code uint16, info absInfo) error
}

// virtualDevice is one uinput device. The operator keeps its fd open
// for as long as a pod needs the node.
type virtualDevice interface {
	node() string
	write(events []byte) error
	close() error
}

// uinputMaxNameSize is UINPUT_MAX_NAME_SIZE, the size of the name field.
const uinputMaxNameSize = 80

// struct uinput_setup and struct uinput_abs_setup from linux/uinput.h,
// the arguments of UI_DEV_SETUP and UI_ABS_SETUP.
type (
	uinputSetup struct {
		ID           inputID
		Name         [uinputMaxNameSize]byte
		FFEffectsMax uint32
	}

	uinputAbsSetup struct {
		Code uint16
		Info absInfo
	}
)

// The uinput ioctl numbers from linux/uinput.h. UI_SET_PHYS takes a
// pointer to a C string, so its size is the size of a pointer, as in
// the kernel's macro.
var (
	uiDevCreate  = ioc(iocNone, 0, uinputLetter, 1)
	uiDevDestroy = ioc(iocNone, 0, uinputLetter, 2)
	uiDevSetup   = ioc(iocWrite, uint32(unsafe.Sizeof(uinputSetup{})), uinputLetter, 3)
	uiAbsSetup   = ioc(iocWrite, uint32(unsafe.Sizeof(uinputAbsSetup{})), uinputLetter, 4)

	uiSetEvBit   = uiSetBit(100)
	uiSetKeyBit  = uiSetBit(101)
	uiSetRelBit  = uiSetBit(102)
	uiSetAbsBit  = uiSetBit(103)
	uiSetMscBit  = uiSetBit(104)
	uiSetLedBit  = uiSetBit(105)
	uiSetSwBit   = uiSetBit(109)
	uiSetPropBit = uiSetBit(110)

	uiSetPhys = ioc(iocWrite, uint32(unsafe.Sizeof(uintptr(0))), uinputLetter, 108)
)

// uiSetBit returns a UI_SET_*BIT number. The kernel reads the int
// argument as a value, not as a pointer.
func uiSetBit(number uint32) uint32 {
	return ioc(iocWrite, uint32(unsafe.Sizeof(int32(0))), uinputLetter, number)
}

func uiGetSysname(size uint32) uint32 { return ioc(iocRead, size, uinputLetter, 44) }

type linuxInput struct{}

// createVirtual creates a uinput device with the capabilities in caps
// and returns it with its fd open. phys is shown in
// /proc/bus/input/devices, so a person can tell which controller a
// virtual device copies.
func (linuxInput) createVirtual(caps evdevCapabilities, phys string) (virtualDevice, error) {
	file, err := os.OpenFile(uinputPath, os.O_WRONLY, 0)
	if err != nil {
		return nil, err
	}
	device := &uinputDevice{file: file}
	if err := device.build(caps, phys); err != nil {
		_ = file.Close()
		return nil, err
	}
	return device, nil
}

type uinputDevice struct {
	file *os.File
	path string
}

func (d *uinputDevice) node() string { return d.path }

// write takes the bytes of one read from a real node unchanged,
// because uinput accepts the same struct input_event records that
// evdev returns.
func (d *uinputDevice) write(events []byte) error {
	_, err := d.file.Write(events)
	return err
}

// close ignores the error of UI_DEV_DESTROY, because the kernel also
// destroys the device when the fd closes.
func (d *uinputDevice) close() error {
	_ = ioctlInt(int(d.file.Fd()), uiDevDestroy, 0)
	return d.file.Close()
}

// build sets every capability before UI_DEV_CREATE, because the kernel
// refuses the UI_SET_*BIT calls once the device exists.
func (d *uinputDevice) build(caps evdevCapabilities, phys string) error {
	fd := int(d.file.Fd())
	for _, property := range caps.Properties {
		if err := ioctlInt(fd, uiSetPropBit, int(property)); err != nil {
			return fmt.Errorf("declaring property %d: %w", property, err)
		}
	}
	for _, mirrored := range mirroredTypes {
		// declaredCodes adds KEY_UNKNOWN to a node that has keyboard
		// codes, because wake.go writes it when a controller reconnects.
		codes := declaredCodes(caps, mirrored.name)
		if len(codes) == 0 {
			continue
		}
		if err := ioctlInt(fd, uiSetEvBit, int(mirrored.event)); err != nil {
			return fmt.Errorf("declaring event type %s: %w", mirrored.name, err)
		}
		for _, code := range codes {
			if err := ioctlInt(fd, mirrored.setBit, int(code)); err != nil {
				return fmt.Errorf("declaring %s code %d: %w", mirrored.name, code, err)
			}
		}
	}
	for _, axis := range caps.Axes {
		setup := uinputAbsSetup{
			Code: axis.Code,
			Info: absInfo{
				Minimum:    axis.Minimum,
				Maximum:    axis.Maximum,
				Fuzz:       axis.Fuzz,
				Flat:       axis.Flat,
				Resolution: axis.Resolution,
			},
		}
		if err := ioctlPtr(fd, uiAbsSetup, unsafe.Pointer(&setup)); err != nil {
			return fmt.Errorf("setting up axis %d: %w", axis.Code, err)
		}
	}

	address := append([]byte(phys), 0)
	if err := ioctlPtr(fd, uiSetPhys, unsafe.Pointer(&address[0])); err != nil {
		return fmt.Errorf("naming the physical address: %w", err)
	}

	setup := uinputSetup{ID: inputID{
		Bus:     caps.ID.Bus,
		Vendor:  caps.ID.Vendor,
		Product: caps.ID.Product,
		Version: caps.ID.Version,
	}}
	copy(setup.Name[:uinputMaxNameSize-1], caps.Name)
	if err := ioctlPtr(fd, uiDevSetup, unsafe.Pointer(&setup)); err != nil {
		return fmt.Errorf("describing the device: %w", err)
	}
	if err := ioctlInt(fd, uiDevCreate, 0); err != nil {
		return fmt.Errorf("creating the device: %w", err)
	}

	path, err := virtualNode(fd)
	if err != nil {
		return err
	}
	d.path = path
	return nil
}

// virtualNode returns the /dev path of the evdev node of the uinput
// device on fd. UI_GET_SYSNAME returns the device's directory name in
// sysfs, and evdevNodes reads the node's DEVNAME there, as it does for
// a real controller.
func virtualNode(fd int) (string, error) {
	sysname := make([]byte, uinputMaxNameSize)
	if err := ioctlPtr(fd, uiGetSysname(uint32(len(sysname))), unsafe.Pointer(&sysname[0])); err != nil {
		return "", fmt.Errorf("reading the sysfs name: %w", err)
	}
	directory := filepath.Join(draSysfsRoot, "devices", "virtual", "input", string(sysname[:clen(sysname)]))
	nodes := evdevNodes(directory)
	if len(nodes) == 0 {
		return "", fmt.Errorf("the device at %s registered no evdev node", directory)
	}
	return nodes[0], nil
}

// withinContainerNodes reports whether node is one of the /dev/input
// nodes that exist in this container.
func withinContainerNodes(node string) bool {
	number, err := strconv.Atoi(strings.TrimPrefix(node, "/dev/input/event"))
	if err != nil {
		return false
	}
	return number < containerInputNodes
}
