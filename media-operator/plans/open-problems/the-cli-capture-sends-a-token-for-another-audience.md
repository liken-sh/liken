# The CLI capture sends a token for another audience

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
On 2026-11-19 the code of the CLI was checked, and the paragraph about
an exec plugin was corrected. No unit test can show the 401, because
it comes from the audience check of the real API server's
`TokenReview`, and a fake would answer only what the test assumes. A
capture on the `lab` fleet, as the last section describes, would show
it.

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
CLI sends no token. It does not stop with its "neither a bearer token
nor a client certificate" error. `clientCertificate` in
`cli/cluster.go` builds the TLS configuration with `rest.TLSConfigFor`,
and client-go v0.37.2 sets a certificate callback for every exec
plugin (`UpdateTransportConfig` in `plugin/pkg/client/auth/exec/exec.go`).
So `GetClientCertificate` is set, and the CLI connects with that
callback and no token. A plugin that returns only a token gives the
TLS handshake no certificate, and `media-api` then has nothing to
authenticate.

## Evidence

- `cli/cluster.go`, `bearerToken`: returns `config.BearerToken` or the
  contents of `config.BearerTokenFile`.
- `apiauth.go`, `authenticate`: refuses a review without `media-api`
  in `status.audiences`.

## What would confirm it

A capture with a kubeconfig whose user is a token from `kubectl
create token <account>`, with no `--audience`. It returns 401.

## What a fix has to decide

A person whose kubeconfig signs in with a token, or with an exec plugin
that returns only a token, cannot record with `kubectl liken media
capture`.

Whether the CLI asks the API server for a token for `media-api` with a
`TokenRequest` for the caller's own `ServiceAccount`, which only a
`ServiceAccount` user can do, or whether the guide states that the CLI
works only with a client certificate. The exec-plugin case needs the
same decision.
