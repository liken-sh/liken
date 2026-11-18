# Tests that do not test the case they name

Open problem, recorded on 2026-11-18. The tests below pass today, and
the code that they cover may be correct. A review of the comments
against the code found that each test checks less than its name says.

## The audience test fakes a response that an API server does not send

`TestATokenMintedForAnotherAudienceIsRefused` in
`audio-operator/tokenreview_test.go` fakes a TokenReview response with
`authenticated` true and `status.audiences` holding
`https://kubernetes.default.svc`, an audience that the request did not
name. Upstream Kubernetes defines `status.audiences` as the audiences
that the request's `spec.audiences` and the token have in common, so a
real API server returns an empty list for a token minted for another
audience. The test's own comment says so.

`reviewer.review` refuses both responses, because it checks that
`status.audiences` contains the reviewer's audience. No test in the
file fakes the response that a real API server sends: `authenticated`
true with an empty `status.audiences`. A change to `review` that
accepted an empty list would pass every test.

## An `errors.Is` check that is always true

`apitls_test.go` checks the result of `expiryOf` on a certificate that
does not parse with `!errors.Is(err, err) || err == nil`.
`errors.Is(err, err)` is true for every non-nil error, so the check
reduces to `err == nil`. It does not check which error `expiryOf`
returns.

## What would confirm it

Both faults are in the test's code. For the audience test, a case
with an empty `status.audiences` shows whether `review` refuses the
response that a real API server sends.

## What a fix has to decide

For the audience test, whether the case with another audience stays
beside the faithful case, as a check that `review` does not trust an
authenticator that ignores `spec.audiences`. For `apitls_test.go`,
which error `expiryOf` returns for a certificate that does not parse,
so that the test can name it.
