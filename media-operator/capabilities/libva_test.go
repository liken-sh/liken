package main

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"unsafe"
)

// The query passes surfaceAttrib to libva by address, so its layout
// must be VASurfaceAttrib's on a 64-bit machine.
func TestASurfaceAttributeHasLibvasLayout(t *testing.T) {
	if size := unsafe.Sizeof(surfaceAttrib{}); size != 24 {
		t.Errorf("surfaceAttrib is %d bytes, want 24", size)
	}
	if offset := unsafe.Offsetof(surfaceAttrib{}.Value); offset != 16 {
		t.Errorf("the value starts at byte %d, want 16", offset)
	}
}

func TestPixelFormatsReadsEachFourCCOnce(t *testing.T) {
	got := pixelFormats([]surfaceAttrib{
		{Type: surfaceAttribPixelFormat, ValueType: genericValueTypeInteger, Value: 0x3231564e},
		{Type: surfaceAttribPixelFormat, ValueType: genericValueTypeInteger, Value: 0x30313050},
		{Type: surfaceAttribPixelFormat, ValueType: genericValueTypeInteger, Value: 0x3231564e},
		// The maximum width is an integer too, and it is not a format.
		{Type: 3, ValueType: genericValueTypeInteger, Value: 16384},
	})
	if want := []string{"NV12", "P010"}; !slices.Equal(got, want) {
		t.Errorf("formats = %v, want %v", got, want)
	}
}

// A file that is not a render node fails in libva-drm, and the error
// names the file. The test needs libva.so.2 and libva-drm.so.2 on the
// machine, which the CI job installs with ffmpeg.
func TestTheQueryOfAFileThatIsNotARenderNodeFails(t *testing.T) {
	_, err := queryRenderNode("/dev/null")
	if err == nil || !strings.Contains(err.Error(), "/dev/null") {
		t.Errorf("err = %v, want libva-drm's refusal of /dev/null", err)
	}
}

// fakeDriver stands in for a VA-API driver behind libva's functions, so
// a test walks the same calls the query sends to a GPU.
type fakeDriver struct {
	entrypoints map[int32][]int32
	rtFormats   map[[2]int32]uint32
	formats     []uint32
	// failing names the one libva function that answers an error.
	failing string
}

func (d *fakeDriver) libva() *libva {
	status := func(name string) int32 {
		if d.failing == name {
			return 1
		}
		return vaStatusSuccess
	}
	var profiles []int32
	for profile := range d.entrypoints {
		profiles = append(profiles, profile)
	}
	slices.Sort(profiles)
	return &libva{
		errorStr:          func(code int32) string { return "operation failed" },
		queryVendorString: func(uintptr) string { return "Fake VA driver 1.0" },
		maxNumProfiles:    func(uintptr) int32 { return int32(len(profiles)) },
		maxNumEntrypoints: func(uintptr) int32 { return 16 },
		queryConfigProfiles: func(_ uintptr, out *int32, count *int32) int32 {
			*count = int32(copy(unsafe.Slice(out, len(profiles)), profiles))
			return status("vaQueryConfigProfiles")
		},
		queryConfigEntrypoints: func(_ uintptr, profile int32, out *int32, count *int32) int32 {
			*count = int32(copy(unsafe.Slice(out, 16), d.entrypoints[profile]))
			return status("vaQueryConfigEntrypoints")
		},
		getConfigAttributes: func(_ uintptr, profile, entrypoint int32, attrib *configAttrib, _ int32) int32 {
			attrib.Value = vaAttribNotSupported
			if format, ok := d.rtFormats[[2]int32{profile, entrypoint}]; ok {
				attrib.Value = format
			}
			return status("vaGetConfigAttributes")
		},
		createConfig: func(_ uintptr, _, _ int32, _ *configAttrib, _ int32, id *uint32) int32 {
			*id = 7
			return status("vaCreateConfig")
		},
		destroyConfig: func(uintptr, uint32) int32 { return vaStatusSuccess },
		querySurfaceAttributes: func(_ uintptr, _ uint32, out *surfaceAttrib, count *uint32) int32 {
			if out != nil {
				for i, format := range d.formats {
					unsafe.Slice(out, len(d.formats))[i] = surfaceAttrib{
						Type: surfaceAttribPixelFormat, ValueType: genericValueTypeInteger, Value: uint64(format),
					}
				}
			}
			*count = uint32(len(d.formats))
			return status("vaQuerySurfaceAttributes")
		},
	}
}

// tenBitDriver lists HEVC Main 10 decode, AV1 decode at both depths,
// and a video processor that takes NV12 and P010.
func tenBitDriver() *fakeDriver {
	return &fakeDriver{
		entrypoints: map[int32][]int32{
			profileNone:        {entrypointVideoProc},
			profileHEVCMain10:  {entrypointVLD},
			profileAV1Profile0: {entrypointVLD},
		},
		rtFormats: map[[2]int32]uint32{
			{profileAV1Profile0, entrypointVLD}: rtFormatYUV420 | rtFormatYUV420_10,
		},
		formats: []uint32{0x3231564e, 0x30313050},
	}
}

func TestTheQueryReadsEachStatementOfTheDriver(t *testing.T) {
	got, err := tenBitDriver().libva().report(1)
	if err != nil {
		t.Fatal(err)
	}
	want := report{
		Vendor: "Fake VA driver 1.0",
		Configs: []config{
			{Profile: profileNone, Entrypoint: entrypointVideoProc},
			{Profile: profileHEVCMain10, Entrypoint: entrypointVLD},
			{Profile: profileAV1Profile0, Entrypoint: entrypointVLD, RTFormat: rtFormatYUV420 | rtFormatYUV420_10},
		},
		VideoProcFormats: []string{"NV12", "P010"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("report = %+v, want %+v", got, want)
	}
}

// A driver with no VideoProc entrypoint has no video processor, so the
// query creates no config for it and states no format.
func TestADriverWithNoVideoProcessorStatesNoFormat(t *testing.T) {
	driver := tenBitDriver()
	delete(driver.entrypoints, profileNone)
	got, err := driver.libva().report(1)
	if err != nil || got.VideoProcFormats != nil {
		t.Errorf("formats = %v, err = %v", got.VideoProcFormats, err)
	}
}

func TestAFailedLibvaCallFailsTheQueryWithLibvasText(t *testing.T) {
	for _, call := range []string{
		"vaQueryConfigProfiles", "vaQueryConfigEntrypoints", "vaGetConfigAttributes",
		"vaCreateConfig", "vaQuerySurfaceAttributes",
	} {
		t.Run(call, func(t *testing.T) {
			driver := tenBitDriver()
			driver.failing = call
			_, err := driver.libva().report(1)
			if err == nil || !strings.Contains(err.Error(), call) || !strings.Contains(err.Error(), "operation failed") {
				t.Errorf("err = %v, want the call and libva's text", err)
			}
		})
	}
}
