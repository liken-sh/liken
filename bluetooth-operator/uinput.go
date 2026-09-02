package main

// The virtual input device, which is what a claim on a controller
// delivers. The kernel's uinput interface creates an evdev node from a
// description of what the device can do, and holds the node for as
// long as the fd stays open. This file and evdev.go are the whole of
// what the operator asks the kernel's input layer for, behind the
// inputKernel interface, so the relay's policy is testable with no
// /dev/uinput.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unsafe"
)

// uinputPath is the kernel's uinput interface, and deliveredInputNodes
// is how many evdev nodes liken's adapter claim creates in this
// container: /dev/input/event0 through event31, char 13 minors 64
// through 95. A virtual device that lands above that number has no
// node inside the container.
const (
	uinputPath          = "/dev/uinput"
	deliveredInputNodes = 32
)

// inputKernel is every call this operator makes into the kernel's
// input layer. relay.go holds the policy and takes one of these, so a
// test drives the policy with no kernel at all.
type inputKernel interface {
	readCapabilities(path string) (evdevCapabilities, error)
	open(path string) (io.ReadCloser, error)
	createVirtual(caps evdevCapabilities, phys string) (virtualDevice, error)
}

// virtualDevice is one uinput device this operator holds open. The
// node exists for as long as the fd does, which is the whole reason
// the relay exists.
type virtualDevice interface {
	node() string
	write(events []byte) error
	close() error
}

// uinputMaxNameSize is UINPUT_MAX_NAME_SIZE, the fixed field a
// device's name goes in.
const uinputMaxNameSize = 80

// The two structures uinput takes, with the layout of their
// counterparts in linux/uinput.h. UI_DEV_SETUP describes the device,
// and UI_ABS_SETUP gives one absolute axis its range.
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

// The uinput writes, in the order a device is built: declare the
// capabilities, describe the axes, name the device, then create it.
// UI_SET_PHYS takes a pointer to a C string, so its size is a
// pointer's, which is what the kernel's own macro spells.
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

// uiSetBit builds one of the UI_SET_*BIT numbers. Every one of them
// declares an int argument, and the kernel reads the value itself
// rather than a pointer to it.
func uiSetBit(number uint32) uint32 {
	return ioc(iocWrite, uint32(unsafe.Sizeof(int32(0))), uinputLetter, number)
}

func uiGetSysname(size uint32) uint32 { return ioc(iocRead, size, uinputLetter, 44) }

// linuxInput is the kernel itself.
type linuxInput struct{}

// open opens a real evdev node for reading.
func (linuxInput) open(path string) (io.ReadCloser, error) {
	return os.Open(path)
}

// createVirtual builds one uinput device from a capability snapshot
// and holds it open.
//
// The order is the kernel's: declare each event type and each code
// before UI_DEV_SETUP, because the ioctls that set a bit are refused
// once the device is created. UI_SET_ABSBIT and UI_ABS_SETUP are both
// needed: the setup call writes the range into the device's absinfo
// and sets no bit. phys names this operator and the controller, so a
// person reading /proc/bus/input/devices reads where the events come
// from.
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

// uinputDevice is one open uinput fd and the node the kernel gave it.
type uinputDevice struct {
	file *os.File
	path string
}

func (d *uinputDevice) node() string { return d.path }

// write hands one read from a real node straight to the kernel. The
// record layout is the same on both sides, so nothing here decodes an
// event.
func (d *uinputDevice) write(events []byte) error {
	_, err := d.file.Write(events)
	return err
}

// close destroys the device and releases the fd. The node goes with
// the fd whether or not the destroy call succeeds, and the destroy is
// the orderly half.
func (d *uinputDevice) close() error {
	_ = ioctlInt(int(d.file.Fd()), uiDevDestroy, 0)
	return d.file.Close()
}

// build declares the device's capabilities, creates it, and finds the
// node the kernel gave it.
func (d *uinputDevice) build(caps evdevCapabilities, phys string) error {
	fd := int(d.file.Fd())
	for _, property := range caps.Properties {
		if err := ioctlInt(fd, uiSetPropBit, int(property)); err != nil {
			return fmt.Errorf("declaring property %d: %w", property, err)
		}
	}
	for _, mirrored := range mirroredTypes {
		codes := caps.Codes[mirrored.name]
		if len(codes) == 0 {
			continue
		}
		if err := ioctlInt(fd, uiSetEvBit, int(mirrored.event)); err != nil {
			return fmt.Errorf("declaring %s: %w", mirrored.name, err)
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

// virtualNode answers with the /dev path of the node the kernel just
// created. UI_GET_SYSNAME names the input device in sysfs, and the
// evdev child below it carries the node's DEVNAME, which is the same
// read the controller walk makes.
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

// withinDeliveredRange reports whether a node is one of the ones
// liken's adapter claim created in this container. A node above the
// range exists on the host and nowhere else, so a claim that named it
// would fail every container creation.
func withinDeliveredRange(node string) bool {
	number, err := strconv.Atoi(strings.TrimPrefix(node, "/dev/input/event"))
	if err != nil {
		return false
	}
	return number < deliveredInputNodes
}
