# kubernetes

The Go module that the `liken` operators share to read, write, and
watch Kubernetes objects. Each operator imports it through a `replace`
directive that points at this directory, so the operators build
against the code in the tree and no version of the module is
published.

- `apiclient` sends each read and write as one HTTPS request with a
  JSON body. It reads the pod's ServiceAccount token on each request,
  separates a `404` and a `409` from a failure, and sends a request
  again after the wait that a `429` asks for. It imports nothing from
  `k8s.io`, so a program that must not link client-go can use it.
- `memo` records the `resourceVersion` of the newest copy of each
  object that an operator wrote or read, so a pass does not act on a
  copy in a watch's store that is older than its own write. It also
  sends the requests whose answers it records: a read of one object, a
  write, and a status write that settles on the API server's copy after
  a `409`. It imports nothing from `k8s.io` either, so the pod build of
  `library-operator` can link it.
- `informer` watches a collection through client-go's reflector and
  keeps a copy of it, and answers a pass's reads from that copy. A
  watch can read a kind that another operator defines, and that the
  cluster does not serve, as an empty collection until it arrives. It
  links only `tools/cache`, `dynamic`, and `rest` from client-go.

The `operators` skill in `.agents/skills` at the top of the repository
gives the rules each watch must follow and the reasons for the
reflector. `make test` runs every check CI runs.
