package promote

// A reader of a FITS file's primary header, for what the promotion Job
// needs from a frame: its identity, the settings that decide which
// sequence it counts toward, and its size, which tells a whole frame
// from one that the driver is still writing.
//
// A FITS header is a run of 80-character cards in blocks of 2,880
// bytes, ending with the card END. A card that holds a value has "= "
// in columns 9 and 10. A string value is in single quotes, with a
// doubled quote for a quote, and anything after a "/" outside the
// quotes is a comment. The data follow the header, in blocks of 2,880
// bytes too.

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
	// maxHeaderBlocks bounds the read of a file that has no END. The
	// driver's header takes two blocks, and a header with every
	// declared keyword takes a few more.
	maxHeaderBlocks = 64
)

// errNoEnd is a header with no END card in maxHeaderBlocks blocks: the
// file is no FITS file, or the driver is still writing its header.
var errNoEnd = errors.New("the header has no END card")

// header is the primary header of a FITS file.
type header struct {
	values map[string]string
	// size is the header's length in bytes, whole blocks.
	size int64
}

// readHeader reads the primary header from the start of a file. A file
// that ends inside its header answers io.ErrUnexpectedEOF.
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
		for card := range slicesOf(block, cardSize) {
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

// slicesOf yields a slice in pieces of a size.
func slicesOf(b []byte, size int) func(func([]byte) bool) {
	return func(yield func([]byte) bool) {
		for start := 0; start+size <= len(b); start += size {
			if !yield(b[start : start+size]) {
				return
			}
		}
	}
}

// cardValue answers the value of a card with its comment removed: the
// text of a string without its quotes and trailing spaces, or the
// number or logical value as written.
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

// text answers a value as text, and false when the header has no such
// card.
func (h header) text(keyword string) (string, bool) {
	value, found := h.values[keyword]
	return value, found
}

// number answers a value as a number, and false when the header has no
// such card or its value is not a number. A FITS number can write its
// exponent with D, as Fortran does.
func (h header) number(keyword string) (float64, bool) {
	value, found := h.values[keyword]
	if !found {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(strings.ReplaceAll(value, "D", "E"), 64)
	return parsed, err == nil
}

// fileSize answers the length of a whole file with this header: the
// header and the primary data, padded to whole blocks. The data take
// |BITPIX| / 8 bytes for each element of the NAXIS axes. The driver
// writes no extension, so the primary data end the frame.
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
