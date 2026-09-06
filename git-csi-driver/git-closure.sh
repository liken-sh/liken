#!/bin/sh
# Collects git, ssh, and every file the two of them open at runtime
# into one directory tree, so the image ships that tree and nothing
# else. Run it in a builder that has git, openssh-client, and
# ca-certificates installed, with the output directory as the
# argument. audio-operator's audio-closure.sh is the same shape for
# the same reason.
set -eu

out=$1

# The multiarch directory that holds every library below, read from
# dpkg so that no architecture is written down here.
lib=$(dirname "$(dpkg -L libc6 | grep '/libc\.so\.6$')")

# ldd reports the DT_NEEDED graph and nothing about a program that a
# running program execs by name. Every seed below is such a file, and
# each one is named with the driver command that reaches it.
#
# /usr/bin/git is the program runGit starts, by name, so it has to be
# on the PATH the container gets.
#
# /usr/lib/git-core/git is git's copy of itself on its exec path. git
# re-execs it as `git <subcommand>` for the work a command farms out:
# `git gc` runs maintenance, pack-objects, and rev-list that way, a
# fetch runs index-pack and rev-list, a push runs pack-objects, and a
# rebase runs reset and notes copy. Without it every one of those
# commands fails with "git: 'maintenance' is not a git command".
# Debian ships it as a second copy of the 4 MB binary. The closure
# replaces the copy with a link below.
#
# git-remote-https is the transport program for an https URL, which
# is what the store's fetch and the work tree's push run against a
# forge. It is a link to git-remote-http, the real binary, and the
# hops walk below copies both. Without it a fetch fails with "Unable
# to find remote helper for 'https'".
#
# git-upload-pack and git-receive-pack serve a fetch and a push whose
# URL is a local path. git runs each one through a shell. Both are
# links to git on the exec path, so they cost two links and no bytes.
#
# ssh is the transport a deploy key uses. credentials.go builds
# GIT_SSH_COMMAND around it.
#
# /bin/sh is dash, and git runs three things through it: the
# credential helper credentials.go writes, which is a `#!/bin/sh`
# script; GIT_SSH_COMMAND, which git always passes to a shell; and
# the local-path transport's `git-upload-pack '<path>'` line. Without
# it a token fetch, a key fetch, and a local-path fetch all fail. The
# image holds a shell for that reason and no other. Nothing in the
# pod spec runs one.
seeds="
/usr/bin/git
/usr/lib/git-core/git
/usr/lib/git-core/git-remote-https
/usr/lib/git-core/git-upload-pack
/usr/lib/git-core/git-receive-pack
/usr/bin/ssh
/bin/sh
"

# Two paths a program opens by name at runtime, so nothing in the
# library graph points at them.
#
# The CA bundle is what libcurl-gnutls, which git-remote-http links,
# verifies a forge's certificate against. Debian builds this path in
# as libcurl's default, so git needs no GIT_SSL_CAINFO to find it.
#
# The templates directory is what git init copies into a new
# repository. The driver reads none of what it holds, and commit.go
# writes info/exclude itself, but git init prints "warning: templates
# not found in /usr/share/git-core/templates" without it, and the
# driver runs git init for the store and again for every work tree.
# The 88 kB buys a clean report from the command a volume starts with.
data="
/etc/ssl/certs/ca-certificates.crt
/usr/share/git-core/templates
"

# hops prints every link of a symlink chain and then the file at the
# end, because a soname is a link to a versioned file and the loader
# opens the soname.
hops() {
	path=$1
	while [ -L "$path" ]; do
		printf '%s\n' "$path"
		target=$(readlink "$path")
		case $target in
		/*) path=$target ;;
		*) path=$(dirname "$path")/$target ;;
		esac
	done
	printf '%s\n' "$path"
}

# needed prints the libraries ldd resolves for one file, which is the
# whole DT_NEEDED graph. linux-vdso has no file behind it, and the
# loader's own line prints with no arrow, which is what the two sed
# patterns accept.
needed() {
	ldd "$1" | sed -n 's/.*=> \(\/[^ ]*\).*/\1/p; s/^\t\(\/[^ ]*\) (0x.*/\1/p'
}

# /bin, /lib, and /lib64 are symlinks into /usr. ldd reports every
# library under the name it resolved through them, the loader's own
# path names /lib64, and the seeds name /bin/sh. The three links go in
# first, so every copy below writes the path exactly as it was
# resolved.
mkdir -p "$out$lib" "$out/usr/lib64" "$out/usr/bin"
for link in /bin /lib /lib64; do
	if [ -L "$link" ]; then
		cp -a --parents "$link" "$out"
	fi
done

for seed in $seeds; do
	{
		hops "$seed"
		needed "$(readlink -f "$seed")" | while read -r path; do hops "$path"; done
	} >>"$out/.closure"
done
sort -u "$out/.closure" | while read -r path; do
	cp -a --parents "$path" "$out"
done
rm -f "$out/.closure"

for path in $data; do
	cp -a --parents "$path" "$out"
done

# The exec-path git is byte for byte the /usr/bin one, so a link
# replaces the second 4 MB copy. git resolves its exec path through
# the link the same way, which the release gate's gc run proves.
if cmp -s "$out/usr/bin/git" "$out/usr/lib/git-core/git"; then
	ln -sf ../../bin/git "$out/usr/lib/git-core/git"
fi

mkdir -p "$out/etc"

# ssh looks up the uid it runs as and refuses to start when no entry
# answers, with "No user exists for uid". The manifests in deploy/
# name no runAsUser, so both pods run as root, and 65534 is the uid a
# cluster owner sets first when it sets one. Two lines cover both. The
# home directory is / because nothing in the image writes under a
# home, and the shell is the one the image ships.
cat >"$out/etc/passwd" <<'PASSWD'
root:x:0:0:root:/:/bin/sh
nobody:x:65534:65534:nobody:/:/bin/sh
PASSWD

# The group file beside it, because a passwd entry names a gid.
cat >"$out/etc/group" <<'GROUP'
root:x:0:
nogroup:x:65534:
GROUP

# Without a cache the loader searches its built-in directory list on
# every open, and that list does not name the multiarch directory
# that holds every library above.
printf '%s\n' "$lib" >"$out/etc/ld.so.conf"
ldconfig -r "$out"
