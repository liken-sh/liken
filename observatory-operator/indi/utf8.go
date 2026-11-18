package indi

import (
	"io"
	"unicode/utf8"
)

// validUTF8 is an io.Reader that replaces each byte that is not part of
// valid UTF-8 with U+FFFD. encoding/xml fails on invalid UTF-8, and a
// failed decoder would end the connection over one stray byte in a
// driver's label or message, such as a Latin-1 degree sign.
type validUTF8 struct {
	from   io.Reader
	buffer []byte
	// pending holds the leading bytes of a multi-byte character that
	// the end of a read cut off. The next read completes it.
	pending []byte
	out     []byte
	err     error
}

func (v *validUTF8) Read(p []byte) (int, error) {
	for len(v.out) == 0 && v.err == nil {
		if v.buffer == nil {
			v.buffer = make([]byte, 32*1024)
		}
		n, err := v.from.Read(v.buffer)
		v.err = err
		data := append(v.pending, v.buffer[:n]...)
		v.pending = nil
		for len(data) > 0 {
			r, size := utf8.DecodeRune(data)
			invalid := r == utf8.RuneError && size == 1
			if invalid && !utf8.FullRune(data) && err == nil {
				v.pending = append([]byte(nil), data...)
				break
			}
			if invalid {
				v.out = utf8.AppendRune(v.out, utf8.RuneError)
			} else {
				v.out = append(v.out, data[:size]...)
			}
			data = data[size:]
		}
	}
	if len(v.out) == 0 {
		return 0, v.err
	}
	n := copy(p, v.out)
	v.out = v.out[n:]
	return n, nil
}
