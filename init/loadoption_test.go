package main

import (
	"bytes"
	"strings"
	"testing"
)

// A hand-assembled EFI_LOAD_OPTION, byte for byte from the UEFI
// specification, mirroring the structure of a real "ubuntu" entry
// read from a laptop's firmware: active, named, pointing at a file
// on one GPT partition, no optional data. Building it by hand (not
// with our encoder) is the point: the decoder is tested against the
// specification, not against our own reflection.
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
		t.Fatal(err)
	}
	if o.attributes != loadOptionActive {
		t.Errorf("attributes: got %#x", o.attributes)
	}
	if o.description != "ubuntu" {
		t.Errorf("description: got %q", o.description)
	}
	if o.hardDrive == nil {
		t.Fatal("the hard-drive node should be decoded")
	}
	if o.hardDrive.partitionNumber != 1 || o.hardDrive.firstLBA != 0x800 || o.hardDrive.sectors != 0x100000 {
		t.Errorf("hard-drive geometry: %+v", o.hardDrive)
	}
	if o.hardDrive.partitionGUID != [16]byte(bytes.Repeat([]byte{0xAB}, 16)) {
		t.Errorf("partition GUID: % X", o.hardDrive.partitionGUID)
	}
	if o.filePath != `\EFI\ubuntu\shimx64.efi` {
		t.Errorf("file path: got %q", o.filePath)
	}
	if len(o.optionalData) != 0 {
		t.Errorf("optional data: got % X, want none", o.optionalData)
	}
}

func TestEncodeLoadOptionAgainstTheSpecification(t *testing.T) {
	// The encoder must produce the hand-assembled fixture byte for
	// byte — a much stronger check than the round trip below, which
	// a self-consistent pair of bugs could pass.
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
		t.Errorf("encoded bytes differ from the specification fixture:\n got % X\nwant % X", got, want)
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
		t.Fatal(err)
	}
	if decoded.description != original.description ||
		decoded.filePath != original.filePath ||
		*decoded.hardDrive != *original.hardDrive ||
		!bytes.Equal(decoded.optionalData, original.optionalData) {
		t.Errorf("round trip changed the option:\n got %+v\nwant %+v", decoded, original)
	}
}

func TestParseLoadOptionRejectsTruncation(t *testing.T) {
	fixture := specFixture()
	// Every truncation point should error, never panic or misread.
	for _, cut := range []int{0, 3, 5, 8, 19, 30, len(fixture) - 5} {
		if _, err := parseLoadOption(fixture[:cut]); err == nil {
			t.Errorf("truncation at %d bytes should be an error", cut)
		}
	}
}

func TestParseLoadOptionRejectsUnterminatedDescription(t *testing.T) {
	// Attributes and length, then description characters that never
	// reach a NUL: the parser must not run off the end.
	b := []byte{0x01, 0, 0, 0, 0x04, 0, 'h', 0, 'i', 0}
	if _, err := parseLoadOption(b); err == nil {
		t.Fatal("an unterminated description should be an error")
	}
}

func TestParseLoadOptionSkipsUnknownNodes(t *testing.T) {
	// Firmware entries routinely carry vendor-specific device-path
	// nodes; the parser keeps what it understands and walks past the
	// rest by each node's own declared length.
	var b bytes.Buffer
	b.Write([]byte{0x01, 0, 0, 0})
	b.Write([]byte{14, 0})                                    // device path list: one 10-byte vendor node + end
	b.Write([]byte{0, 0})                                     // empty description
	b.Write([]byte{0x01, 0x17, 0x0A, 0x00, 1, 2, 3, 4, 5, 6}) // ACPI-ish mystery node
	b.Write([]byte{0x7F, 0xFF, 0x04, 0x00})
	o, err := parseLoadOption(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if o.hardDrive != nil || o.filePath != "" {
		t.Errorf("nothing recognizable should decode: %+v", o)
	}
}

func TestDescribeBootEntryFormatting(t *testing.T) {
	if got := bootEntryID(0); got != "Boot0000" {
		t.Errorf("got %q", got)
	}
	if got := bootEntryID(0x2001); got != "Boot2001" {
		t.Errorf("got %q", got)
	}
	if !strings.HasPrefix(bootEntryID(0xABC), "Boot0ABC") {
		t.Errorf("got %q", bootEntryID(0xABC))
	}
}
