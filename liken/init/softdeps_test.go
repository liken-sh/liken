package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// modinfoBytes joins strings as a .modinfo section does: separated by
// NUL, with a NUL at the end.
func modinfoBytes(entries ...string) []byte {
	return []byte(strings.Join(entries, "\x00") + "\x00")
}

// modinfoELF returns the smallest ELF64 object that debug/elf reads,
// with modinfo as its .modinfo section, so that tests need no compiled
// kernel module.
func modinfoELF(t *testing.T, modinfo []byte) []byte {
	t.Helper()
	// The first byte of the string table is NUL, so name offset 1 is
	// ".modinfo" and offset 10 is ".shstrtab".
	shstrtab := []byte("\x00.modinfo\x00.shstrtab\x00")
	modinfoOff := 64
	shstrtabOff := modinfoOff + len(modinfo)
	shoff := shstrtabOff + len(shstrtab)
	image := make([]byte, shoff+3*64)
	le := binary.LittleEndian

	copy(image, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0})
	le.PutUint16(image[16:], uint16(elfET_REL))
	le.PutUint16(image[18:], uint16(elfEM_X86_64))
	le.PutUint32(image[20:], 1)
	le.PutUint64(image[40:], uint64(shoff))
	le.PutUint16(image[52:], 64)
	le.PutUint16(image[58:], 64)
	le.PutUint16(image[60:], 3)
	le.PutUint16(image[62:], 2)

	copy(image[modinfoOff:], modinfo)
	copy(image[shstrtabOff:], shstrtab)

	modinfoHdr := shoff + 64
	le.PutUint32(image[modinfoHdr:], 1)
	le.PutUint32(image[modinfoHdr+4:], elfSHT_PROGBITS)
	le.PutUint64(image[modinfoHdr+24:], uint64(modinfoOff))
	le.PutUint64(image[modinfoHdr+32:], uint64(len(modinfo)))

	shstrtabHdr := shoff + 128
	le.PutUint32(image[shstrtabHdr:], 10)
	le.PutUint32(image[shstrtabHdr+4:], elfSHT_STRTAB)
	le.PutUint64(image[shstrtabHdr+24:], uint64(shstrtabOff))
	le.PutUint64(image[shstrtabHdr+32:], uint64(len(shstrtab)))
	return image
}

const (
	elfET_REL       = 1
	elfEM_X86_64    = 62
	elfSHT_PROGBITS = 1
	elfSHT_STRTAB   = 3
)

func TestParseSoftdepPreReadsPreNamesInOrder(t *testing.T) {
	modinfo := modinfoBytes("license=GPL", "softdep=pre: realtek", "author=someone")
	if got := parseSoftdepPre(modinfo); !slices.Equal(got, []string{"realtek"}) {
		t.Errorf("result is %v, want [realtek]", got)
	}
}

func TestParseSoftdepPreDropsPostNames(t *testing.T) {
	modinfo := modinfoBytes("softdep=pre: a b post: c")
	if got := parseSoftdepPre(modinfo); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("result is %v, want [a b]", got)
	}
}

func TestParseSoftdepPreGathersEveryLine(t *testing.T) {
	modinfo := modinfoBytes("softdep=pre: a", "softdep=pre: b")
	if got := parseSoftdepPre(modinfo); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("result is %v, want [a b]", got)
	}
}

func TestParseSoftdepPreReturnsNilWithoutASoftdepEntry(t *testing.T) {
	if got := parseSoftdepPre(modinfoBytes("license=GPL")); got != nil {
		t.Errorf("names are %v, want none without a softdep entry", got)
	}
}

func TestExpandSoftdepsPutsDependenciesBeforeTheModule(t *testing.T) {
	pre := func(name string) []string {
		if name == "r8169" {
			return []string{"realtek"}
		}
		return nil
	}
	if got := expandSoftdeps("r8169", pre); !slices.Equal(got, []string{"realtek", "r8169"}) {
		t.Errorf("result is %v, want [realtek r8169]", got)
	}
}

func TestExpandSoftdepsResolvesDependenciesOfDependencies(t *testing.T) {
	pre := func(name string) []string {
		return map[string][]string{"a": {"b"}, "b": {"c"}}[name]
	}
	if got := expandSoftdeps("a", pre); !slices.Equal(got, []string{"c", "b", "a"}) {
		t.Errorf("result is %v, want [c b a]", got)
	}
}

