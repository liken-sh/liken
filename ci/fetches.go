package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// checkFetches lists each file that breaks a rule of how the repository
// downloads what it pins. Root AGENTS.md states the rules and the
// reason for each one, under "Pins and downloads":
//
//   - a fetch.sh or a Dockerfile never downloads a checksum file. The
//     digest is in the repository, where latest.sh --bump wrote it, so
//     a build accepts only the bytes that a commit named;
//   - a fetch.sh downloads into a .partial file, which replaces the
//     cached file only after its digest matches, so a torn or wrong
//     download never sits in the cache under the name of a good one;
//   - each VERSION and each fetch.sh has a latest.sh in its directory,
//     each Dockerfile that downloads has one in its directory or a
//     directory above it, and each latest.sh is one that versions.sh
//     runs, so make versions reports every pin;
//   - a latest.sh that reads a sha256sums.asc file of kernel.org
//     verifies its signature through trust/keys/kernel-org-digest.sh;
//   - a check that a binary is static accepts a static PIE, which gcc
//     on Alpine builds for -static and file(1) calls "static-pie
//     linked";
//   - no script or Dockerfile installs Alpine packages with apk add,
//     because apk installs whatever the branch holds on the day of the
//     build, and no digest in the repository names those bytes. A
//     static program builds with buildroot in liken/userland instead,
//     as "Static programs" in root AGENTS.md says;
//   - each fetch.sh and latest.sh sources toolchains/pin.sh and reads
//     the network only through its functions, never with curl or wget
//     of its own, so each step of a pin has one implementation. The
//     library itself downloads into .partial files, as a fetch.sh did.
//
// The checks read shell and Dockerfile text by pattern, not by parsing
// it, so each one looks for a narrow form: a file name of a checksum
// file, the output argument of curl and wget, and the words of a file(1)
// check. A line that breaks a pattern for a reason the check does not
// see takes a comment line directly above it that holds
// "fetch-check: allow" and the reason.
func checkFetches(root string) ([]string, error) {
	files, err := sourceFiles(root)
	if err != nil {
		return nil, err
	}
	reported := map[string]bool{}
	for _, file := range files {
		if path.Base(file) == "latest.sh" {
			reported[path.Dir(file)] = true
		}
	}
	var findings []string
	for _, file := range files {
		name, dir := path.Base(file), path.Dir(file)
		if name == "VERSION" && !reported[dir] {
			findings = append(findings, fmt.Sprintf("%s pins a version, and %s has no latest.sh", file, dir))
		}
		if !isDockerfile(name) && !strings.HasSuffix(name, ".sh") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, file))
		if errors.Is(err, fs.ErrNotExist) {
			// git still tracks a file that the tree deleted.
			continue
		}
		if err != nil {
			return nil, err
		}
		lines := logicalLines(string(data))
		findings = append(findings, staticChecks(file, lines)...)
		findings = append(findings, apkInstalls(file, lines)...)
		switch {
		case file == pinLibrary:
			findings = append(findings, fetchScript(file, lines)...)
		case name == "fetch.sh":
			if !reported[dir] {
				findings = append(findings, fmt.Sprintf("%s downloads a pin, and %s has no latest.sh", file, dir))
			}
			findings = append(findings, fetchScript(file, lines)...)
			findings = append(findings, libraryUse(file, lines)...)
		case isDockerfile(name):
			found, downloads := dockerfileFetches(file, lines)
			findings = append(findings, found...)
			if downloads && !reportedAbove(dir, reported) {
				findings = append(findings, fmt.Sprintf("%s downloads a pin, and neither %s nor a directory above it has a latest.sh", file, dir))
			}
		case name == "latest.sh":
			found, err := latestScript(root, file, lines)
			if err != nil {
				return nil, err
			}
			findings = append(findings, found...)
			findings = append(findings, libraryUse(file, lines)...)
		}
	}
	return findings, nil
}

// line is one command or instruction, with the lines that a backslash
// continues joined onto it. n is the number of its first line, and
// allowed is true when the comment line above it excuses it.
type line struct {
	n       int
	text    string
	allowed bool
}

// logicalLines joins each line that ends in a backslash with the lines
// that continue it, and leaves out comment lines and empty lines.
func logicalLines(text string) []line {
	var lines []line
	var current *line
	allow := false
	for i, raw := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			if current == nil {
				allow = strings.HasPrefix(trimmed, "#") && strings.Contains(trimmed, "fetch-check: allow")
			}
			continue
		}
		if current == nil {
			current = &line{n: i + 1, allowed: allow}
			allow = false
		}
		rest, continued := strings.CutSuffix(strings.TrimRight(raw, " \t"), "\\")
		current.text += rest + " "
		if !continued {
			lines = append(lines, *current)
			current = nil
		}
	}
	if current != nil {
		lines = append(lines, *current)
	}
	return lines
}

