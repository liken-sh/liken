#!/bin/sh
# Copies git, ssh, and every file they open at runtime into the
# directory given as the argument, so that the image holds that tree
# and nothing else from Debian. Run it in a builder with git and
# openssh-client installed. closure.sh is vulkan/closure.sh, which the
# Dockerfile copies from the build context named builder.
set -eu

# shellcheck source=../vulkan/closure.sh
. "$(dirname "$0")/closure.sh"

out=$1

# collect copies each seed with the shared libraries that ldd lists for
# it. ldd does not list a program that another program starts by name,
# so each such program is a seed here, with the reason it is needed.
#
# /usr/bin/git is the program that runGit starts by name from PATH.
#
# /usr/lib/git-core/git is the git in git's exec path. git runs it again
# for the subcommands that a command starts: `git gc` runs maintenance,
# pack-objects, and rev-list, a fetch runs index-pack and rev-list, a
# push runs pack-objects, and a rebase runs reset and notes copy.
# Without it those commands fail with "git: 'maintenance' is not a git
# command". Debian ships it as a second copy of the git binary, which
# the script replaces with a link below.
#
# git-remote-https is the transport program for an https URL, which the
# store fetches from and a work tree pushes to. It is a link to
# git-remote-http, and collect copies both. Without it a fetch fails
# with "Unable to find remote helper for 'https'".
#
# git-upload-pack and git-receive-pack serve a fetch or a push when the
# URL is a local path. Both are links to the git in the exec path, so
# they add no file content.
#
# ssh is the transport for a deploy key. credentials.go builds
# GIT_SSH_COMMAND around it.
#
# /bin/sh is dash. git runs these through it: the credential helper
# that credentials.go writes, which is a `#!/bin/sh` script,
# GIT_SSH_COMMAND, and the `git-upload-pack '<path>'` command of a
# local-path fetch. Without a shell, a fetch with a token, with a key,
# or from a local path fails. Nothing in the pod specs runs the shell.
seeds="
/usr/bin/git
/usr/lib/git-core/git
/usr/lib/git-core/git-remote-https
/usr/lib/git-core/git-upload-pack
/usr/lib/git-core/git-receive-pack
/usr/bin/ssh
/bin/sh
"

# Data that git opens by path at runtime, which ldd cannot find. git
# init copies the templates directory into each new repository. The
# driver reads nothing from it, but without it every git init of the
# store and of each work tree prints "warning: templates not found in
# /usr/share/git-core/templates".
#
# The CA bundle that git-remote-http checks a forge's certificate with
# is not copied here. The Dockerfile copies it from the trust component
# to /etc/ssl/certs/ca-certificates.crt, the default path that Debian
# builds into libcurl, so git needs no GIT_SSL_CAINFO.
data="
/usr/share/git-core/templates
"

collect "$out" "$seeds" "$data"

# The exec-path git is byte-identical to /usr/bin/git, so a link
# replaces the second copy. The gc run in smoke/git-csi-driver.sh checks
# that git still finds its exec path through the link.
if cmp -s "$out/usr/bin/git" "$out/usr/lib/git-core/git"; then
	ln -sf ../../bin/git "$out/usr/lib/git-core/git"
fi

# ssh looks up the uid it runs as and refuses to start when /etc/passwd
# has no entry for it, with "No user exists for uid". The manifests in
# deploy/ set no runAsUser, so the pods run as root. The nobody entry
# covers a cluster that sets runAsUser to 65534. Nothing in the image
# writes to a home directory, so the home directory is /.
cat >"$out/etc/passwd" <<'PASSWD'
root:x:0:0:root:/:/bin/sh
nobody:x:65534:65534:nobody:/:/bin/sh
PASSWD

# The passwd entries refer to gids, so the image needs a group file.
cat >"$out/etc/group" <<'GROUP'
root:x:0:
nogroup:x:65534:
GROUP

notices "$out" git-csi-driver