func TestExpandSoftdepsTerminatesOnModulesThatNameEachOther(t *testing.T) {
	pre := func(name string) []string {
		return map[string][]string{"a": {"b"}, "b": {"a"}}[name]
	}
	if got := expandSoftdeps("a", pre); !slices.Equal(got, []string{"b", "a"}) {
		t.Errorf("result is %v, want [b a]", got)
	}
}

func TestElfSectionReturnsTheNamedSection(t *testing.T) {
	image := modinfoELF(t, modinfoBytes("softdep=pre: realtek"))
	data, err := elfSection(image, ".modinfo")
	if err != nil {
		t.Fatalf("reading the .modinfo section: %v", err)
	}
	if !slices.Equal(parseSoftdepPre(data), []string{"realtek"}) {
		t.Errorf("parsing the section returned the wrong names from %q, want realtek", data)
	}
}

func TestReadModinfoDecompressesAZstModule(t *testing.T) {
	image := modinfoELF(t, modinfoBytes("softdep=pre: realtek"))
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatalf("creating the zstd encoder: %v", err)
	}
	path := writeBytes(t, "r8169.ko.zst", encoder.EncodeAll(image, nil))
	encoder.Close()
	modinfo, err := readModinfo(path)
	if err != nil {
		t.Fatalf("reading the module info: %v", err)
	}
	if !slices.Equal(parseSoftdepPre(modinfo), []string{"realtek"}) {
		t.Errorf("pre names are %q, want realtek", modinfo)
	}
}

// softdepTree writes a module tree of .ko files and a modules.dep that
// lists them, and returns its root.
func softdepTree(t *testing.T, modinfos map[string][]byte) string {
	t.Helper()
	base := t.TempDir()
	var dep strings.Builder
	for path, modinfo := range modinfos {
		full := filepath.Join(base, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("creating the module directory: %v", err)
		}
		if err := os.WriteFile(full, modinfoELF(t, modinfo), 0o644); err != nil {
			t.Fatalf("writing the module file: %v", err)
		}
		dep.WriteString(path + ":\n")
	}
	if err := os.WriteFile(filepath.Join(base, "modules.dep"), []byte(dep.String()), 0o644); err != nil {
		t.Fatalf("writing modules.dep: %v", err)
	}
	return base
}

func TestSoftdepChainReadsModinfoFromTheModuleTree(t *testing.T) {
	base := softdepTree(t, map[string][]byte{
		"kernel/drivers/net/ethernet/realtek/r8169.ko": modinfoBytes("softdep=pre: realtek"),
		"kernel/drivers/net/phy/realtek.ko":            modinfoBytes("license=GPL"),
	})
	if got := softdepChain(base, "r8169"); !slices.Equal(got, []string{"realtek", "r8169"}) {
		t.Errorf("result is %v, want [realtek r8169]", got)
	}
}

func TestSoftdepChainReturnsOnlyTheNameWithoutModulesDep(t *testing.T) {
	if got := softdepChain(t.TempDir(), "r8169"); !slices.Equal(got, []string{"r8169"}) {
		t.Errorf("chain is %v, want r8169 alone without modules.dep", got)
	}
}

func TestDriverChainAppendsTheBusCompanionOfAModule(t *testing.T) {
	// hid-generic binds the HID device that usbhid creates, and no
	// modalias or soft dependency names it.
	if got := driverChain(t.TempDir(), "usbhid"); !slices.Equal(got, []string{"usbhid", "hid_generic"}) {
		t.Errorf("result is %v, want [usbhid hid_generic]", got)
	}
}

func TestDriverChainAddsNoCompanionToAModuleWithoutOne(t *testing.T) {
	base := softdepTree(t, map[string][]byte{
		"kernel/drivers/net/ethernet/realtek/r8169.ko": modinfoBytes("softdep=pre: realtek"),
		"kernel/drivers/net/phy/realtek.ko":            modinfoBytes("license=GPL"),
	})
	if got := driverChain(base, "r8169"); !slices.Equal(got, []string{"realtek", "r8169"}) {
		t.Errorf("result is %v, want [realtek r8169]", got)
	}
}