// checksumFile matches the name of a file that lists digests:
// SHA256SUMS, sha256sums.asc, CHECKSUMS, checksums.txt, and a file
// named for the one file it checks, such as a.tar.xz.sha256. The
// commands sha256sum and sha512sum do not match.
var checksumFile = regexp.MustCompile(`(?i)(sha(1|256|512)sums|md5sums|checksums|\.sha(1|256|512)(sum)?\b)`)

// fetchScript lists each line of a fetch.sh that names a checksum file,
// and each curl or wget that writes anywhere but a .partial file.
func fetchScript(file string, lines []line) []string {
	var findings []string
	for _, l := range lines {
		if l.allowed {
			continue
		}
		if checksumFile.MatchString(l.text) {
			findings = append(findings, fmt.Sprintf("%s:%d reads a checksum file from upstream at build time; write its digest into the repository with latest.sh --bump", file, l.n))
		}
		for _, out := range downloadOutputs(l.text) {
			if out == "" {
				findings = append(findings, fmt.Sprintf("%s:%d downloads into the remote file name, not into a .partial file", file, l.n))
			} else if !strings.HasSuffix(out, ".partial") && out != "-" && out != "/dev/null" {
				findings = append(findings, fmt.Sprintf("%s:%d downloads into %q, not into a .partial file", file, l.n, out))
			}
		}
	}
	return findings
}

// pinLibrary is the shell library of the pins, which every fetch.sh and
// latest.sh sources.
const pinLibrary = "toolchains/pin.sh"

// libraryUse lists a fetch.sh or latest.sh that does not source the
// library, and each line of one that runs curl or wget itself. A
// source line is a command whose path, from the script's directory
// through $here, is the library, so a comment that names the library
// or a path one directory off does not count.
func libraryUse(file string, lines []line) []string {
	var findings []string
	sources := false
	for _, l := range lines {
		words := commandWords(l.text)
		if len(words) > 0 && (words[0] == "source" || words[0] == ".") && sourcesLibrary(file, l.text) {
			sources = true
		}
		if l.allowed {
			continue
		}
		for _, word := range words {
			if word == "curl" || word == "wget" {
				findings = append(findings, fmt.Sprintf("%s:%d runs %s, not a function of %s", file, l.n, word, pinLibrary))
				break
			}
		}
	}
	if !sources {
		findings = append([]string{fmt.Sprintf("%s does not source %s", file, pinLibrary)}, findings...)
	}
	return findings
}

// sourcesLibrary is true when a source line names the library by its
// path from the script's directory, as "$here/../../toolchains/pin.sh".
func sourcesLibrary(file, text string) bool {
	for _, field := range strings.Fields(text) {
		rest, ok := strings.CutPrefix(strings.Trim(field, `"'`), "$here/")
		if ok && path.Join(path.Dir(file), rest) == pinLibrary {
			return true
		}
	}
	return false
}

// shellSeparators end one command, so the word after one is a command.
var shellSeparators = []string{"|", "||", "&&", ";", "!", "{", "then", "do", "else", "if", "while", "until", "exec", "time"}

// assignment matches a variable assignment, which can precede the
// command that it sets the variable for.
var assignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// commandWords lists the words of a shell line that a shell runs as
// commands: the first word, the word after a separator, and the first
// word of each command substitution or subshell. A word inside a quoted
// message, such as "the digest that curl publishes", is not a command.
func commandWords(text string) []string {
	var words []string
	atCommand := true
	for _, field := range strings.Fields(text) {
		if i := strings.LastIndex(field, "$("); i >= 0 {
			words = append(words, strings.Trim(field[i+2:], `"'`))
			atCommand = false
			continue
		}
		if rest, ok := strings.CutPrefix(field, "("); ok {
			words = append(words, strings.Trim(rest, `"'`))
			atCommand = false
			continue
		}
		word := strings.Trim(field, `"'`)
		switch {
		case slices.Contains(shellSeparators, field) || strings.HasSuffix(field, ";"):
			atCommand = true
		case atCommand && assignment.MatchString(word):
		case atCommand:
			words = append(words, word)
			atCommand = false
		}
	}
	return words
}

// curlOutputFlags and wgetOutputFlags match short flags joined with
// the flag that names the output file, such as curl's -fsSLo.
var (
	curlOutputFlags = regexp.MustCompile(`^-[a-zA-Z]+o$`)
	wgetOutputFlags = regexp.MustCompile(`^-[a-zA-Z]+O$`)
	downloader      = regexp.MustCompile(`\b(curl|wget)\b`)
)

