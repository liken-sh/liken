package fits

import (
	"fmt"
	"io"
)

// Complete walks every HDU of a file of size bytes and answers an error
// when a header does not read or the file ends before an HDU's data
// does. A file that a program still writes, such as an index file in
// the middle of a download, usually ends in the middle of an HDU, so
// the solver loads no file that Complete refuses. A last HDU whose data
// is not padded to a whole block still counts as complete, because the
// data itself is all there.
func Complete(r io.ReaderAt, size int64) error {
	for offset := int64(0); offset < size; {
		counted := &counter{r: io.NewSectionReader(r, offset, size-offset)}
		h, err := ReadHeader(counted)
		if err != nil {
			return fmt.Errorf("the HDU at byte %d: %w", offset, err)
		}
		data, err := h.dataSize()
		if err != nil {
			return fmt.Errorf("the HDU at byte %d: %w", offset, err)
		}
		offset += counted.n
		if offset+data > size {
			return fmt.Errorf("the file ends at byte %d, inside the data of the HDU that ends at byte %d", size, offset+data)
		}
		offset += padded(data)
	}
	return nil
}

// counter counts the bytes read through it, so Complete learns how
// many blocks a header took.
type counter struct {
	r io.Reader
	n int64
}

func (c *counter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
