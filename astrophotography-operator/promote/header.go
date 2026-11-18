package promote

// The promotion Job reads a frame's FITS header for the frame's
// identity and the settings that pick the sequence it counts toward. The
// header also gives the size of the complete file, so the Job can tell
// a whole frame from one that the driver is still writing.
//
// A FITS header is a series of 80-character cards, in blocks of 2,880
// bytes, that ends with the card END. A card that has a value has "= "
// in columns 9 and 10. A string value is in single quotes, with a
// doubled quote for a quote inside it, and anything after a "/" outside
// the quotes is a comment. The data follow the header, also in blocks
// of 2,880 bytes.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	blockSize = 2880
	cardSize  = 80
	// maxHeaderBlocks limits how much readHeader reads from a file that
	// has no END card.
	maxHeaderBlocks = 64
)

// errNoEnd means that no END card appeared in the first maxHeaderBlocks
// blocks. Either the file is not a FITS file, or the driver is still
// writing its header.
var errNoEnd = errors.New("the header has no END card")

// header is the primary header of a FITS file.
type header struct {
	values map[string]string
	// size is the header's length in bytes, in whole blocks.
	size int64
}

// readHeader reads the primary header from the start of a file. It
// returns io.ErrUnexpectedEOF when the file ends inside the header.
func readHeader(r io.Reader) (header, error) {
	h := header{values: map[string]string{}}
	block := make([]byte, blockSize)
	for range maxHeaderBlocks {
		if _, err := io.ReadFull(r, block); err != nil {
			if errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			return header{}, err
		}
		h.size += blockSize
		for card := range chunks(block, cardSize) {
			keyword := strings.TrimSpace(string(card[:8]))
			if keyword == "END" {
				return h, nil
			}
			if bytes.HasPrefix(card[8:], []byte("= ")) {
				h.values[keyword] = cardValue(string(card[10:]))
			}
		}
	}
	return header{}, errNoEnd
}

// chunks returns an iterator over consecutive slices of b of length
// size. A shorter remainder at the end is dropped.
func chunks(b []byte, size int) func(func([]byte) bool) {
	return func(yield func([]byte) bool) {
		for start := 0; start+size <= len(b); start += size {
			if !yield(b[start : start+size]) {
				return
			}
		}
	}
}

// cardValue returns the value of a card without its comment. A string
// value is returned without its quotes and trailing spaces, and a
// number or logical value is returned as written.
func cardValue(field string) string {
	field = strings.TrimLeft(field, " ")
	if !strings.HasPrefix(field, "'") {
		value, _, _ := strings.Cut(field, "/")
		return strings.TrimSpace(value)
	}
	var text strings.Builder
	for i := 1; i < len(field); i++ {
		if field[i] != '\'' {
			text.WriteByte(field[i])
			continue
		}
		if i+1 < len(field) && field[i+1] == '\'' {
			text.WriteByte('\'')
			i++
			continue
		}
		break
	}
	return strings.TrimRight(text.String(), " ")
}

// text returns a card's value as a string, and false when the header has
// no such card.
func (h header) text(keyword string) (string, bool) {
	value, found := h.values[keyword]
	return value, found
}

// number returns a card's value as a number, and false when the header
// has no such card or the value is not a number. FITS allows D in place
// of E before the exponent, as Fortran does.
func (h header) number(keyword string) (float64, bool) {
	value, found := h.values[keyword]
	if !found {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(strings.ReplaceAll(value, "D", "E"), 64)
	return parsed, err == nil
}

// fileSize returns the length of a complete file with this header: the
// header plus the primary data, padded to whole blocks. It counts no
// extensions, because the driver writes none.
func (h header) fileSize() (int64, error) {
	bitpix, okBitpix := h.number("BITPIX")
	axes, okAxes := h.number("NAXIS")
	if !okBitpix || !okAxes {
		return 0, fmt.Errorf("the header has no BITPIX or NAXIS")
	}
	data := int64(0)
	if axes > 0 {
		data = int64(max(bitpix, -bitpix)) / 8
		for axis := 1; axis <= int(axes); axis++ {
			length, ok := h.number("NAXIS" + strconv.Itoa(axis))
			if !ok {
				return 0, fmt.Errorf("the header has no NAXIS%d", axis)
			}
			data *= int64(length)
		}
	}
	return h.size + (data+blockSize-1)/blockSize*blockSize, nil
}
