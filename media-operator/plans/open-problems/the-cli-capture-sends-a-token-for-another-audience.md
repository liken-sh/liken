# The CLI capture sends a token for another audience

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`runCapture` in `cli/capture.go` reads the bearer token of the current
kubeconfig context with `bearerToken` in `cli/cluster.go` and sends it
to `media-api` unchanged. `media-api` accepts a token only when the
`TokenReview` returns `media-api` in `status.audiences` (`apiAudience`
and `authenticate` in `apiauth.go`). A kubeconfig token is minted for
the API server's audience, or, from an authenticator without
audiences, comes back with none. So a person whose kubeconfig
authenticates with a token gets 401 from `kubectl liken media
capture`, and only a client certificate works.

The record guide says the CLI "sends the bearer token or the client
certificate of your current kubeconfig context, so it records as the
same user as `kubectl`".

A kubeconfig that authenticates through an exec plugin leaves
`BearerToken` and `BearerTokenFile` empty in the REST config, so the
CLI sends no token at all and stops with its "neither a bearer token
nor a client certificate" error.

## Evidence

- `cli/cluster.go`, `bearerToken`: returns `config.BearerToken` or the
  contents of `config.BearerTokenFile`.
- `apiauth.go`, `authenticate`: refuses a review without `media-api`
  in `status.audiences`.

## What would confirm it

A capture with a kubeconfig whose user is a token from `kubectl
create token <account>`, with no `--audience`. It returns 401.

## What a fix has to decide

Whether the CLI asks the API server for a token for `media-api` with a
`TokenRequest` for the caller's own `ServiceAccount`, which only a
`ServiceAccount` user can do, or whether the guide states that the CLI
works only with a client certificate. The exec-plugin case needs the
same decision.
