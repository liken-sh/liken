package main

// Reading the mode each output runs right now, straight from
// the card node, with the DRM mode-getting ioctls.
//
// Why the ioctls and not sysfs, weston, or debugfs: sysfs
// publishes the modes a connector accepts and never the one it runs;
// weston reports the running mode only over its private IPC; debugfs
// is not mounted in a container. GETCRTC needs no DRM master, no
// capability, and no library, and it answers while the compositor
// holds master, which the feasibility drill proved on the lab
// machine.
//
// The walk is GETRESOURCES to list the connectors and crtcs,
// GETCONNECTOR for each connector's encoder, GETENCODER for that
// encoder's crtc, and GETCRTC for the mode the crtc drives. A
// connector with no encoder and an encoder on no crtc are both an
// output that drives nothing.
//
// The ioctl numbers and the structs are the kernel's ABI, in
// full here because this operator links no libdrm. Each struct must
// keep its field order and its size: the request number carries the
// size, and the kernel refuses a call whose size it does not
// recognize.
//
// The two-pass GETRESOURCES is the kernel's counting protocol.
// The first pass answers how many connectors and crtcs the card has,
// and the second pass fills the arrays the caller allocated for them.

import (
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The four mode-getting requests, encoded with their direction
// and their struct size, as <drm/drm.h> writes them.
const (
	drmGetResources = 0xC04064A0
	drmGetCrtc      = 0xC06864A1
	drmGetEncoder   = 0xC01464A6
	drmGetConnector = 0xC05064A7
)

// Drm_mode_modeinfo, the timings of one mode. Only Name is read
// here, because the name is the vocabulary a claim, the sysfs list,
// and weston.ini all speak.
type drmModeInfo struct {
	Clock                                         uint32
	Hdisplay, HsyncStart, HsyncEnd, Htotal, Hskew uint16
	Vdisplay, VsyncStart, VsyncEnd, Vtotal, Vscan uint16
	Vrefresh, Flags, Type                         uint32
	Name                                          [32]byte
}

// Drm_mode_card_res. The pointer fields carry the addresses of
// arrays the caller allocated, and the count fields carry their
// lengths in and the card's own numbers out.
type drmCardResources struct {
	FbIDPtr, CrtcIDPtr, ConnectorIDPtr, EncoderIDPtr     uint64
	CountFbs, CountCrtcs, CountConnectors, CountEncoders uint32
	MinWidth, MaxWidth, MinHeight, MaxHeight             uint32
}

// Drm_mode_crtc. ModeValid is the field that says whether Mode
// describes anything.
type drmCrtc struct {
	SetConnectorsPtr                    uint64
	CountConnectors, CrtcID, FbID, X, Y uint32
	GammaSize, ModeValid                uint32
	Mode                                drmModeInfo
}

// Drm_mode_get_connector. The counts are left at zero, so the
// kernel fills in the scalar fields and copies no arrays.
type drmConnector struct {
	EncodersPtr, ModesPtr, PropsPtr, PropValuesPtr uint64
	CountModes, CountProps, CountEncoders          uint32
	EncoderID, ConnectorID, ConnectorType          uint32
	ConnectorTypeID, Connection                    uint32
	MmWidth, MmHeight, Subpixel, Pad               uint32
}

// Drm_mode_get_encoder. CrtcID is the whole reason for this
// call: it is what ties a connector to the crtc that drives it.
type drmEncoder struct {
	EncoderID, EncoderType, CrtcID uint32
	PossibleCrtcs, PossibleClones  uint32
}

// The kernel's own connector-type names, in its own numbering.
// sysfs names a connector directory by this table and a counter per
// type, so the same pair reproduces the name that everything else
// here keys on.
var drmConnectorTypes = map[uint32]string{
	1: "VGA", 2: "DVI-I", 3: "DVI-D", 4: "DVI-A", 5: "Composite",
	6: "SVIDEO", 7: "LVDS", 8: "Component", 9: "DIN", 10: "DP",
	11: "HDMI-A", 12: "HDMI-B", 13: "TV", 14: "eDP", 15: "Virtual",
	16: "DSI", 17: "DPI", 18: "Writeback", 19: "SPI", 20: "USB",
}

// ConnectorName rebuilds the sysfs name of one connector.
// A type this table does not carry answers nothing, and the caller
// publishes no mode for it, because a name this operator guessed
// would key a record that weston.ini never matches.
func connectorName(connectorType, typeID uint32) string {
	name, known := drmConnectorTypes[connectorType]
	if !known {
		return ""
	}
	return fmt.Sprintf("%s-%d", name, typeID)
}

// CrtcMode is the mode name a crtc drives, and nothing while it
// drives none. The kernel sets ModeValid only while the crtc is
// enabled, and it leaves whatever the caller sent in the Mode field
// otherwise.
func crtcMode(crtc drmCrtc) string {
	if crtc.ModeValid == 0 {
		return ""
	}
	name := crtc.Mode.Name[:]
	for i, b := range name {
		if b == 0 {
			return string(name[:i])
		}
	}
	return string(name)
}

// ReadCurrentModes answers the mode each of the card's outputs
// runs right now, keyed by the connector name sysfs uses.
//
// An output that runs no mode is absent from the map rather
// than present with an empty value, because absent is what the slice
// publishes for it and what a wait for a mode change must not accept.
func readCurrentModes(cardPath string) (map[string]string, error) {
	fd, err := unix.Open(cardPath, unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", cardPath, err)
	}
	defer unix.Close(fd)

	connectors, err := cardConnectors(fd)
	if err != nil {
		return nil, err
	}
	modes := map[string]string{}
	for _, id := range connectors {
		name, mode, err := connectorMode(fd, id)
		if err != nil || name == "" || mode == "" {
			// One connector that answers nothing costs its own
			// entry and never the whole read. A card removes a connector
			// while this walk runs when its driver unbinds.
			continue
		}
		modes[name] = mode
	}
	return modes, nil
}

// CardConnectors runs the counting protocol and answers the
// card's connector ids.
func cardConnectors(fd int) ([]uint32, error) {
	var resources drmCardResources
	if err := drmIoctl(fd, drmGetResources, unsafe.Pointer(&resources)); err != nil {
		return nil, fmt.Errorf("counting the card's resources: %w", err)
	}
	if resources.CountConnectors == 0 {
		return nil, nil
	}
	connectors := make([]uint32, resources.CountConnectors)
	resources.ConnectorIDPtr = uint64(uintptr(unsafe.Pointer(&connectors[0])))
	// The framebuffer, crtc, and encoder arrays stay unasked
	// for. A count of zero tells the kernel to copy none of them, and
	// this walk reaches the crtc through the connector's encoder.
	resources.CountFbs, resources.CountCrtcs, resources.CountEncoders = 0, 0, 0
	err := drmIoctl(fd, drmGetResources, unsafe.Pointer(&resources))
	runtime.KeepAlive(connectors)
	if err != nil {
		return nil, fmt.Errorf("listing the card's connectors: %w", err)
	}
	return connectors, nil
}

// ConnectorMode follows one connector to its encoder, that
// encoder to its crtc, and answers the connector's name and the mode
// the crtc drives.
func connectorMode(fd int, id uint32) (string, string, error) {
	connector := drmConnector{ConnectorID: id}
	if err := drmIoctl(fd, drmGetConnector, unsafe.Pointer(&connector)); err != nil {
		return "", "", err
	}
	name := connectorName(connector.ConnectorType, connector.ConnectorTypeID)
	if connector.EncoderID == 0 {
		// A connector with no encoder drives nothing, which is
		// every connector with no monitor on it and every connector the
		// compositor left disabled.
		return name, "", nil
	}
	encoder := drmEncoder{EncoderID: connector.EncoderID}
	if err := drmIoctl(fd, drmGetEncoder, unsafe.Pointer(&encoder)); err != nil {
		return name, "", err
	}
	if encoder.CrtcID == 0 {
		return name, "", nil
	}
	crtc := drmCrtc{CrtcID: encoder.CrtcID}
	if err := drmIoctl(fd, drmGetCrtc, unsafe.Pointer(&crtc)); err != nil {
		return name, "", err
	}
	return name, crtcMode(crtc), nil
}

// DrmIoctl is the one syscall this file makes. The kernel
// answers an errno, and Go's ioctl wrappers cover none of these
// requests.
func drmIoctl(fd int, request uint, arg unsafe.Pointer) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(request), uintptr(arg))
	if errno != 0 {
		return errno
	}
	return nil
}
