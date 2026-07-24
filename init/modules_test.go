package main

// Tests for the module index parsing: the file formats depmod leaves
// behind. Actually loading modules into a kernel is QEMU territory.

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/liken-sh/liken/machine"
)

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeBytes(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadModuleListSkipsCommentsAndBlanks(t *testing.T) {
	path := writeFile(t, "modules.list", "# storage\nvirtio_blk\n\n  ext4  \n")
	names, err := readModuleList(path)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names, []string{"virtio_blk", "ext4"}) {
		t.Errorf("got %v", names)
	}
}

func TestReadModuleListToleratesAMissingFile(t *testing.T) {
	names, err := readModuleList(filepath.Join(t.TempDir(), "absent"))
	if names != nil || err != nil {
		t.Errorf("a missing list means nothing to load: %v, %v", names, err)
	}
}

func TestReadModulesDepParsesTheIndex(t *testing.T) {
	path := writeFile(t, "modules.dep",
		"kernel/fs/overlayfs/overlay.ko.zst: kernel/a.ko.zst kernel/b.ko.zst\n"+
			"kernel/drivers/block/virtio-blk.ko.zst:\n"+
			"not an entry\n")
	deps, err := readModulesDep(path)
	if err != nil {
		t.Fatal(err)
	}
	overlay := deps["overlay"]
	if !slices.Equal(overlay, []string{"kernel/fs/overlayfs/overlay.ko.zst", "kernel/a.ko.zst", "kernel/b.ko.zst"}) {
		t.Errorf("got %v", overlay)
	}
	// Module names use "_" and "-" interchangeably; the index keys
	// normalize to "_" so lookups can too.
	if _, ok := deps["virtio_blk"]; !ok {
		t.Errorf("dashes normalize to underscores: %v", deps)
	}
}

func TestModuleNameStripsExtensionsAndNormalizes(t *testing.T) {
	if got := moduleName("kernel/drivers/block/virtio-blk.ko.zst"); got != "virtio_blk" {
		t.Errorf("got %q", got)
	}
	if got := moduleName("kernel/fs/ext4.ko"); got != "ext4" {
		t.Errorf("got %q", got)
	}
}

func TestReadModulesBuiltinKeysLikeTheDepIndex(t *testing.T) {
	path := writeFile(t, "modules.builtin",
		"kernel/fs/binfmt_misc.ko\nkernel/drivers/char/hw_random/rng-core.ko\n\n")
	builtin, err := readModulesBuiltin(path)
	if err != nil {
		t.Fatal(err)
	}
	if !builtin["binfmt_misc"] {
		t.Errorf("binfmt_misc should be builtin: %v", builtin)
	}
	if !builtin["rng_core"] {
		t.Errorf("dashes normalize to underscores: %v", builtin)
	}
}

// declaredFixture is the module world a declared-modules test runs
// against: one shippable module, one builtin, and a loader that fails
// on demand, so every outcome in the vocabulary is reachable.
func declaredFixture(failing string) (map[string][]string, map[string]bool, func(string) error) {
	deps := map[string][]string{"nvidia": {"kernel/nvidia.ko.zst"}}
	builtin := map[string]bool{"loop": true}
	load := func(name string) error {
		if name == failing {
			return errors.New("finit_module: no such device")
		}
		return nil
	}
	return deps, builtin, load
}

func TestDeclaredModuleOutcomes(t *testing.T) {
	deps, builtin, load := declaredFixture("")
	statuses := declaredModuleOutcomes([]string{"nvidia", "loop", "nbd"}, deps, builtin, load)
	states := []machine.ModuleState{machine.ModuleLoaded, machine.ModuleBuiltin, machine.ModuleMissing}
	for i, want := range states {
		if statuses[i].State != want {
			t.Errorf("%s: got %s, want %s", statuses[i].Name, statuses[i].State, want)
		}
	}
	if statuses[2].Message == "" {
		t.Error("a missing module's message must name the fix")
	}
}

func TestDeclaredModuleOutcomesReportsKernelRefusals(t *testing.T) {
	deps, builtin, load := declaredFixture("nvidia")
	statuses := declaredModuleOutcomes([]string{"nvidia"}, deps, builtin, load)
	if statuses[0].State != machine.ModuleFailed {
		t.Errorf("got %s", statuses[0].State)
	}
	if statuses[0].Message != "finit_module: no such device" {
		t.Errorf("message: got %q", statuses[0].Message)
	}
}

func TestDeclaredModuleOutcomesNormalizesDashes(t *testing.T) {
	_, builtin, load := declaredFixture("")
	deps := map[string][]string{"rng_core": {"kernel/rng-core.ko.zst"}}
	statuses := declaredModuleOutcomes([]string{"rng-core"}, deps, builtin, load)
	if statuses[0].State != machine.ModuleLoaded {
		t.Errorf("got %s", statuses[0].State)
	}
}

