# Working on kubernetes

`kubernetes/` is the Go module `github.com/liken-sh/liken/kubernetes`,
which the operators import. `README.md` describes its packages.

## How it runs

The module runs inside each program that imports it. Each operator
builds against the code in this tree, so a change here affects every
operator that imports the changed package, in the same commit. When
you change a package, run `make test` here and the tests of each
operator that imports the package.

## Design

Code goes in this module when two operators need it in the same form.
Code that only one operator uses stays in that operator. A hook that
cannot work outside its package, such as an option of a shared
function, is the exception: it comes here even when only one operator
uses it. The code that the hook runs stays in the operator.

The module is split into small packages because some programs that
import it run in many pods at once, such as the pod build of
`library-operator`. Linking client-go more than doubles the size of
such a binary and the memory that it uses at start. So `make test`
fails when a small package links a `k8s.io` package outside its
limit, and the comments in `Makefile` give each limit and its reason.

Load the `operators` skill before you change a watch, the store that
a pass reads, or `memo`.
