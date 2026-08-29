# Watch state and people

Plan 14. A stub, at low fidelity, for a later agent to shape. It
adds what the first nine plans leave out on purpose: positions,
history, and who was watching.

## The problem

Every `Play` starts at the beginning, and nothing remembers that a
film was half watched or that a season is on episode four. Jellyfin
gives each person their own history today, and the children having
their own is a real feature the design must not lose. But a person is
a fact of the whole cluster, not of one operator: a `Remote` may
belong to one, and lighting or presence may name one later.

## The shape

- The library operator records what a `Play` reached when it ended,
  from the `Play` status: the item index this design added in plan 08
  and the position within it. It keeps that per `Player` first, which
  needs no notion of people.
- Per person, it needs a cluster-wide `Person` resource, owned by an
  operator of its own and not by this one: a display name, an avatar,
  a flag for a child, and later a link to an identity provider. A
  screen is told who is watching by a picker, or from a default per
  room, and the browser stamps the person on its request for a
  `Play`.
- Where the state lives is open. The catalog is a multi-writer store
  with every screen as a member, which is the shape watch state has:
  written from rooms, read everywhere. A separate table in the same
  cluster would carry it with no new infrastructure. dqlite was also
  weighed for this exact shape and set aside; see
  [`rejected/dqlite.md`](rejected/dqlite.md).

## What is not decided

The `Person` operator's home and name. Whether a screen's writes to
the catalog break the "a screen never writes" rule or whether watch
state is a second cluster with its own rule. The retention of
history, and how "continue watching" decides that a film is finished.
