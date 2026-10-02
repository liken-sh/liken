package main

// The query of one render node through libva.
//
// The program calls libva without cgo, through purego, which opens a
// shared library and calls its functions from Go. Every other program
// in this repository builds with CGO_ENABLED=0, and purego keeps that
// build: the same Go stage, with no C toolchain and no libva headers.
// The result is not a static binary. It names the system's dynamic
// loader, and it opens libva.so.2 and libva-drm.so.2 when the query
// runs, so it runs only in an image that holds both, such as the vaapi
// base. The agent's other work never opens libva.
//
// The query runs in a child process of the agent (query.go), so a
// driver that hangs or crashes ends the child and not the agent.

import (
	"encoding/binary"
	"fmt"
	"os"

	"github.com/ebitengine/purego"
)

// The two libraries, by soname. libva-drm opens a display on a DRM
// render node, and libva holds every query.
const (
	libvaName    = "libva.so.2"
	libvaDRMName = "libva-drm.so.2"
)

// VAConfigAttribRTFormat names the render-target formats of a config,
// and VASurfaceAttribPixelFormat names one surface format.
const (
	configAttribRTFormat     int32  = 0
	surfaceAttribPixelFormat int32  = 1
	genericValueTypeInteger  int32  = 1
	vaStatusSuccess          int32  = 0
	vaAttribNotSupported     uint32 = 0x80000000
)

// configAttrib is libva's VAConfigAttrib: a type and a value, 8 bytes.
type configAttrib struct {
	Type  int32
	Value uint32
}

// surfaceAttrib is libva's VASurfaceAttrib. The value is a
// VAGenericValue: a type, then a union of an int, a float, and two
// pointers, aligned to 8 bytes. The struct is 24 bytes on a 64-bit
// machine.
type surfaceAttrib struct {
	Type      int32
	Flags     uint32
	ValueType int32
	_         int32
	Value     uint64
}

// libva holds the functions the query calls.
type libva struct {
	getDisplayDRM          func(fd int32) uintptr
	initialize             func(display uintptr, major, minor *int32) int32
	terminate              func(display uintptr) int32
	errorStr               func(status int32) string
	queryVendorString      func(display uintptr) string
	maxNumProfiles         func(display uintptr) int32
	maxNumEntrypoints      func(display uintptr) int32
	queryConfigProfiles    func(display uintptr, profiles *int32, count *int32) int32
	queryConfigEntrypoints func(display uintptr, profile int32, entrypoints *int32, count *int32) int32
	getConfigAttributes    func(display uintptr, profile, entrypoint int32, attribs *configAttrib, count int32) int32
	createConfig           func(display uintptr, profile, entrypoint int32, attribs *configAttrib, count int32, id *uint32) int32
	destroyConfig          func(display uintptr, id uint32) int32
	querySurfaceAttributes func(display uintptr, id uint32, attribs *surfaceAttrib, count *uint32) int32
}

// openLibva opens the two libraries and binds each function. A library
// that is missing fails here, with the loader's own message.
func openLibva() (*libva, error) {
	va, err := purego.Dlopen(libvaName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", libvaName, err)
	}
	drm, err := purego.Dlopen(libvaDRMName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", libvaDRMName, err)
	}
	l := &libva{}
	purego.RegisterLibFunc(&l.getDisplayDRM, drm, "vaGetDisplayDRM")
	for symbol, fn := range map[string]any{
		"vaInitialize":             &l.initialize,
		"vaTerminate":              &l.terminate,
		"vaErrorStr":               &l.errorStr,
		"vaQueryVendorString":      &l.queryVendorString,
		"vaMaxNumProfiles":         &l.maxNumProfiles,
		"vaMaxNumEntrypoints":      &l.maxNumEntrypoints,
		"vaQueryConfigProfiles":    &l.queryConfigProfiles,
		"vaQueryConfigEntrypoints": &l.queryConfigEntrypoints,
		"vaGetConfigAttributes":    &l.getConfigAttributes,
		"vaCreateConfig":           &l.createConfig,
		"vaDestroyConfig":          &l.destroyConfig,
		"vaQuerySurfaceAttributes": &l.querySurfaceAttributes,
	} {
		purego.RegisterLibFunc(fn, va, symbol)
	}
	return l, nil
}

