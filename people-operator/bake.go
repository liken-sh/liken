package main

// A bake is one read of an nfs:// or claim:// picture by a baker pod,
// which mounts the export or the claim. The reason Baked reports a
// successful bake, and BakeFailed or DecodeFailed a failed one.
//
// `people-operator bake <path> <colour>` is the command of a baker pod.
// It reads one picture from a mounted file, with the same size limit and
// decoder as an HTTP fetch, and prints one line of JSON. The operator
// decodes the whole pod log as that JSON (bakers.go), and the log has
// both stdout and stderr. So the command prints nothing else, except
// the usage message for bad arguments, which only a bug in the
// operator causes.

import (
	"encoding/json"
	"fmt"
	"image/color"
	"io"
	"os"
	"time"
)

// bakeResult is the JSON line a baker pod prints. A successful bake sets
// the thumbnail, modTime, and size. A failed bake sets reason and error.
type bakeResult struct {
	Thumbnail string `json:"thumbnail,omitempty"`
	ModTime   string `json:"modTime,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Error     string `json:"error,omitempty"`
}

// runBake returns exit status 2 for bad arguments and 1 for a picture
// that fails. The operator builds the arguments, so status 2 means a
// bug in the operator and not a bad picture.
func runBake(out, stderr io.Writer, args []string) int {
	if len(args) != 2 {
		fmt.Fprintf(stderr, "bake takes a path and a colour, and got %d arguments\nusage: people-operator bake <path> <#rrggbb>\n", len(args))
		return 2
	}
	backdrop, err := parseColour(args[1])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	return bake(out, args[0], backdrop)
}

// bake prints the result for the file at path and returns 0 when the
// result has a thumbnail and 1 when it has an error. backdrop is the
// Person's colour. personColour hashes the Person's name, and the
// operator passes the result in the second argument (colourArgument),
// so the pod and the operator cannot disagree on the colour of one
// Person.
func bake(out io.Writer, path string, backdrop color.Color) int {
	result := bakeFile(path, backdrop)
	line, _ := json.Marshal(result)
	fmt.Fprintf(out, "%s\n", line)
	if result.Error != "" {
		return 1
	}
	return 0
}

func bakeFile(path string, backdrop color.Color) bakeResult {
	file, err := os.Open(path)
	if err != nil {
		return bakeResult{Reason: reasonBakeFailed, Error: err.Error()}
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return bakeResult{Reason: reasonBakeFailed, Error: err.Error()}
	}
	if info.Size() > maxPictureBytes {
		return bakeResult{Reason: reasonBakeFailed, Error: errTooLarge.Error()}
	}
	picture, err := io.ReadAll(io.LimitReader(file, maxPictureBytes+1))
	if err != nil {
		return bakeResult{Reason: reasonBakeFailed, Error: err.Error()}
	}
	thumbnail, err := makeThumbnail(picture, backdrop)
	if err != nil {
		// failure gives DecodeFailed for a picture that does not decode,
		// and BakeFailed for any other error.
		return bakeResult{Reason: reasonOf(failure(reasonBakeFailed, err)), Error: err.Error()}
	}
	return bakeResult{
		Thumbnail: thumbnail,
		ModTime:   info.ModTime().UTC().Format(time.RFC3339Nano),
		Size:      info.Size(),
	}
}

// colourArgument formats a colour as #rrggbb, which parseColour reads.
func colourArgument(c color.RGBA) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

func parseColour(text string) (color.RGBA, error) {
	var c color.RGBA
	if _, err := fmt.Sscanf(text, "#%02x%02x%02x", &c.R, &c.G, &c.B); err != nil || len(text) != 7 {
		return c, fmt.Errorf("the colour %q is not in the form #rrggbb", text)
	}
	c.A = 0xFF
	return c, nil
}
