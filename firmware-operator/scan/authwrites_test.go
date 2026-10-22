package main

import (
	"bytes"
	"encoding/binary"
	"slices"
	"testing"
)

// sha256ListGUID is EFI_CERT_SHA256_GUID as it is stored, the type of
// the lists that authWrite's tests carry.
var sha256ListGUID = [16]byte{0x26, 0x16, 0xc4, 0xc1, 0x4c, 0x50, 0x92, 0x40, 0xac, 0xa9, 0x41, 0xf9, 0x36, 0x93, 0x43, 0x28}

// authWrite builds one authenticated append as fwupd writes it through
// efivarfs: the attributes, the time, a certificate with eight bytes of
// signature, and the signature lists.
func authWrite(lists ...[]byte) []byte {
	cert := make([]byte, 24+8)
	binary.LittleEndian.PutUint32(cert, uint32(len(cert)))
	binary.LittleEndian.PutUint16(cert[4:], winCertRevision)
	binary.LittleEndian.PutUint16(cert[6:], winCertTypeEFIGUID)
	copy(cert[8:], pkcs7GUID[:])
	copy(cert[24:], bytes.Repeat([]byte{0x5e}, 8))
	write := append([]byte{0x67, 0, 0, 0}, make([]byte, 16)...)
	write = append(write, cert...)
	for _, list := range lists {
		write = append(write, list...)
	}
	return write
}

// signatureList builds one list of SHA-256 signatures, each an owner
// GUID and a hash, filled with fill.
func signatureList(fill byte, signatures int) []byte {
	list := bytes.Repeat([]byte{fill}, 28+48*signatures)
	copy(list, sha256ListGUID[:])
	binary.LittleEndian.PutUint32(list[16:], uint32(len(list)))
	binary.LittleEndian.PutUint32(list[20:], 0)
	binary.LittleEndian.PutUint32(list[24:], 48)
	return list
}

func TestSplitWritesFindsEachAuthenticatedWrite(t *testing.T) {
	one, two := authWrite(signatureList(0xbb, 1)), authWrite(signatureList(0xcc, 2), signatureList(0xdd, 1))
	empty := authWrite()
	cases := []struct {
		name   string
		writes [][]byte
	}{
		{"one write of one list", [][]byte{one}},
		{"one write of two lists", [][]byte{two}},
		{"two writes", [][]byte{one, two}},
		{"the same pair twice", [][]byte{one, two, one, two}},
		{"a write with no list before another", [][]byte{empty, one}},
		{"a write with no list at the end", [][]byte{one, empty}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := splitWrites(bytes.Join(c.writes, nil))

			if err != nil || !slices.EqualFunc(got, c.writes, bytes.Equal) {
				t.Errorf("got %d writes, %v; want the %d writes back", len(got), err, len(c.writes))
			}
		})
	}
}

func TestSplitWritesRefusesBytesThatAreNotAuthenticatedWrites(t *testing.T) {
	write := authWrite(signatureList(0xbb, 1))
	cases := []struct {
		name  string
		bytes []byte
		want  string
	}{
		{"a write shorter than its header", write[:40],
			"write 1 has 40 bytes, fewer than the 44 of an authenticated write's header"},
		{"a certificate of another revision", patch(write, 24, 0x00, 0x01),
			"write 1 has no PKCS7 certificate of revision 0x0200 at byte 20"},
		{"a certificate of another type", patch(write, 26, 0x02, 0x00),
			"write 1 has no PKCS7 certificate of revision 0x0200 at byte 20"},
		{"a certificate that runs past the end", patch(write, 20, 0xff, 0x0f),
			"write 1 states a certificate of 4095 bytes, which runs past the end of its 128 bytes"},
		{"a list that runs past the end", write[:len(write)-1],
			"the signature list at byte 52 of write 1 states 76 bytes, and 75 remain"},
		{"a list shorter than its header", patch(write, 68, 0x10, 0x00),
			"the signature list at byte 52 of write 1 states 16 bytes, fewer than its 28-byte header"},
		{"a second write that is not one", append(slices.Clone(write), 0x47, 0, 0, 0),
			"the signature list at byte 128 of write 1 has 4 bytes, fewer than its 28-byte header"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := splitWrites(c.bytes)

			if got != nil || err == nil || err.Error() != c.want {
				t.Errorf("got %d writes, %v; want the error %q", len(got), err, c.want)
			}
		})
	}
}

// patch answers a copy of b with the given bytes at offset.
func patch(b []byte, offset int, with ...byte) []byte {
	out := slices.Clone(b)
	copy(out[offset:], with)
	return out
}
