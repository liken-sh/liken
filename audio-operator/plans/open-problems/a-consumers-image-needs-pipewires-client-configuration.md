# A consumer's image needs PipeWire's client configuration

Open problem. A consumer image that carries only `libpipewire-0.3-0`
cannot open the socket this operator delivers, and nothing this
operator publishes says so.

## What happens

`libpipewire` refuses to build a client context without
`/usr/share/pipewire/client.conf`. Debian ships that file in
`pipewire-bin`, which is the daemon package, and not in the library
package. A consumer image that installs the library alone fails with:

    can't load config client.conf: No such file or directory

The client never opens the socket. The delivery is correct and unused.

## How it was found

An mpv image hit this on 2026-08-17. The container had the right
`PIPEWIRE_REMOTE`, the right `PIPEWIRE_NODE`, and the socket mounted at
the path the variable named, and it still could not play. The first
suspicion fell on the delivery, because the delivery is the new thing
and the consumer's packaging is not. The delivery was correct in every
part.

The cost of this open problem is that debugging time, and the next
consumer's author pays it again.

## The open question

Whether this operator should surface the requirement somewhere a
consumer's author meets it. Three candidates, none chosen:

* A line in the README's "What a consumer receives" section, beside the
  two environment variables, which is where an author reads what the
  claim delivers.
* A note in the `audio-output` DeviceClass, which travels with the
  cluster instead of with the repository.
* Nothing beyond this document, on the argument that a consumer's own
  packaging is the consumer's problem and this operator publishes no
  claim about it.
