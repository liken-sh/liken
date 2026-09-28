# redirect

The service that keeps the old manual addresses working. Each
operator's API group, such as `bluetooth.liken.sh`, and each CSI
driver's name, such as `git.liken.sh`, is also the address of its
manual, and the manuals now live together on one site under
`liken.sh/<name>/`. This service answers every subdomain of liken.sh
that has no DNS record of its own with a 301 to that path, with the
same page and query:

    https://display.liken.sh/docs/guides/install/
      -> https://liken.sh/display/docs/guides/install/

It speaks plain HTTP on port 8080 and answers `/healthz` on any host
that is not a subdomain of liken.sh. Where it runs is an open question
in plan 69. The host in front of it must hold a wildcard certificate
for `*.liken.sh` and terminate TLS, and a wildcard DNS record in
`../terraform.tf` must point every such name at that host. Until then,
the old names still serve the manuals from the archived repositories'
GitHub Pages.

    make test          the tests and the coverage gate
    docker build .     the image, ghcr.io/liken-sh/redirect
