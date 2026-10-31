# A freshly minted admin credential can answer 401

Open bug, cause not found. `liken plugins sync <deployment-dir>` failed
once with `401 Unauthorized` on its first request, and the same command
succeeded when it ran again 25 seconds later. Six runs after that, each
with a new credential, all succeeded.

## What the command does

Every command that talks to the cluster calls `writeKubeconfig` in
`cli/cluster.go` first: `plugins`, `request`, `approve`, and the
commands in `cli/cluster.go` and `cli/main.go`. It calls
`identity.Kubeconfig` in `identity/kubeconfig.go`, which makes a new
P-256 key and signs an `admin` certificate in `system:masters` with the
client CA, and writes both into `<deployment-dir>/identity/kubeconfig`.
The certificate's `NotBefore` is `time.Now()` on the workstation, and
its `NotAfter` is 365 days later. The command then sends its first
request with that credential within the same second.

## Evidence

The failing run, on release 2026.10.30-001:

```
liken: GET /apis/apps/v1/deployments?labelSelector=cli.liken.sh%2Fplugin: 401 Unauthorized: {"kind":"Status","apiVersion":"v1","metadata":{},"status":"Failure","message":"Unauthorized","reason":"Unauthorized","code":401}
```

The credential that run wrote was valid when it was read 25 seconds
later. `openssl verify` against `identity/tls/client-ca.crt` answered
`OK`, its `notBefore` was the second of the failing run, and `kubectl
--kubeconfig identity/kubeconfig get namespace default` succeeded with
it. The client CA in the identity directory has the same SHA-256
fingerprint as the `client-ca-file` in the cluster's
`kube-system/extension-apiserver-authentication` `ConfigMap`.

The cluster's `spec.endpoint` names one leader's address, not a virtual
IP, so every request went to the same API server.

## What it is not, as far as the evidence goes

A clock difference is the first explanation for a certificate that
fails in its first second and works later, because the API server
refuses a certificate whose `NotBefore` is after its own clock. The
clocks do not support it. The workstation was 41 ms from its NTP
server, the API server's `Date` header matched the workstation's clock
to the second, and every machine's clock was within 50 ms of the
others. An X.509 time holds whole seconds, so `NotBefore` is cut down to
the second, which makes it earlier, not later.

## What would settle it

- The API server's log line for the refused request. Its authenticator
  logs why it refused a client certificate, such as `x509: certificate
  has expired or is not yet valid` or `certificate signed by unknown
  authority`. That line was not collected.
- A loop that runs `liken plugins list <deployment-dir>` many times and
  records each status, to find out how often it fails.

## A change worth making either way

Setting `NotBefore` a few minutes before `time.Now()` is common for a
certificate that is used at once. It costs nothing, and it removes clock
difference as a cause on a cluster whose clocks do drift. It would not
fix this failure if the cause is something else, so it does not close
this problem.