// queryRenderNode reads the report of the driver behind one render
// node. libva chooses the driver from the kernel driver of the node, as
// every VA-API program does.
func queryRenderNode(path string) (report, error) {
	l, err := openLibva()
	if err != nil {
		return report{}, err
	}
	node, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return report{}, err
	}
	defer node.Close()
	display := l.getDisplayDRM(int32(node.Fd()))
	if display == 0 {
		return report{}, fmt.Errorf("vaGetDisplayDRM returned no display for %s", path)
	}
	var major, minor int32
	if status := l.initialize(display, &major, &minor); status != vaStatusSuccess {
		return report{}, fmt.Errorf("vaInitialize on %s: %s", path, l.errorStr(status))
	}
	defer l.terminate(display)
	return l.report(display)
}

// report reads the driver's statements from an initialized display.
func (l *libva) report(display uintptr) (report, error) {
	facts := report{Vendor: l.queryVendorString(display)}
	configs, err := l.configs(display)
	if err != nil {
		return report{}, err
	}
	facts.Configs = configs
	formats, err := l.videoProcFormats(display, configs)
	if err != nil {
		return report{}, err
	}
	facts.VideoProcFormats = formats
	return facts, nil
}

// configs lists every profile and entrypoint pair the driver states,
// with the render-target formats of each.
func (l *libva) configs(display uintptr) ([]config, error) {
	profiles := make([]int32, max(l.maxNumProfiles(display), 1))
	var profileCount int32
	if status := l.queryConfigProfiles(display, &profiles[0], &profileCount); status != vaStatusSuccess {
		return nil, fmt.Errorf("vaQueryConfigProfiles: %s", l.errorStr(status))
	}
	entrypoints := make([]int32, max(l.maxNumEntrypoints(display), 1))
	var out []config
	for _, profile := range profiles[:profileCount] {
		var entrypointCount int32
		if status := l.queryConfigEntrypoints(display, profile, &entrypoints[0], &entrypointCount); status != vaStatusSuccess {
			return nil, fmt.Errorf("vaQueryConfigEntrypoints for profile %d: %s", profile, l.errorStr(status))
		}
		for _, entrypoint := range entrypoints[:entrypointCount] {
			attrib := configAttrib{Type: configAttribRTFormat}
			if status := l.getConfigAttributes(display, profile, entrypoint, &attrib, 1); status != vaStatusSuccess {
				return nil, fmt.Errorf("vaGetConfigAttributes for profile %d, entrypoint %d: %s",
					profile, entrypoint, l.errorStr(status))
			}
			format := attrib.Value
			if format == vaAttribNotSupported {
				format = 0
			}
			out = append(out, config{Profile: profile, Entrypoint: entrypoint, RTFormat: format})
		}
	}
	return out, nil
}

// videoProcFormats lists the surface formats of the video processor's
// config. A driver with no video processor lists no VideoProc
// entrypoint, and has no formats.
func (l *libva) videoProcFormats(display uintptr, configs []config) ([]string, error) {
	listed := false
	for _, c := range configs {
		listed = listed || (c.Profile == profileNone && c.Entrypoint == entrypointVideoProc)
	}
	if !listed {
		return nil, nil
	}
	var id uint32
	if status := l.createConfig(display, profileNone, entrypointVideoProc, nil, 0, &id); status != vaStatusSuccess {
		return nil, fmt.Errorf("vaCreateConfig for the video processor: %s", l.errorStr(status))
	}
	defer l.destroyConfig(display, id)
	// The first call with no array returns the count, and the second
	// fills an array of that length.
	var count uint32
	if status := l.querySurfaceAttributes(display, id, nil, &count); status != vaStatusSuccess {
		return nil, fmt.Errorf("vaQuerySurfaceAttributes: %s", l.errorStr(status))
	}
	if count == 0 {
		return nil, nil
	}
	attribs := make([]surfaceAttrib, count)
	if status := l.querySurfaceAttributes(display, id, &attribs[0], &count); status != vaStatusSuccess {
		return nil, fmt.Errorf("vaQuerySurfaceAttributes: %s", l.errorStr(status))
	}
	return pixelFormats(attribs[:count]), nil
}

// pixelFormats reads the FourCC codes out of a list of surface
// attributes, once each, in the order the driver gave them.
func pixelFormats(attribs []surfaceAttrib) []string {
	var out []string
	seen := map[string]bool{}
	for _, attrib := range attribs {
		if attrib.Type != surfaceAttribPixelFormat || attrib.ValueType != genericValueTypeInteger {
			continue
		}
		code := fourcc(uint32(attrib.Value))
		if !seen[code] {
			seen[code] = true
			out = append(out, code)
		}
	}
	return out
}

// fourcc spells a FourCC code, which libva stores as four characters
// in little-endian order: NV12 is 0x3231564e.
func fourcc(code uint32) string {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], code)
	return string(b[:])
}
