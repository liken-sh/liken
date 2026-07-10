# Releases on the website

Milestone 26 — Not started

Public releases (milestone 22) need a public home, and liken.sh
(milestone 25) is it: the place a person downloads a release of liken
itself, verifies it, and learns what changed.

The shape is already in the code, which is the encouraging part. A
deployment's fleet consumes releases from a plain HTTP directory (a
catalog document plus digest-verified artifacts, releases/serve is
the whole server), and a public channel wants the same shape one
layer up: a catalog of liken's own releases, each entry a digest, so
that verification is the same story whether a fleet or a person is
doing the downloading. Publishing should be CI's job (milestone 24),
because a release someone's laptop assembled is exactly what the
digest discipline exists to rule out.

Open questions, deliberately unanswered here: where the bytes live
(the site's own hosting, or the forge's release storage with the site
as the index); what a release page owes the reader beyond digests —
changelogs, and whether those are written or derived; and signatures,
which stay deferred with the hardening tier but will land exactly
here when they come.
