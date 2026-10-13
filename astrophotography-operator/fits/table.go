package fits

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// Table is the header of a binary table extension and its rows.
type Table struct {
	Header Header
	rows   []byte
	width  int64
	count  int64
}

// ReadTable reads the first binary table of a file: it skips the
// primary HDU and reads the next HDU, which must be a BINTABLE.
// SourceExtractor's FITS_1.0 catalog and solve-field's .axy both hold
// their stars there.
func ReadTable(r io.Reader) (Table, error) {
	primary, err := ReadHeader(r)
	if err != nil {
		return Table{}, err
	}
	if err := skipData(r, primary); err != nil {
		return Table{}, err
	}
	h, err := ReadHeader(r)
	if err != nil {
		return Table{}, err
	}
	if kind, _ := h.Get("XTENSION"); kind != "BINTABLE" {
		return Table{}, fmt.Errorf("the first extension is %q, not BINTABLE", kind)
	}
	width, err := h.Int("NAXIS1")
	if err != nil {
		return Table{}, err
	}
	count, err := h.Int("NAXIS2")
	if err != nil {
		return Table{}, err
	}
	rows := make([]byte, width*count)
	if _, err := io.ReadFull(r, rows); err != nil {
		return Table{}, fmt.Errorf("reading %d rows of %d bytes: %w", count, width, err)
	}
	return Table{Header: h, rows: rows, width: width, count: count}, nil
}

func skipData(r io.Reader, h Header) error {
	size, err := h.dataSize()
	if err != nil {
		return err
	}
	if _, err := io.CopyN(io.Discard, r, padded(size)); err != nil {
		return fmt.Errorf("skipping %d bytes of data: %w", padded(size), err)
	}
	return nil
}

// Rows counts the table's rows.
func (t Table) Rows() int { return int(t.count) }

// Column answers each row's value of one column, by its TTYPE. The
// column must hold one number in each row, of the TFORM types L, B, I,
// J, K, E, or D.
func (t Table) Column(name string) ([]float64, error) {
	fields, err := t.Header.Int("TFIELDS")
	if err != nil {
		return nil, err
	}
	offset := int64(0)
	for field := int64(1); field <= fields; field++ {
		form, err := t.Header.String(fmt.Sprintf("TFORM%d", field))
		if err != nil {
			return nil, err
		}
		repeat, code, err := parseForm(form)
		if err != nil {
			return nil, err
		}
		size := repeat * sizes[code]
		if title, _ := t.Header.Get(fmt.Sprintf("TTYPE%d", field)); title == name {
			if repeat != 1 {
				return nil, fmt.Errorf("column %s holds %d values in each row, not one", name, repeat)
			}
			return t.read(offset, code), nil
		}
		offset += size
	}
	return nil, fmt.Errorf("column %s: %w", name, ErrMissing)
}

// sizes holds the bytes of one value of each TFORM type the package
// reads, and of the types it only steps over (A, X).
var sizes = map[byte]int64{'L': 1, 'X': 1, 'B': 1, 'A': 1, 'I': 2, 'J': 4, 'K': 8, 'E': 4, 'D': 8}

// parseForm reads a TFORM such as 1E or 16A: a repeat count, which is
// 1 when absent, and a type. A bit column (X) takes a byte for each
// eight bits, which the package treats as whole bytes.
func parseForm(form string) (int64, byte, error) {
	form = strings.TrimSpace(form)
	digits := strings.IndexFunc(form, func(r rune) bool { return r < '0' || r > '9' })
	if digits < 0 {
		return 0, 0, fmt.Errorf("TFORM %q names no type", form)
	}
	repeat := int64(1)
	if digits > 0 {
		repeat, _ = strconv.ParseInt(form[:digits], 10, 64)
	}
	code := form[digits]
	if _, known := sizes[code]; !known {
		return 0, 0, fmt.Errorf("TFORM %q has a type the package does not read", form)
	}
	if code == 'X' {
		repeat = (repeat + 7) / 8
	}
	return repeat, code, nil
}

// read answers the value at one offset of each row. FITS stores every
// number big-endian.
func (t Table) read(offset int64, code byte) []float64 {
	out := make([]float64, t.count)
	for row := range t.count {
		b := t.rows[row*t.width+offset:]
		switch code {
		case 'I':
			out[row] = float64(int16(binary.BigEndian.Uint16(b)))
		case 'J':
			out[row] = float64(int32(binary.BigEndian.Uint32(b)))
		case 'K':
			out[row] = float64(int64(binary.BigEndian.Uint64(b)))
		case 'E':
			out[row] = float64(math.Float32frombits(binary.BigEndian.Uint32(b)))
		case 'D':
			out[row] = math.Float64frombits(binary.BigEndian.Uint64(b))
		default:
			out[row] = float64(b[0])
		}
	}
	return out
}
