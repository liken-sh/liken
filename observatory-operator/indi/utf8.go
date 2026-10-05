package indi

import (
	"io"
	"unicode/utf8"
)

// validUTF8 replaces each byte that is not part of valid UTF-8 with
// U+FFFD. encoding/xml refuses invalid UTF-8 and stops, and a stopped
// decoder would end the connection over one stray byte in a driver's
// label or message, such as a degree sign in Latin-1.
type validUTF8 struct {
	from   io.Reader
	buffer []byte
	// pending holds the first bytes of a character that a read split.
	// They wait for the next read to complete the character.
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
