# The playback pod reports over plain HTTP

The supervisor in the playback pod tells the operator what mpv is
doing with one plain HTTP request. Every few seconds, and on every
pause or item change, it POSTs a small JSON body to the operator's
`/report` endpoint: the Play's namespace and name, a token, and the
paused flag, item, position, and duration. The operator runs an HTTP
server for this on port 8080, behind a Service. There is no TLS and
no authorization header. The token in the body is the whole of the
proof: the operator mints it, writes it into the pod's environment,
holds it in memory, and accepts a report only when the two match.

This shape follows from the trust boundary, and that part should
stay. The playback pod decodes media pulled off the network, so it
is the least trusted process in the system, and it holds no
Kubernetes credentials at all. Only the operator writes a Play's
status. A stolen token can misreport one film's position; it can
never reach the cluster API.

What should not stay is the transport. The report is a second data
plane running beside the one the founding design already names for
input: a message bus, likely MQTT, that carries button and axis
events between a standing remote pod and the receiver in the playback
pod. Reports belong on that same bus. The supervisor would publish
its observations to a topic, and the operator would subscribe,
instead of each pod POSTing to an endpoint the operator has to serve.

Two costs of the plain-HTTP shape go away when the bus arrives. The
operator holds every pod's token in memory, so a restarted operator
has to read each running pod's environment back to accept its reports
again, and that recovery is unproven: a drill saw a film's position
stop advancing across an operator restart while mpv kept playing. And
the operator serves an HTTP endpoint whose only client is its own
pods, which is a listener and a Service that exist for one message
type. A broker the input plane already runs would carry the reports
with neither the in-memory token table nor the endpoint.

The bus is not built yet, and neither is the input plane it serves.
When that plan lands, the report belongs in it. Until then the plain
POST is what the operator hears, and the restart recovery is the part
of it that most needs a test.
