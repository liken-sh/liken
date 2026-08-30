# Watch state and people

Plan 14. A stub for a later agent to shape. It adds what plans 01 to 09
leave out: positions, history, and who was watching.

## The problem

Every `Play` starts at the beginning, and nothing records that a film
was half watched or that a season is on episode four. Jellyfin gives
each person their own history today, and the children having their own
is a feature the design keeps. A person is a fact of the whole cluster:
a `Remote` may belong to one, and lighting or presence may name one.

## The shape

- The library operator records what a `Play` reached when it ended, from
  the `Play` status: the item index plan 08 added and the position
  inside it. It records that per `Player` first, which needs no notion
  of people.
- Per person, it needs a cluster-wide `Person` resource, owned by an
  operator of its own: a display name, an avatar, a flag for a child,
  and later a link to an identity provider. A screen takes the person
  from a picker or from a default per room, and the media browser puts
  the person on its `Play` request.
- Where the state is stored is open. The catalog is a multi-writer store
  with every screen as a member, which is the shape watch state has:
  written from rooms, read everywhere. A table in the same cluster would
  store it with no new infrastructure. dqlite was weighed for this shape
  and set aside; see [`rejected/dqlite.md`](rejected/dqlite.md).

## What is not decided

The `Person` operator's home and name. Whether a screen that writes
watch state breaks the rule that a screen never writes the catalog, or
whether watch state is a second cluster with its own rule. The retention
of history, and how "continue watching" classifies a film as finished.