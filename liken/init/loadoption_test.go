package main

import (
	"bytes"
	"strings"
	"testing"
)

// specFixture is an EFI_LOAD_OPTION assembled by hand from the UEFI
// specification. It has the structure of a typical "ubuntu" entry:
// active, named, pointing at a file on one GPT partition, with no
// optional data. It is not built with this package's encoder, so the
// tests check the decoder and the encoder against the specification
// and not against each other.
func specFixture() []byte {
	var b bytes.Buffer
	b.Write([]byte{0x01, 0x00, 0x00, 0x00}) // attributes: LOAD_OPTION_ACTIVE
	b.Write([]byte{0x62, 0x00})             // device path list: 98 bytes (42 + 52 + 4)
	// Description: "ubuntu" in UTF-16LE, NUL-terminated.
	b.Write([]byte{'u', 0, 'b', 0, 'u', 0, 'n', 0, 't', 0, 'u', 0, 0, 0})
	// Hard-drive node: type 4 (media), subtype 1, length 42.
	b.Write([]byte{0x04, 0x01, 0x2A, 0x00})
	b.Write([]byte{0x01, 0x00, 0x00, 0x00})          // partition number 1
	b.Write([]byte{0x00, 0x08, 0, 0, 0, 0, 0, 0})    // first LBA 0x800
	b.Write([]byte{0x00, 0x00, 0x10, 0, 0, 0, 0, 0}) // 0x100000 sectors
	b.Write(bytes.Repeat([]byte{0xAB}, 16))          // partition GUID
	b.Write([]byte{0x02, 0x02})                      // MBRType GPT, signature is a GUID
	// File-path node: type 4, subtype 4, length 52: "\EFI\ubuntu\shimx64.efi"
	b.Write([]byte{0x04, 0x04, 0x34, 0x00})
	for _, r := range `\EFI\ubuntu\shimx64.efi` {
		b.WriteByte(byte(r))
		b.WriteByte(0)
	}
	b.Write([]byte{0, 0})                   // path terminator
	b.Write([]byte{0x7F, 0xFF, 0x04, 0x00}) // end-of-device-path node
	return b.Bytes()
}

func TestParseLoadOptionAgainstTheSpecification(t *testing.T) {
	o, err := parseLoadOption(specFixture())
	if err != nil {
		t.Fatalf("parsing the load option: %v", err)
	}
	if o.attributes != loadOptionActive {
		t.Errorf("the attributes are %#x, want %#x", o.attributes, loadOptionActive)
	}
	if o.description != "ubuntu" {
		t.Errorf("the description is %q, want %q", o.description, "ubuntu")
	}
	if o.hardDrive == nil {
		t.Fatal("the load option has no hard-drive node, want one")
	}
	if o.hardDrive.partitionNumber != 1 || o.hardDrive.firstLBA != 0x800 || o.hardDrive.sectors != 0x100000 {
		t.Errorf("the hard-drive node is %+v, want partition 1, first LBA 0x800, 0x100000 sectors", o.hardDrive)
	}
	if o.hardDrive.partitionGUID != [16]byte(bytes.Repeat([]byte{0xAB}, 16)) {
		t.Errorf("the partition GUID is % X, want 16 bytes of AB", o.hardDrive.partitionGUID)
	}
	if o.filePath != `\EFI\ubuntu\shimx64.efi` {
		t.Errorf("the file path is %q, want %q", o.filePath, `\EFI\ubuntu\shimx64.efi`)
	}
	if len(o.optionalData) != 0 {
		t.Errorf("the optional data is % X, want none", o.optionalData)
	}
}

func TestEncodeLoadOptionAgainstTheSpecification(t *testing.T) {
	// The round trip below passes even when the encoder and the
	// decoder have the same bug, so this test compares the encoder's
	// output with the fixture byte for byte.
	o := loadOption{
		attributes:  loadOptionActive,
		description: "ubuntu",
		hardDrive: &hardDriveNode{
			partitionNumber: 1,
			firstLBA:        0x800,
			sectors:         0x100000,
			partitionGUID:   [16]byte(bytes.Repeat([]byte{0xAB}, 16)),
		},
		filePath: `\EFI\ubuntu\shimx64.efi`,
	}
	if got, want := encodeLoadOption(o), specFixture(); !bytes.Equal(got, want) {
		t.Errorf("the encoded bytes are\n% X\nwant\n% X", got, want)
	}
}

func TestLoadOptionRoundTrip(t *testing.T) {
	original := loadOption{
		attributes:  loadOptionActive,
		description: "liken slot A",
		hardDrive: &hardDriveNode{
			partitionNumber: 1,
			firstLBA:        2_048,
			sectors:         1_048_576,
			partitionGUID:   [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		},
		filePath:     `\vmlinuz`,
		optionalData: []byte("console=ttyS0 liken.slot=A"),
	}
	decoded, err := parseLoadOption(encodeLoadOption(original))
	if err != nil {
		t.Fatalf("parsing the load option: %v", err)
	}
	if decoded.description != original.description ||
		decoded.filePath != original.filePath ||
		*decoded.hardDrive != *original.hardDrive ||
		!bytes.Equal(decoded.optionalData, original.optionalData) {
		t.Errorf("the decoded option is %+v, want %+v", decoded, original)
	}
}

func TestParseLoadOptionRejectsTruncation(t *testing.T) {
	fixture := specFixture()
	for _, cut := range []int{0, 3, 5, 8, 19, 30, len(fixture) - 5} {
		if _, err := parseLoadOption(fixture[:cut]); err == nil {
			t.Errorf("parsing the first %d bytes returned no error, want one", cut)
		}
	}
}

func TestParseLoadOptionRejectsUnterminatedDescription(t *testing.T) {
	// The description has no NUL terminator, so the parser must stop
	// at the end of the input and return an error.
	b := []byte{0x01, 0, 0, 0, 0x04, 0, 'h', 0, 'i', 0}
	if _, err := parseLoadOption(b); err == nil {
		t.Fatal("parsing a description with no terminator returned no error, want one")
	}
}

func TestParseLoadOptionSkipsUnknownNodes(t *testing.T) {
	var b bytes.Buffer
	b.Write([]byte{0x01, 0, 0, 0})
	b.Write([]byte{14, 0})                                    // device path list: one 10-byte node + end
	b.Write([]byte{0, 0})                                     // empty description
	b.Write([]byte{0x01, 0x17, 0x0A, 0x00, 1, 2, 3, 4, 5, 6}) // hardware node of an undefined subtype
	b.Write([]byte{0x7F, 0xFF, 0x04, 0x00})
	o, err := parseLoadOption(b.Bytes())
	if err != nil {
		t.Fatalf("parsing the load option: %v", err)
	}
	if o.hardDrive != nil || o.filePath != "" {
		t.Errorf("the option has a hard drive or a file path, %+v, want neither", o)
	}
}

func TestBootEntryIDIsBootAndFourUppercaseHexDigits(t *testing.T) {
	if got := bootEntryID(0); got != "Boot0000" {
		t.Errorf("bootEntryID is %q, want %q", got, "Boot0000")
	}
	if got := bootEntryID(0x2001); got != "Boot2001" {
		t.Errorf("bootEntryID is %q, want %q", got, "Boot2001")
	}
	if !strings.HasPrefix(bootEntryID(0xABC), "Boot0ABC") {
		t.Errorf("bootEntryID(0xABC) is %q, want a prefix of Boot0ABC", bootEntryID(0xABC))
	}
}