func TestLoadDeclaredModulesFromAFabricatedTree(t *testing.T) {
	base := t.TempDir()
	deps := "kernel/drivers/gpu/nvidia.ko.zst:\n"
	if err := os.WriteFile(filepath.Join(base, "modules.dep"), []byte(deps), 0o644); err != nil {
		t.Fatal(err)
	}
	builtin := "kernel/block/loop.ko\n"
	if err := os.WriteFile(filepath.Join(base, "modules.builtin"), []byte(builtin), 0o644); err != nil {
		t.Fatal(err)
	}
	// nvidia is indexed but its file does not exist here, so the
	// load itself fails: this reaches the Failed path without a real
	// kernel involved.
	statuses := loadDeclaredModulesFrom(base, []string{"nvidia", "loop", "nbd"})
	states := []machine.ModuleState{machine.ModuleFailed, machine.ModuleBuiltin, machine.ModuleMissing}
	for i, want := range states {
		if statuses[i].State != want {
			t.Errorf("%s: got %s, want %s", statuses[i].Name, statuses[i].State, want)
		}
	}
}

func TestLoadDeclaredModulesWithNothingDeclared(t *testing.T) {
	if statuses := loadDeclaredModules(nil); statuses != nil {
		t.Errorf("nothing declared means nothing to report: %v", statuses)
	}
}

func TestLoadDeclaredModulesFromAMissingTree(t *testing.T) {
	statuses := loadDeclaredModulesFrom(filepath.Join(t.TempDir(), "absent"), []string{"nvidia"})
	if len(statuses) != 1 || statuses[0].State != machine.ModuleMissing {
		t.Errorf("an unreadable index reads as nothing shipped: %v", statuses)
	}
}

func TestKernelReleaseAsksTheKernel(t *testing.T) {
	if release := kernelRelease(); release == "" {
		t.Error("uname always has a release string")
	}
}

// fakeModulesConf points the fixed-list pass at a list of the test's
// making, restoring the real path when the test ends.
func fakeModulesConf(t *testing.T, path string) {
	t.Helper()
	old := modulesConf
	modulesConf = path
	t.Cleanup(func() { modulesConf = old })
}

func TestLoadModulesWithNoListLoadsNothing(t *testing.T) {
	fakeModulesConf(t, filepath.Join(t.TempDir(), "absent.conf"))
	loadModules()
}

func TestLoadModulesWithAnEmptyListLoadsNothing(t *testing.T) {
	fakeModulesConf(t, writeFile(t, "modules.conf", "# nothing but commentary\n"))
	loadModules()
}

func TestLoadModulesReportsAnUnreadableList(t *testing.T) {
	path := writeFile(t, "modules.conf", "overlay\n")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	fakeModulesConf(t, path)
	loadModules()
}

func TestLoadModuleReportsAMissingModuleFile(t *testing.T) {
	// The index promises a file that the tree does not hold. The open
	// fails as an ordinary error, and nothing counts as loaded.
	base := t.TempDir()
	deps := map[string][]string{"overlay": {"kernel/fs/overlayfs/overlay.ko.zst"}}
	n, err := loadModule(base, "overlay", deps, map[string]bool{})
	if err == nil || n != 0 {
		t.Errorf("a missing file is an error and loads nothing: %d, %v", n, err)
	}
}

func TestLoadModuleReportsAKernelRefusal(t *testing.T) {
	// The file exists, but the kernel refuses it (here, because an
	// ordinary process may not load modules; on a real boot, because
	// the bytes are not a module). Either way, this is the same
	// branch: finit_module's error, wrapped with the file's name.
	base := t.TempDir()
	rel := "kernel/fake.ko.zst"
	if err := os.MkdirAll(filepath.Join(base, "kernel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, rel), []byte("not a module"), 0o644); err != nil {
		t.Fatal(err)
	}
	deps := map[string][]string{"fake": {rel}}
	n, err := loadModule(base, "fake", deps, map[string]bool{})
	if err == nil || !strings.Contains(err.Error(), "finit_module") || n != 0 {
		t.Errorf("expected the kernel's refusal to surface: %d, %v", n, err)
	}
}

func TestLoadModuleSkipsFilesAlreadyLoaded(t *testing.T) {
	// A dependency that another chain already fed to the kernel is
	// skipped entirely: no open, no syscall, no count.
	deps := map[string][]string{"overlay": {"kernel/fs/overlayfs/overlay.ko.zst"}}
	loaded := map[string]bool{"kernel/fs/overlayfs/overlay.ko.zst": true}
	n, err := loadModule(t.TempDir(), "overlay", deps, loaded)
	if err != nil || n != 0 {
		t.Errorf("an already-loaded chain is a no-op: %d, %v", n, err)
	}
}

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
	// A module can want one module before it and another after it. Only
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
