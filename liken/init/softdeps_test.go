package main

// Tests for the soft-dependency reader: the .modinfo strings that name
// a module's "pre" dependencies, and the walk that turns one driver
// name into the full list a manifest declares.

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// modinfoBytes joins strings the way a real .modinfo section holds
// them: NUL-separated, with a trailing NUL.
func modinfoBytes(entries ...string) []byte {
	return []byte(strings.Join(entries, "\x00") + "\x00")
}

// modinfoELF builds a minimal ELF64 object that carries the given
// bytes in a .modinfo section, so a test can exercise the section
// reader and the whole soft-dependency walk without a real compiled
// module. The object holds only what debug/elf must find: the header,
// the .modinfo section, and the section-name string table.
func modinfoELF(t *testing.T, modinfo []byte) []byte {
	t.Helper()
	// The string table names the sections. Its first byte is a NUL, so
	// name offset 1 is ".modinfo" and offset 10 is ".shstrtab".
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
		t.Errorf("got %v", got)
	}
}

func TestParseSoftdepPreDropsPostNames(t *testing.T) {
	// A module can name one module before it and another after it. Only
	// the "pre" names change the probe outcome, so only they survive.
	modinfo := modinfoBytes("softdep=pre: a b post: c")
	if got := parseSoftdepPre(modinfo); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("got %v", got)
	}
}

func TestParseSoftdepPreGathersEveryLine(t *testing.T) {
	modinfo := modinfoBytes("softdep=pre: a", "softdep=pre: b")
	if got := parseSoftdepPre(modinfo); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("got %v", got)
	}
}

func TestParseSoftdepPreFindsNothingWhenAbsent(t *testing.T) {
	if got := parseSoftdepPre(modinfoBytes("license=GPL")); got != nil {
		t.Errorf("no softdep means no names: %v", got)
	}
}

func TestExpandSoftdepsOrdersPreDepsThenSelf(t *testing.T) {
	pre := func(name string) []string {
		if name == "r8169" {
			return []string{"realtek"}
		}
		return nil
	}
	if got := expandSoftdeps("r8169", pre); !slices.Equal(got, []string{"realtek", "r8169"}) {
		t.Errorf("got %v", got)
	}
}

func TestExpandSoftdepsRecursesThroughSoftdeps(t *testing.T) {
	// A soft dependency can carry its own, so the walk goes deep and
	// still lists every name before the one that wanted it.
	pre := func(name string) []string {
		return map[string][]string{"a": {"b"}, "b": {"c"}}[name]
	}
	if got := expandSoftdeps("a", pre); !slices.Equal(got, []string{"c", "b", "a"}) {
		t.Errorf("got %v", got)
	}
}

func TestExpandSoftdepsGuardsAgainstCycles(t *testing.T) {
	// Soft dependencies are only a hint, so two modules can name each
	// other. The walk must terminate and place each name once.
	pre := func(name string) []string {
		return map[string][]string{"a": {"b"}, "b": {"a"}}[name]
	}
	if got := expandSoftdeps("a", pre); !slices.Equal(got, []string{"b", "a"}) {
		t.Errorf("got %v", got)
	}
}

func TestElfSectionReadsANamedSection(t *testing.T) {
	image := modinfoELF(t, modinfoBytes("softdep=pre: realtek"))
	data, err := elfSection(image, ".modinfo")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(parseSoftdepPre(data), []string{"realtek"}) {
		t.Errorf("section bytes did not round-trip: %q", data)
	}
}

func TestReadModinfoDecompressesAZstModule(t *testing.T) {
	// The runtime tree holds .ko.zst files, so the reader must undo the
	// zstd frame the kernel would otherwise decompress itself.
	image := modinfoELF(t, modinfoBytes("softdep=pre: realtek"))
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	path := writeBytes(t, "r8169.ko.zst", encoder.EncodeAll(image, nil))
	encoder.Close()
	modinfo, err := readModinfo(path)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(parseSoftdepPre(modinfo), []string{"realtek"}) {
		t.Errorf("got %q", modinfo)
	}
}

// softdepTree writes a module tree the soft-dependency walk can read:
// a modules.dep that names each module's file, and the .ko files
// themselves, each carrying its own .modinfo.
func softdepTree(t *testing.T, modinfos map[string][]byte) string {
	t.Helper()
	base := t.TempDir()
	var dep strings.Builder
	for path, modinfo := range modinfos {
		full := filepath.Join(base, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, modinfoELF(t, modinfo), 0o644); err != nil {
			t.Fatal(err)
		}
		dep.WriteString(path + ":\n")
	}
	if err := os.WriteFile(filepath.Join(base, "modules.dep"), []byte(dep.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return base
}

func TestSoftdepChainWalksAFabricatedTree(t *testing.T) {
	base := softdepTree(t, map[string][]byte{
		"kernel/drivers/net/ethernet/realtek/r8169.ko": modinfoBytes("softdep=pre: realtek"),
		"kernel/drivers/net/phy/realtek.ko":            modinfoBytes("license=GPL"),
	})
	if got := softdepChain(base, "r8169"); !slices.Equal(got, []string{"realtek", "r8169"}) {
		t.Errorf("got %v", got)
	}
}

func TestSoftdepChainReturnsTheNameAloneWithoutAnIndex(t *testing.T) {
	if got := softdepChain(t.TempDir(), "r8169"); !slices.Equal(got, []string{"r8169"}) {
		t.Errorf("a tree with no index resolves to the name alone: %v", got)
	}
}

func TestDriverChainAddsTheCompanionABusDriverNeeds(t *testing.T) {
	// hid-generic binds the HID device that usbhid creates, so no
	// modalias and no soft dependency ever names it. A recommendation
	// that stops at usbhid leaves a keyboard that still types nothing.
	if got := driverChain(t.TempDir(), "usbhid"); !slices.Equal(got, []string{"usbhid", "hid_generic"}) {
		t.Errorf("got %v", got)
	}
}

func TestDriverChainLeavesAnOrdinaryDriverAlone(t *testing.T) {
	base := softdepTree(t, map[string][]byte{
		"kernel/drivers/net/ethernet/realtek/r8169.ko": modinfoBytes("softdep=pre: realtek"),
		"kernel/drivers/net/phy/realtek.ko":            modinfoBytes("license=GPL"),
	})
	if got := driverChain(base, "r8169"); !slices.Equal(got, []string{"realtek", "r8169"}) {
		t.Errorf("got %v", got)
	}
}
