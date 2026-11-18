# The crawl test keeps a `<code>` element before a `<code>` with attributes

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

`outside_links` in `crates/transect/src/web/crawl.rs` removes the
`<a>`, `<code>`, and `<title>` elements of a page with `without`, so
the crawl test can check which names a page shows outside links.
`without` looks for `"{open} "` anywhere in the rest of the text first,
and tries `"{open}>"` only when there is none. It does not take the
earlier of the two.

So in `<code>x</code> … <code class="y">z</code>`, `without` finds the
second element first, copies everything before it into the result, and
the first `<code>x</code>` stays in the text. A name inside that
element then counts as shown outside links, and the test can pass on a
page that it should fail.

## What would confirm it

A unit test of `without` with the text above and the tag `<code`. The
result still contains `x`.

## What a fix has to decide

Nothing beyond taking the earlier match of the two forms.
