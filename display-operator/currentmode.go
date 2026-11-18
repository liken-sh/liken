package main

// readCurrentModes and readConnectorModes read the card node with the
// DRM mode ioctls. The first returns the mode each output runs, and the
// second the modes each connector offers.
//
// The current mode has no other source in the container. sysfs lists
// the modes a connector offers but not the one it runs, weston reports
// the running mode only over its private IPC, and debugfs is not
// mounted. These ioctls need no DRM master, so they work while the
// compositor is master. Each read uses a file that the gate in
// cardgate.go opens.
//
// The ioctl numbers and structs copy the kernel's ABI, because the
// operator links no libdrm. Each struct must match the kernel's layout
// field for field, because the kernel reads and writes each field at
// its offset and copies only as many bytes as the size in the request
// number.
//
// GETRESOURCES and GETCONNECTOR each take two calls. The first, with
// zero counts, returns how many entries exist. The second fills arrays
// that the caller allocated to that size.

import (
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The request numbers from <drm/drm.h>. Each encodes the direction and
// the size of its struct.
const (
	drmGetResources = 0xC04064A0
	drmGetCrtc      = 0xC06864A1
	drmGetEncoder   = 0xC01464A6
	drmGetConnector = 0xC05064A7
)

// drmModeInfo is the kernel's drm_mode_modeinfo, the timings of one
// mode. The operator reads Name, which has the format of the sysfs mode
// list, a claim's mode, and weston.ini, and Vrefresh, the refresh rate
// rounded to an integer, which weston compares with a claim's @refresh.
type drmModeInfo struct {
	Clock                                         uint32
	Hdisplay, HsyncStart, HsyncEnd, Htotal, Hskew uint16
	Vdisplay, VsyncStart, VsyncEnd, Vtotal, Vscan uint16
	Vrefresh, Flags, Type                         uint32
	Name                                          [32]byte
}

// drmCardResources is the kernel's drm_mode_card_res. The count fields
// hold the lengths of the caller's arrays on input and the card's totals
// on output.
type drmCardResources struct {
	FbIDPtr, CrtcIDPtr, ConnectorIDPtr, EncoderIDPtr     uint64
	CountFbs, CountCrtcs, CountConnectors, CountEncoders uint32
	MinWidth, MaxWidth, MinHeight, MaxHeight             uint32
}

// drmCrtc is the kernel's drm_mode_crtc.
type drmCrtc struct {
	SetConnectorsPtr                    uint64
	CountConnectors, CrtcID, FbID, X, Y uint32
	GammaSize, ModeValid                uint32
	Mode                                drmModeInfo
}

// drmConnector is the kernel's drm_mode_get_connector. With every count
// zero, the kernel fills only the scalar fields and copies no arrays.
type drmConnector struct {
	EncodersPtr, ModesPtr, PropsPtr, PropValuesPtr uint64
	CountModes, CountProps, CountEncoders          uint32
	EncoderID, ConnectorID, ConnectorType          uint32
	ConnectorTypeID, Connection                    uint32
	MmWidth, MmHeight, Subpixel, Pad               uint32
}

// drmEncoder is the kernel's drm_mode_get_encoder. CrtcID is the crtc
// that drives the encoder's connector, or 0 for none.
type drmEncoder struct {
	EncoderID, EncoderType, CrtcID uint32
	PossibleCrtcs, PossibleClones  uint32
}

// drmConnectorTypes maps the kernel's connector type numbers to the
// type names in the kernel's connector names, such as HDMI-A in
// HDMI-A-1.
var drmConnectorTypes = map[uint32]string{
	1: "VGA", 2: "DVI-I", 3: "DVI-D", 4: "DVI-A", 5: "Composite",
	6: "SVIDEO", 7: "LVDS", 8: "Component", 9: "DIN", 10: "DP",
	11: "HDMI-A", 12: "HDMI-B", 13: "TV", 14: "eDP", 15: "Virtual",
	16: "DSI", 17: "DPI", 18: "Writeback", 19: "SPI", 20: "USB",
}

// connectorName returns the kernel's name for a connector, the name
// that sysfs, weston.ini, and the rest of the operator use. It returns
// "" for a type that the table lacks, and the callers skip that
// connector, because a guessed name would match no connector in sysfs
// or weston.ini.
func connectorName(connectorType, typeID uint32) string {
	name, known := drmConnectorTypes[connectorType]
	if !known {
		return ""
	}
	return fmt.Sprintf("%s-%d", name, typeID)
}

// crtcMode returns the mode a crtc drives as NAME@REFRESH, the format
// that claims use, or "" when the crtc drives none. The kernel fills
// Mode only when it sets ModeValid. When the card reports no refresh,
// crtcMode returns the name alone, because a mode such as 1280x720@0
// matches nothing.
func crtcMode(crtc drmCrtc) string {
	if crtc.ModeValid == 0 {
		return ""
	}
	name := modeName(crtc.Mode)
	if name == "" || crtc.Mode.Vrefresh == 0 {
		return name
	}
	return fmt.Sprintf("%s@%d", name, crtc.Mode.Vrefresh)
}

// modeName reads the kernel's 32-byte name field, which ends at the
// first NUL byte or fills all 32 bytes.
func modeName(info drmModeInfo) string {
	name := info.Name[:]
	for i, b := range name {
		if b == 0 {
			return string(name[:i])
		}
	}
	return string(name)
}

// drmMode is one mode that a connector offers. sysfs lists the same
// names without refresh rates, so a claim's mode is validated against
// these.
type drmMode struct {
	Name    string
	Refresh int
}

// readConnectorModes returns the modes each connector offers, keyed by
// connector name.
func readConnectorModes(fd int) (map[string][]drmMode, error) {
	connectors, err := cardConnectors(fd)
	if err != nil {
		return nil, err
	}
	offered := map[string][]drmMode{}
	for _, id := range connectors {
		name, modes, err := connectorModeList(fd, id)
		if err != nil || name == "" || len(modes) == 0 {
			continue
		}
		offered[name] = modes
	}
	return offered, nil
}

// connectorModeList returns one connector's name and offered modes. It
// calls GETCONNECTOR once to count the modes and once to fill an array
// of that size. If the list grew between the two calls it returns no
// modes, and the next pass reads the list again.
func connectorModeList(fd int, id uint32) (string, []drmMode, error) {
	counted := drmConnector{ConnectorID: id}
	if err := drmIoctl(fd, drmGetConnector, unsafe.Pointer(&counted)); err != nil {
		return "", nil, err
	}
	name := connectorName(counted.ConnectorType, counted.ConnectorTypeID)
	if counted.CountModes == 0 {
		return name, nil, nil
	}
	infos := make([]drmModeInfo, counted.CountModes)
	filled := drmConnector{
		ConnectorID: id,
		ModesPtr:    uint64(uintptr(unsafe.Pointer(&infos[0]))),
		CountModes:  counted.CountModes,
	}
	err := drmIoctl(fd, drmGetConnector, unsafe.Pointer(&filled))
	runtime.KeepAlive(infos)
	if err != nil {
		return name, nil, err
	}
	if filled.CountModes > counted.CountModes {
		return name, nil, nil
	}
	modes := make([]drmMode, 0, filled.CountModes)
	for _, info := range infos[:filled.CountModes] {
		modes = append(modes, drmMode{Name: modeName(info), Refresh: int(info.Vrefresh)})
	}
	return name, modes, nil
}

// readCurrentModes returns the mode each output runs, keyed by connector
// name. An output that runs no mode is absent from the map, not present
// with an empty value. The slice publishes nothing for it, and a wait
// for a mode change must not treat it as a mode.
func readCurrentModes(fd int) (map[string]string, error) {
	connectors, err := cardConnectors(fd)
	if err != nil {
		return nil, err
	}
	modes := map[string]string{}
	for _, id := range connectors {
		name, mode, err := connectorMode(fd, id)
		if err != nil || name == "" || mode == "" {
			// Skip only this connector, because the kernel can remove a
			// connector between GETRESOURCES and GETCONNECTOR.
			continue
		}
		modes[name] = mode
	}
	return modes, nil
}

// cardConnectors returns the ids of the card's connectors.
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
	// Zero counts make the kernel skip the framebuffer, crtc, and
	// encoder arrays. connectorMode reaches each crtc through its
	// connector's encoder.
	resources.CountFbs, resources.CountCrtcs, resources.CountEncoders = 0, 0, 0
	err := drmIoctl(fd, drmGetResources, unsafe.Pointer(&resources))
	runtime.KeepAlive(connectors)
	if err != nil {
		return nil, fmt.Errorf("listing the card's connectors: %w", err)
	}
	return connectors, nil
}

// connectorMode follows one connector to its encoder with GETCONNECTOR
// and GETENCODER, and reads the mode of the encoder's crtc with
// GETCRTC. It returns the connector's name and that mode.
func connectorMode(fd int, id uint32) (string, string, error) {
	connector := drmConnector{ConnectorID: id}
	if err := drmIoctl(fd, drmGetConnector, unsafe.Pointer(&connector)); err != nil {
		return "", "", err
	}
	name := connectorName(connector.ConnectorType, connector.ConnectorTypeID)
	if connector.EncoderID == 0 {
		// The connector drives nothing: no monitor is on it, or the
		// compositor left it disabled.
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

// drmIoctl sends one DRM ioctl and returns its errno.
func drmIoctl(fd int, request uint, arg unsafe.Pointer) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(request), uintptr(arg))
	if errno != 0 {
		return errno
	}
	return nil
}
