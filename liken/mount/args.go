package main

// The command line mount(8) accepts, and what liken's mount does
// with it.
//
// This grammar is old and irregular, and it cannot be rebuilt on
// Go's flag package: options repeat and accumulate (-o may appear
// many times), short names cluster (-rv), a value may be attached to
// its name (-tnfs) or follow it, and long names carry an operation
// rather than a value (--make-rshared). The parser is therefore
// written out by hand, and it accepts the forms that callers really
// use rather than every form that has ever existed.
//
// One form is deliberately refused. `mount -a` mounts everything in
// /etc/fstab, and a liken machine has no fstab: every filesystem it
// mounts is either init's work or a pod's volume, and both name what
// they need. Refusing with a message is better than reading a file
// that will never exist and reporting success.

import (
	"fmt"
	"strings"

	"golang.org/x/sys/unix"
)

// request is one mount, as the command line described it.
type request struct {
	// source is what to mount and target is where. A command line
	// with one path names only the target, which is how remounts and
	// propagation changes are written.
	source string
	target string

	// fstype is the -t argument. It is empty for bind mounts,
	// remounts, and propagation changes, none of which introduce a
	// filesystem.
	fstype string

	// options are the -o lists, in the order given. They stay
	// unsplit until the last moment, because a mount helper needs
	// them exactly as they arrived.
	options []string

	// flags are the bits that came from long options rather than
	// from an -o list, such as --bind.
	flags uintptr

	// These four are mount(8)'s own behavior switches. This program
	// acts on none of them, and forwards them to a helper, which is
	// the only place they can still mean anything.
	verbose bool
	fake    bool
	noMtab  bool
	sloppy  bool
}

// longFlags are the long options that name an operation. Each is a
// spelling of an option that could also have arrived inside -o, and
// each sets exactly the same bits.
var longFlags = map[string]uintptr{
	"--bind":             unix.MS_BIND,
	"--rbind":            unix.MS_BIND | unix.MS_REC,
	"--move":             unix.MS_MOVE,
	"--make-shared":      unix.MS_SHARED,
	"--make-rshared":     unix.MS_SHARED | unix.MS_REC,
	"--make-slave":       unix.MS_SLAVE,
	"--make-rslave":      unix.MS_SLAVE | unix.MS_REC,
	"--make-private":     unix.MS_PRIVATE,
	"--make-rprivate":    unix.MS_PRIVATE | unix.MS_REC,
	"--make-unbindable":  unix.MS_UNBINDABLE,
	"--make-runbindable": unix.MS_UNBINDABLE | unix.MS_REC,
}

// parser walks one command line. It carries its position, because
// an option's value may sit in the next argument, and reading it
// there means the walk skips it.
type parser struct {
	argv  []string
	at    int
	req   request
	paths []string
}

// value takes the value that belongs to an option: the rest of this
// argument when one is attached to the name, and the following
// argument otherwise.
func (p *parser) value(name, attached string) (string, error) {
	if attached != "" {
		return attached, nil
	}
	if p.at+1 >= len(p.argv) {
		return "", fmt.Errorf("%s needs a value", name)
	}
	p.at++
	return p.argv[p.at], nil
}

// parseArgs reads a mount command line. It returns an error only for
// a command line this program cannot act on, never for an option it
// merely does not understand: an unknown -o entry is the
// filesystem's business, and splitOptions passes it along.
func parseArgs(argv []string) (*request, error) {
	p := &parser{argv: argv}
	for ; p.at < len(p.argv); p.at++ {
		if err := p.argument(p.argv[p.at]); err != nil {
			return nil, err
		}
	}

	// One path is a target, which is what a remount or a propagation
	// change is written with. Two are a source and a target. The
	// long spellings may have supplied either already.
	switch len(p.paths) {
	case 0:
	case 1:
		p.req.target = p.paths[0]
	case 2:
		p.req.source, p.req.target = p.paths[0], p.paths[1]
	default:
		return nil, fmt.Errorf("too many paths: %s", strings.Join(p.paths, " "))
	}
	if p.req.target == "" {
		return nil, fmt.Errorf("no mount point given")
	}
	return &p.req, nil
}

// argument handles one argument of the command line.
func (p *parser) argument(arg string) error {
	switch {
	case arg == "--":
		// Everything after this is a path, however it is spelled. A
		// target whose name begins with a dash is why this exists.
		p.paths = append(p.paths, p.argv[p.at+1:]...)
		p.at = len(p.argv)
		return nil
	case arg == "-a" || arg == "--all":
		return fmt.Errorf("-a mounts what /etc/fstab lists, and this system has no fstab; name what to mount")
	case strings.HasPrefix(arg, "--"):
		return p.longOption(arg)
	case strings.HasPrefix(arg, "-") && arg != "-":
		return p.shortOptions(arg)
	default:
		p.paths = append(p.paths, arg)
		return nil
	}
}

// longOption handles one argument that begins with two dashes.
func (p *parser) longOption(arg string) error {
	switch arg {
	case "--options", "--types", "--source", "--target":
		v, err := p.value(arg, "")
		if err != nil {
			return err
		}
		switch arg {
		case "--options":
			p.req.options = append(p.req.options, v)
		case "--types":
			p.req.fstype = v
		case "--source":
			p.req.source = v
		case "--target":
			p.req.target = v
		}
	case "--read-only":
		p.req.options = append(p.req.options, "ro")
	case "--rw", "--read-write":
		p.req.options = append(p.req.options, "rw")
	case "--verbose":
		p.req.verbose = true
	case "--fake":
		p.req.fake = true
	case "--no-mtab":
		p.req.noMtab = true
	case "--sloppy":
		p.req.sloppy = true
	case "--no-canonicalize", "--internal-only":
		// Both ask mount(8) to skip work this program never does.
		// Paths arrive here as written, and there is no mount table
		// of this program's own to consult.
	default:
		bits, known := longFlags[arg]
		if !known {
			return fmt.Errorf("unknown option %s", arg)
		}
		p.req.flags |= bits
	}
	return nil
}

// shortOptions handles one argument that begins with a single dash.
// Several names may share the argument, and the last of them may
// carry a value, so -rv, -o bind, and -obind are each one argument.
func (p *parser) shortOptions(arg string) error {
	for pos, name := range arg[1:] {
		attached := arg[1+pos+len(string(name)):]
		switch name {
		case 'o', 't':
			v, err := p.value("-"+string(name), attached)
			if err != nil {
				return err
			}
			if name == 'o' {
				p.req.options = append(p.req.options, v)
			} else {
				p.req.fstype = v
			}
			// A name that took a value ends the argument, because
			// the value ran to the end of it.
			return nil
		case 'r':
			p.req.options = append(p.req.options, "ro")
		case 'w':
			p.req.options = append(p.req.options, "rw")
		case 'v':
			p.req.verbose = true
		case 'f':
			p.req.fake = true
		case 'n':
			p.req.noMtab = true
		case 's':
			p.req.sloppy = true
		case 'B':
			p.req.flags |= unix.MS_BIND
		case 'R':
			p.req.flags |= unix.MS_BIND | unix.MS_REC
		case 'M':
			p.req.flags |= unix.MS_MOVE
		default:
			return fmt.Errorf("unknown option -%c", name)
		}
	}
	return nil
}

// optionString joins the -o lists back into the single list that
// both mount(2) and a mount helper expect. Rebuilding it in the
// order the options arrived is what keeps the last name for a
// setting the winning one, whether the caller wrote -o ro,rw or
// -o ro -o rw.
func (r *request) optionString() string {
	return strings.Join(r.options, ",")
}
