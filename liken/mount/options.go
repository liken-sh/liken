package main

// The translation half of mount(8): turning one comma list into the
// two arguments mount(2) actually takes.
//
// The kernel splits a filesystem's options in two, and the split is
// not principled. Some options are bits in a flag word that the VFS
// itself acts on, the same bits for every filesystem: read-only,
// no-setuid, no-execute, the times policy. The rest is an opaque
// string that the kernel hands to the filesystem driver, which
// parses it however it likes: vers=4.1 means something to the NFS
// client and nothing to ext4.
//
// Nobody writes options that way. A pod spec, a fstab line, and the
// kubelet all write one comma list that mixes the two kinds freely.
// Sorting that list into the flag word and the data string is what
// this file does, and it is most of what mount(8) is.

import (
	"strings"

	"golang.org/x/sys/unix"
)

// kernelFlags are the option names that become bits in mount(2)'s
// flag word. Each entry names the bit and says whether the option
// sets or clears it, because these names come in pairs: nosuid and
// suid are two names for one bit. The parser applies them in the
// order written, so the last name for a bit wins, which is the rule
// every mount command has followed.
//
// None of these names ever takes a value, so the parser matches the
// whole token rather than the part before an equals sign.
var kernelFlags = map[string]struct {
	bit uintptr
	set bool
}{
	"ro":            {unix.MS_RDONLY, true},
	"rw":            {unix.MS_RDONLY, false},
	"suid":          {unix.MS_NOSUID, false},
	"nosuid":        {unix.MS_NOSUID, true},
	"dev":           {unix.MS_NODEV, false},
	"nodev":         {unix.MS_NODEV, true},
	"exec":          {unix.MS_NOEXEC, false},
	"noexec":        {unix.MS_NOEXEC, true},
	"sync":          {unix.MS_SYNCHRONOUS, true},
	"async":         {unix.MS_SYNCHRONOUS, false},
	"dirsync":       {unix.MS_DIRSYNC, true},
	"atime":         {unix.MS_NOATIME, false},
	"noatime":       {unix.MS_NOATIME, true},
	"diratime":      {unix.MS_NODIRATIME, false},
	"nodiratime":    {unix.MS_NODIRATIME, true},
	"relatime":      {unix.MS_RELATIME, true},
	"norelatime":    {unix.MS_RELATIME, false},
	"strictatime":   {unix.MS_STRICTATIME, true},
	"nostrictatime": {unix.MS_STRICTATIME, false},
	"lazytime":      {unix.MS_LAZYTIME, true},
	"nolazytime":    {unix.MS_LAZYTIME, false},
	"mand":          {unix.MS_MANDLOCK, true},
	"nomand":        {unix.MS_MANDLOCK, false},
	"iversion":      {unix.MS_I_VERSION, true},
	"noiversion":    {unix.MS_I_VERSION, false},
	"nosymfollow":   {unix.MS_NOSYMFOLLOW, true},
	"symfollow":     {unix.MS_NOSYMFOLLOW, false},
	"silent":        {unix.MS_SILENT, true},
	"loud":          {unix.MS_SILENT, false},

	// These last names are not properties of a mount but operations
	// on one. The kernel takes them in the same flag word, so the
	// options list can carry them, and the kubelet's bind mounts do
	// exactly that. Everything that changes propagation is recursive
	// under its r-prefixed name, which is the only form anything
	// here uses.
	"remount":     {unix.MS_REMOUNT, true},
	"bind":        {unix.MS_BIND, true},
	"rbind":       {unix.MS_BIND | unix.MS_REC, true},
	"move":        {unix.MS_MOVE, true},
	"shared":      {unix.MS_SHARED, true},
	"rshared":     {unix.MS_SHARED | unix.MS_REC, true},
	"slave":       {unix.MS_SLAVE, true},
	"rslave":      {unix.MS_SLAVE | unix.MS_REC, true},
	"private":     {unix.MS_PRIVATE, true},
	"rprivate":    {unix.MS_PRIVATE | unix.MS_REC, true},
	"unbindable":  {unix.MS_UNBINDABLE, true},
	"runbindable": {unix.MS_UNBINDABLE | unix.MS_REC, true},
}

// userspaceOptions are the names that mount commands act on
// themselves and never pass down. They answer questions the kernel
// was never asked: whether a boot should mount this entry, who may
// unmount it, whether the network must be up first. A liken machine
// has no fstab and no unprivileged users, so every one of these is
// inert here. They are recognized anyway, because dropping them is
// the difference between an option a caller may harmlessly include
// and a mount that fails with an unknown option.
//
// The parser also drops any name beginning with x-, the reserved
// prefix for comments that tools keep in the mount table for
// themselves.
var userspaceOptions = map[string]bool{
	"defaults": true,
	"auto":     true,
	"noauto":   true,
	"nofail":   true,
	"_netdev":  true,
	"user":     true,
	"nouser":   true,
	"users":    true,
	"owner":    true,
	"group":    true,
	"comment":  true,
}

// splitOptions sorts a comma list into the flag word and the data
// string that mount(2) takes. Anything this file does not recognize
// is data, which is the correct default: an option missing from the
// table above is one the filesystem driver defines, and the driver
// is the one that must judge it.
func splitOptions(options string) (uintptr, string) {
	var flags uintptr
	var data []string
	for token := range strings.SplitSeq(options, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if entry, known := kernelFlags[token]; known {
			if entry.set {
				flags |= entry.bit
			} else {
				flags &^= entry.bit
			}
			continue
		}
		// An option may carry a value, and the name is the part
		// before the equals sign. Only the userspace list is
		// consulted by name, because every kernel flag above is a
		// bare word.
		name, _, _ := strings.Cut(token, "=")
		if userspaceOptions[name] || strings.HasPrefix(name, "x-") {
			continue
		}
		data = append(data, token)
	}
	return flags, strings.Join(data, ",")
}
