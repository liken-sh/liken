package main

// `people-operator bake <path> <colour>` is the program a baker pod
// runs. It reads one picture from a mounted file, applies the same
// limits as a fetch, and writes one line of JSON to standard output.
// The operator reads that line from the pod's log (bakers.go). The
// command writes nothing else to either output, because the log holds
// both.

import (
	"encoding/json"
	"fmt"
	"image/color"
	"io"
	"os"
	"time"
)

// bakeResult is the line a baker pod writes. A bake that worked sets
// the thumbnail and the file's modification time and size. A bake that
// failed sets the reason and the error.
type bakeResult struct {
	Thumbnail string `json:"thumbnail,omitempty"`
	ModTime   string `json:"modTime,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Error     string `json:"error,omitempty"`
}

// runBake reads the command's arguments, and answers the exit status.
// A wrong argument writes to problems and exits with 2, because the
// operator builds every argument and a wrong one is a fault in the
// operator, not in the picture.
func runBake(out, problems io.Writer, args []string) int {
	if len(args) != 2 {
		fmt.Fprintln(problems, "usage: people-operator bake <path> <#rrggbb>")
		return 2
	}
	backdrop, err := parseColour(args[1])
	if err != nil {
		fmt.Fprintln(problems, err)
		return 2
	}
	return bake(out, args[0], backdrop)
}

// bake writes the result for the file at path, and answers the exit
// status: 0 when the thumbnail is in the line, and 1 when the line
// holds an error. The backdrop is the Person's colour, which the pod
// cannot compute without the Person, so the operator passes it as the
// command's second argument (colourArgument).
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
	thumbnail, err := bakeThumbnail(picture, backdrop)
	if err != nil {
		return bakeResult{Reason: reasonOf(failure(reasonBakeFailed, err)), Error: err.Error()}
	}
	return bakeResult{
		Thumbnail: thumbnail,
		ModTime:   info.ModTime().UTC().Format(time.RFC3339Nano),
		Size:      info.Size(),
	}
}

// colourArgument writes a colour as #rrggbb, the form parseColour
// reads.
func colourArgument(c color.RGBA) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

// parseColour reads a colour in the form #rrggbb.
func parseColour(text string) (color.RGBA, error) {
	var c color.RGBA
	if _, err := fmt.Sscanf(text, "#%02x%02x%02x", &c.R, &c.G, &c.B); err != nil || len(text) != 7 {
		return c, fmt.Errorf("the colour %q is not in the form #rrggbb", text)
	}
	c.A = 0xFF
	return c, nil
}
