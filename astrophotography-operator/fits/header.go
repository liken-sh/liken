// Package fits reads the parts of a FITS file that this operator needs:
// the header of an HDU, and the numeric columns of a binary table. The
// worker reads a frame's size from its header and the stars from
// SourceExtractor's catalog, and the solver checks that each index
// file holds all of its data and reads its header, and reads the
// headers of each .axy it receives and of each WCS file that the
// engine writes. Each of these is a small header or a table of a few
// kilobytes, so the package reads from an io.Reader and holds nothing
// else.
//
// The format is the FITS standard, version 4.0: a file is a sequence of
// HDUs, each a header of 80-character cards in blocks of 2,880 bytes,
// and a data area padded to the same blocks.
package fits

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	// BlockSize is the size of every block of a FITS file.
	BlockSize = 2880
	cardSize  = 80
	// maxHeaderBlocks bounds a header that never ends, such as a file
	// that is not FITS at all. The largest header the operator reads,
	// a .axy's, is four blocks.
	maxHeaderBlocks = 1000
)

// Card is one keyword of a header. Value holds a string's text with
// the quotes removed, and any other value as written. A COMMENT or
// HISTORY card holds its text in Value.
type Card struct {
	Key   string
	Value string
}

// Header is the cards of one HDU, in their order, without END.
type Header struct {
	Cards []Card
}

// ErrMissing is the error of a keyword that the header does not hold.
var ErrMissing = errors.New("no such keyword")

// ReadHeader reads one header from r, up to and including the block
// that holds END, so r is then at the start of the HDU's data.
func ReadHeader(r io.Reader) (Header, error) {
	var h Header
	block := make([]byte, BlockSize)
	for range maxHeaderBlocks {
		if _, err := io.ReadFull(r, block); err != nil {
			return Header{}, fmt.Errorf("reading a FITS header: %w", err)
		}
		for start := 0; start < BlockSize; start += cardSize {
			card := block[start : start+cardSize]
			key := strings.TrimRight(string(card[:8]), " ")
			if key == "END" {
				return h, nil
			}
			if key == "" {
				continue
			}
			h.Cards = append(h.Cards, Card{Key: key, Value: value(card)})
		}
	}
	return Header{}, fmt.Errorf("no END in the first %d blocks: not a FITS header", maxHeaderBlocks)
}

// value reads the value of one card. A card with "= " in columns 9 and
// 10 has a value, and a quote starts a string, in which two quotes
// stand for one. Any other value ends at the comment's "/". A card
// with no "= " holds commentary text, as COMMENT and HISTORY do.
func value(card []byte) string {
	if !bytes.Equal(card[8:10], []byte("= ")) {
		return strings.TrimSpace(string(card[8:]))
	}
	field := strings.TrimLeft(string(card[10:]), " ")
	if !strings.HasPrefix(field, "'") {
		text, _, _ := strings.Cut(field, "/")
		return strings.TrimSpace(text)
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
	// Trailing spaces in a string are not significant (section 4.2.1).
	return strings.TrimRight(text.String(), " ")
}

// Get answers the value of the first card with the key.
func (h Header) Get(key string) (string, bool) {
	for _, c := range h.Cards {
		if c.Key == key {
			return c.Value, true
		}
	}
	return "", false
}

// String answers a keyword's text.
func (h Header) String(key string) (string, error) {
	text, held := h.Get(key)
	if !held {
		return "", fmt.Errorf("%s: %w", key, ErrMissing)
	}
	return text, nil
}

// Int answers a keyword's integer value.
func (h Header) Int(key string) (int64, error) {
	text, err := h.String(key)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s = %q is not an integer", key, text)
	}
	return n, nil
}

// Float answers a keyword's real value. FITS writes an exponent with
// E or D, and Go reads only E.
func (h Header) Float(key string) (float64, error) {
	text, err := h.String(key)
	if err != nil {
		return 0, err
	}
	f, err := strconv.ParseFloat(strings.ReplaceAll(text, "D", "E"), 64)
	if err != nil {
		return 0, fmt.Errorf("%s = %q is not a number", key, text)
	}
	return f, nil
}

// Comments answers the text of each COMMENT card, in order.
func (h Header) Comments() []string {
	var out []string
	for _, c := range h.Cards {
		if c.Key == "COMMENT" {
			out = append(out, c.Value)
		}
	}
	return out
}

// dataSize answers the bytes of the HDU's data, before the padding to
// a whole block: |BITPIX| / 8 × (PCOUNT + GCOUNT × NAXIS1 × ... ×
// NAXISn), from section 4.4.1. An HDU with NAXIS = 0 has no data.
func (h Header) dataSize() (int64, error) {
	bitpix, err := h.Int("BITPIX")
	if err != nil {
		return 0, err
	}
	axes, err := h.Int("NAXIS")
	if err != nil {
		return 0, err
	}
	if axes == 0 {
		return 0, nil
	}
	elements := int64(1)
	for axis := int64(1); axis <= axes; axis++ {
		n, err := h.Int(fmt.Sprintf("NAXIS%d", axis))
		if err != nil {
			return 0, err
		}
		elements *= n
	}
	pcount, gcount := h.optional("PCOUNT", 0), h.optional("GCOUNT", 1)
	return max(bitpix, -bitpix) / 8 * gcount * (pcount + elements), nil
}

// optional answers an integer keyword, or a default when the header
// does not hold it.
func (h Header) optional(key string, otherwise int64) int64 {
	n, err := h.Int(key)
	if err != nil {
		return otherwise
	}
	return n
}

// padded rounds a size up to whole blocks.
func padded(size int64) int64 {
	return (size + BlockSize - 1) / BlockSize * BlockSize
}
