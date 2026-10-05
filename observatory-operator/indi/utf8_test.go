package indi

import (
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

func TestValidUTF8(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"valid text passes", "25°C ±1 ✓", "25°C ±1 ✓"},
		{"a Latin-1 byte is replaced", "25\xb0C", "25�C"},
		{"a character cut off at the end is replaced", "25\xc2", "25�"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// One byte for each read splits every multi-byte character.
			got, err := io.ReadAll(&validUTF8{from: iotest.OneByteReader(strings.NewReader(c.in))})
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Errorf("read %q, want %q", got, c.want)
			}
		})
	}
}
