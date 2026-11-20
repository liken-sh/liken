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
Code that only one operator uses stays in that operator. A hook is a
point where a shared function calls code that its caller supplies,
such as an option of the function. A hook cannot work outside its
package, so it comes here even when only one operator uses it. The
code that the hook runs stays in the operator.

The module is split into small packages because some programs that
import it run in many pods at once. An example is the pod build of
`library-operator`, the program that each of its pods and `Job`s runs.
The small packages link no `k8s.io` package, or only small parts of
client-go, so such a program stays small. `make test` fails when a small
package links a `k8s.io` package outside its limit. The comments in
`Makefile` give each limit and its reason.

Load the `operators` skill before you change a watch, the store that
a reconcile pass reads, or `memo`. A reconcile pass is one run of an
operator's loop that compares the declared state with the cluster.