// downloadOutputs lists the file that each curl or wget in a command
// writes, without its quotes. A download to standard output that a
// pipe or a command substitution reads writes no file and is not
// listed. A download to the remote file name is listed as "".
func downloadOutputs(text string) []string {
	var outputs []string
	fields := strings.Fields(text)
	for i := 0; i < len(fields); i++ {
		tool := strings.TrimLeft(fields[i], `"$(`)
		if tool != "curl" && tool != "wget" {
			continue
		}
		out, toStdout := "", tool == "curl"
		for i++; i < len(fields) && !slices.Contains([]string{"|", "||", "&&", ";"}, fields[i]); i++ {
			field := fields[i]
			next := ""
			if i+1 < len(fields) {
				next = fields[i+1]
			}
			switch {
			case tool == "curl" && (field == "-o" || field == "--output" || curlOutputFlags.MatchString(field)),
				tool == "wget" && (field == "-O" || field == "--output-document" || wgetOutputFlags.MatchString(field)):
				out, toStdout = next, false
				i++
			case strings.HasPrefix(field, "--output=") || strings.HasPrefix(field, "--output-document="):
				_, out, _ = strings.Cut(field, "=")
				toStdout = false
			case tool == "wget" && strings.HasPrefix(field, "-O"):
				out, toStdout = strings.TrimPrefix(field, "-O"), false
			case tool == "curl" && (field == "-O" || field == "--remote-name"):
				out, toStdout = "", false
			case field == ">" || field == ">>":
				out, toStdout = next, false
				i++
			case strings.HasPrefix(field, ">"):
				out, toStdout = strings.TrimLeft(field, ">"), false
			}
		}
		if !toStdout {
			outputs = append(outputs, strings.Trim(out, `"'`))
		}
	}
	return outputs
}

// dockerfileFetches lists each ADD or RUN of a Dockerfile that
// downloads a checksum file, and reports whether the Dockerfile
// downloads anything: an ADD of a URL, or a RUN of curl or wget.
func dockerfileFetches(file string, lines []line) ([]string, bool) {
	var findings []string
	downloads := false
	for _, l := range lines {
		keyword := strings.ToUpper(strings.Fields(l.text)[0])
		if !strings.Contains(l.text, "://") || (keyword != "ADD" && !(keyword == "RUN" && downloader.MatchString(l.text))) {
			continue
		}
		downloads = true
		if !l.allowed && checksumFile.MatchString(l.text) {
			findings = append(findings, fmt.Sprintf("%s:%d reads a checksum file from upstream at build time; write its digest into the repository with latest.sh --bump", file, l.n))
		}
	}
	return findings, downloads
}

// reportedAbove is true when dir or a directory above it, below the
// root, holds a latest.sh.
func reportedAbove(dir string, reported map[string]bool) bool {
	for ; dir != "." && dir != "/"; dir = path.Dir(dir) {
		if reported[dir] {
			return true
		}
	}
	return false
}

// versionsSkips names the directories whose latest.sh versions.sh does
// not run. The list must match the find command in versions.sh, which
// leaves out caches, build output, and vendored packages.
var versionsSkips = []string{"cache", ".cache", "dist", "node_modules"}

// latestScript checks that versions.sh runs a latest.sh, and that a
// latest.sh that reads a sha256sums.asc of kernel.org verifies it.
func latestScript(root, file string, lines []line) ([]string, error) {
	var findings []string
	if slices.ContainsFunc(strings.Split(path.Dir(file), "/"), func(dir string) bool { return slices.Contains(versionsSkips, dir) }) {
		findings = append(findings, fmt.Sprintf("%s is under a directory that versions.sh skips, so make versions does not report it", file))
	}
	info, err := os.Stat(filepath.Join(root, file))
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0o111 == 0 {
		findings = append(findings, fmt.Sprintf("%s is not executable, so versions.sh cannot run it", file))
	}
	var text strings.Builder
	for _, l := range lines {
		text.WriteString(l.text + "\n")
	}
	if strings.Contains(text.String(), "sha256sums.asc") && !strings.Contains(text.String(), "kernel-org-digest.sh") {
		findings = append(findings, fmt.Sprintf("%s reads a sha256sums.asc file and does not verify it with trust/keys/kernel-org-digest.sh", file))
	}
	return findings, nil
}

// staticChecks lists each grep for "statically linked" that does not
// also accept "static-pie linked".
func staticChecks(file string, lines []line) []string {
	var findings []string
	for _, l := range lines {
		if strings.Contains(l.text, "grep") && strings.Contains(l.text, "statically linked") && !strings.Contains(l.text, "static-pie linked") {
			findings = append(findings, fmt.Sprintf("%s:%d accepts \"statically linked\" and refuses \"static-pie linked\"", file, l.n))
		}
	}
	return findings
}

// apkAdd matches a command that installs Alpine packages: apk add at the
// start of a command, after a separator, or after a Dockerfile's RUN.
var apkAdd = regexp.MustCompile(`(^|[;&|(]\s*|\bRUN\s+)apk\s+add\b`)

// apkInstalls lists each line of a script or a Dockerfile that installs
// Alpine packages with apk add.
func apkInstalls(file string, lines []line) []string {
	var findings []string
	for _, l := range lines {
		if !l.allowed && apkAdd.MatchString(strings.TrimSpace(l.text)) {
			findings = append(findings, fmt.Sprintf("%s:%d installs Alpine packages with apk add, which no digest pins; build the program with buildroot in liken/userland", file, l.n))
		}
	}
	return findings
}
