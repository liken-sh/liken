package main

// fwupd installs a Secure Boot database update as one authenticated
// write to the variable for each payload in the cabinet. A cabinet of
// the third-party db and the option ROM db holds two payloads, and
// fwupd can install a cabinet more than once in one run, so a
// rehearsal can record several writes. The firmware verifies the
// signature of each write over that write's own signature lists, and
// the kernel passes each write call to it as one SetVariable. So the
// apply must write each one by itself: the bytes of two writes in one
// call fail the first signature. The copy that a rehearsal runs
// against is a plain file, which keeps the bytes of every write and
// loses where one ends, so splitWrites finds the ends again from the
// format that UEFI gives a write:
//
//   - the four bytes of attributes that efivarfs puts first;
//   - the EFI_VARIABLE_AUTHENTICATION_2 header: an EFI_TIME of 16
//     bytes, then a WIN_CERTIFICATE_UEFI_GUID whose dwLength counts its
//     own header, its CertType, and the PKCS7 signature;
//   - the EFI_SIGNATURE_LISTs, each of which states its own size.
//
// The lists of a write end where the bytes end, or where the next
// write's header starts. A list starts with its SignatureType, and
// splitWrites reads a header only where the attributes of an
// authenticated append, a certificate of revision 0x0200 and type
// WIN_CERT_TYPE_EFI_GUID, and the PKCS7 CertType all match.

import (
	"encoding/binary"
	"fmt"
)

const (
	// certOffset is where a write's certificate starts: after the
	// attributes and the EFI_TIME.
	certOffset = 4 + 16
	// certHeaderSize is the WIN_CERTIFICATE header and the CertType GUID.
	certHeaderSize = 8 + 16
	// writeHeaderSize is the smallest write that holds a whole header.
	writeHeaderSize = certOffset + certHeaderSize
	// listHeaderSize is an EFI_SIGNATURE_LIST's SignatureType and its
	// three sizes.
	listHeaderSize = 16 + 4 + 4 + 4

	winCertRevision    = 0x0200
	winCertTypeEFIGUID = 0x0ef1
)

// pkcs7GUID is EFI_CERT_TYPE_PKCS7_GUID,
// 4aafd29d-68df-49ee-8aa9-347d375665a7, as it is stored.
var pkcs7GUID = [16]byte{0x9d, 0xd2, 0xaf, 0x4a, 0xdf, 0x68, 0xee, 0x49, 0x8a, 0xa9, 0x34, 0x7d, 0x37, 0x56, 0x65, 0xa7}

// splitWrites answers the authenticated writes whose bytes follow each
// other in b, or an error that names the first byte that is not part
// of one.
func splitWrites(b []byte) ([][]byte, error) {
	var writes [][]byte
	for start := 0; start < len(b); {
		n, err := writeLength(b[start:], len(writes)+1)
		if err != nil {
			return nil, err
		}
		writes = append(writes, b[start:start+n])
		start += n
	}
	return writes, nil
}

// writeLength answers the length of the write at the start of b, the
// write numbered index.
func writeLength(b []byte, index int) (int, error) {
	if len(b) < writeHeaderSize {
		return 0, fmt.Errorf("write %d has %d bytes, fewer than the %d of an authenticated write's header", index, len(b), writeHeaderSize)
	}
	if !isCertificate(b) {
		return 0, fmt.Errorf("write %d has no PKCS7 certificate of revision 0x%04x at byte %d", index, winCertRevision, certOffset)
	}
	certLength := int(binary.LittleEndian.Uint32(b[certOffset:]))
	n := certOffset + certLength
	if certLength < certHeaderSize || n > len(b) {
		return 0, fmt.Errorf("write %d states a certificate of %d bytes, which runs past the end of its %d bytes", index, certLength, len(b))
	}
	for n < len(b) && !isWriteHeader(b[n:]) {
		remaining := len(b) - n
		if remaining < listHeaderSize {
			return 0, fmt.Errorf("the signature list at byte %d of write %d has %d bytes, fewer than its %d-byte header", n, index, remaining, listHeaderSize)
		}
		size := int(binary.LittleEndian.Uint32(b[n+16:]))
		if size < listHeaderSize {
			return 0, fmt.Errorf("the signature list at byte %d of write %d states %d bytes, fewer than its %d-byte header", n, index, size, listHeaderSize)
		}
		if size > remaining {
			return 0, fmt.Errorf("the signature list at byte %d of write %d states %d bytes, and %d remain", n, index, size, remaining)
		}
		n += size
	}
	return n, nil
}

// isWriteHeader reports whether b starts with the header of an
// authenticated append.
func isWriteHeader(b []byte) bool {
	if len(b) < writeHeaderSize {
		return false
	}
	attributes := binary.LittleEndian.Uint32(b)
	return attributes&attrAppendWrite != 0 && attributes&attrTimeBasedAuth != 0 && isCertificate(b)
}

// isCertificate reports whether the certificate of the write at the
// start of b is a WIN_CERTIFICATE_UEFI_GUID of the PKCS7 type.
func isCertificate(b []byte) bool {
	cert := b[certOffset:]
	return binary.LittleEndian.Uint16(cert[4:]) == winCertRevision &&
		binary.LittleEndian.Uint16(cert[6:]) == winCertTypeEFIGUID &&
		[16]byte(cert[8:24]) == pkcs7GUID
}
