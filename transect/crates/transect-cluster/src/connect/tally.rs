//! Counts the requests that `transect` sends to the API server, by verb
//! and resource, so that its log shows the load it puts on the server.
//!
//! The count wraps the connections, below the retry layer, so a request
//! that gets a 429 and its retry count as two, as the server counts them.
//! The count of the first minute is logged at info, because it includes
//! the lists that `transect` sends at startup. Each later minute is
//! logged at debug, and on a quiet cluster it should contain only
//! watches that reconnect.

use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};
use std::task::{Context, Poll};
use std::time::Duration;

use futures::future::BoxFuture;
use http::{Method, Request, Response};

/// How often the count is logged and reset.
const LOG_INTERVAL: Duration = Duration::from_secs(60);

/// The verb and resource of a request. The verb is the RBAC verb, so a
/// list with the `watch` parameter set is `watch`. The resource is written as
/// kubectl writes it, with the group after a dot. A discovery path is
/// `discovery`, followed by the group when it has one.
#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub struct Operation {
    pub verb: String,
    pub resource: String,
}

/// The requests since the count was last taken, by operation, and how
/// many responses were 429.
#[derive(Default)]
struct Counts {
    operations: BTreeMap<Operation, u64>,
    throttled: u64,
}

/// The count of requests, shared by every clone of the service.
#[derive(Clone, Default)]
pub struct Tally {
    counts: Arc<Mutex<Counts>>,
}

impl Tally {
    /// Wraps `connections` in a service that counts each request.
    pub fn around<S>(&self, connections: S) -> Counting<S> {
        Counting {
            inner: connections,
            tally: self.clone(),
        }
    }

    /// Logs and resets the count every minute. `streams` returns how
    /// many HTTP/2 streams are open on each connection, or `None` when
    /// the client does not pool connections that way.
    pub fn log(self, streams: impl Fn() -> Option<Vec<usize>> + Send + 'static) {
        tokio::spawn(async move {
            let mut first = true;
            let mut interval =
                tokio::time::interval_at(tokio::time::Instant::now() + LOG_INTERVAL, LOG_INTERVAL);
            loop {
                interval.tick().await;
                let (operations, throttled) = self.take();
                let total: u64 = operations.iter().map(|(_, count)| count).sum();
                let line = summary(&operations);
                let streams = streams();
                if first {
                    tracing::info!(
                        total,
                        busy = throttled,
                        operations = line,
                        ?streams,
                        "requests to the API server in the first minute"
                    );
                } else {
                    tracing::debug!(
                        total,
                        busy = throttled,
                        operations = line,
                        ?streams,
                        "requests to the API server in the last minute"
                    );
                }
                first = false;
            }
        });
    }

    fn count(&self, operation: Operation) {
        let mut counts = self.counts.lock().unwrap();
        *counts.operations.entry(operation).or_default() += 1;
    }

    fn throttled(&self) {
        self.counts.lock().unwrap().throttled += 1;
    }

    /// Returns and resets the counts and the number of 429 responses,
    /// with the most frequent operation first.
    fn take(&self) -> (Vec<(Operation, u64)>, u64) {
        let counts = std::mem::take(&mut *self.counts.lock().unwrap());
        let mut operations: Vec<(Operation, u64)> = counts.operations.into_iter().collect();
        operations.sort_by(|(a, one), (b, other)| other.cmp(one).then(a.cmp(b)));
        (operations, counts.throttled)
    }
}

/// Formats the counts as one line, such as `watch pods=1, get pods/log=3`.
fn summary(operations: &[(Operation, u64)]) -> String {
    let each = operations.iter().map(|(operation, count)| {
        let Operation { verb, resource } = operation;
        format!("{verb} {resource}={count}")
    });
    each.collect::<Vec<_>>().join(", ")
}

/// The operation of a request. A path outside the Kubernetes API is
/// counted under its first segment, such as `/v2` for the image registry
/// that k3s serves on the API server's port.
pub fn operation(method: &Method, path: &str, query: Option<&str>) -> Operation {
    let parts: Vec<&str> = path.trim_matches('/').split('/').collect();
    let (group, rest) = match parts.as_slice() {
        ["api", _version, rest @ ..] => ("", rest),
        ["apis", group, _version, rest @ ..] => (*group, rest),
        ["api" | "apis", ..] => ("", &[][..]),
        [first, ..] => {
            return Operation {
                verb: method.as_str().to_lowercase(),
                resource: format!("/{first}"),
            };
        }
        [] => ("", &[][..]),
    };
    let rest = match rest {
        ["namespaces", _, resource, rest @ ..] => [&[*resource][..], rest].concat(),
        rest => rest.to_vec(),
    };
    let watch = query
        .into_iter()
        .flat_map(|query| query.split('&'))
        .any(|pair| pair == "watch=true" || pair == "watch=1");
    let (resource, named) = match rest.as_slice() {
        [] => ("discovery".to_string(), false),
        [resource] => (resource.to_string(), false),
        [resource, _name] => (resource.to_string(), true),
        [resource, _name, subresource, ..] => (format!("{resource}/{subresource}"), true),
    };
    let resource = match group {
        "" => resource,
        group if rest.is_empty() => format!("{resource} {group}"),
        group => format!("{resource}.{group}"),
    };
    let verb = match *method {
        Method::GET if watch => "watch",
        Method::GET if rest.is_empty() || named => "get",
        Method::GET => "list",
        ref other => {
            return Operation {
                verb: other.as_str().to_lowercase(),
                resource,
            };
        }
    };
    Operation {
        verb: verb.into(),
        resource,
    }
}

#[derive(Clone)]
pub struct Counting<S> {
    inner: S,
    tally: Tally,
}

impl<S, B, R> tower::Service<Request<B>> for Counting<S>
where
    S: tower::Service<Request<B>, Response = Response<R>>,
    S::Future: Send + 'static,
{
    type Response = S::Response;
    type Error = S::Error;
    type Future = BoxFuture<'static, Result<Self::Response, Self::Error>>;

    fn poll_ready(&mut self, cx: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
        self.inner.poll_ready(cx)
    }

    fn call(&mut self, request: Request<B>) -> Self::Future {
        let uri = request.uri();
        self.tally
            .count(operation(request.method(), uri.path(), uri.query()));
        let tally = self.tally.clone();
        let pending = self.inner.call(request);
        Box::pin(async move {
            let response = pending.await?;
            if response.status() == http::StatusCode::TOO_MANY_REQUESTS {
                tally.throttled();
            }
            Ok(response)
        })
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::a_list_across_namespaces("GET", "/api/v1/pods", Some("limit=500"), "list", "pods")]
    #[case::a_watch(
        "GET",
        "/api/v1/pods",
        Some("watch=true&resourceVersion=1"),
        "watch",
        "pods"
    )]
    #[case::a_watch_as_kube_sends_it(
        "GET",
        "/apis/apps/v1/deployments",
        Some("allowWatchBookmarks=true&watch=1"),
        "watch",
        "deployments.apps"
    )]
    #[case::a_list_in_a_namespace(
        "GET",
        "/apis/apps/v1/namespaces/shop/deployments",
        None,
        "list",
        "deployments.apps"
    )]
    #[case::one_object(
        "GET",
        "/apis/example.io/v1/namespaces/shop/widgets/a",
        None,
        "get",
        "widgets.example.io"
    )]
    #[case::a_subresource(
        "GET",
        "/api/v1/namespaces/shop/pods/web-1/log",
        Some("tailLines=5"),
        "get",
        "pods/log"
    )]
    #[case::the_namespaces("GET", "/api/v1/namespaces", Some("watch=true"), "watch", "namespaces")]
    #[case::one_namespace("GET", "/api/v1/namespaces/shop", None, "get", "namespaces")]
    #[case::a_cluster_scoped_object(
        "GET",
        "/apis/rbac.authorization.k8s.io/v1/clusterroles/view",
        None,
        "get",
        "clusterroles.rbac.authorization.k8s.io"
    )]
    #[case::the_core_group("GET", "/api/v1", None, "get", "discovery")]
    #[case::a_group("GET", "/apis/apps/v1", None, "get", "discovery apps")]
    #[case::every_group("GET", "/apis", None, "get", "discovery")]
    #[case::the_core_versions("GET", "/api", None, "get", "discovery")]
    #[case::a_registry_on_the_api_servers_address(
        "GET",
        "/v2/example/app/manifests/v1",
        Some("ns=registry.example.io"),
        "get",
        "/v2"
    )]
    #[case::a_post(
        "POST",
        "/apis/authorization.k8s.io/v1/selfsubjectrulesreviews",
        None,
        "post",
        "selfsubjectrulesreviews.authorization.k8s.io"
    )]
    fn finds_the_verb_and_resource_of_a_request(
        #[case] method: &str,
        #[case] path: &str,
        #[case] query: Option<&str>,
        #[case] verb: &str,
        #[case] resource: &str,
    ) {
        let method = Method::from_bytes(method.as_bytes()).unwrap();
        let expected = Operation {
            verb: verb.into(),
            resource: resource.into(),
        };
        assert_eq!(operation(&method, path, query), expected);
    }

    #[rstest]
    fn takes_the_most_frequent_first_and_empties_the_count() {
        let tally = Tally::default();
        let list = operation(&Method::GET, "/api/v1/pods", None);
        let watch = operation(&Method::GET, "/api/v1/pods", Some("watch=1"));
        tally.count(watch.clone());
        tally.count(list.clone());
        tally.count(list.clone());
        tally.throttled();
        assert_eq!(tally.take(), (vec![(list, 2), (watch, 1)], 1));
        assert_eq!(tally.take(), (vec![], 0));
    }

    #[rstest]
    fn writes_the_count_on_one_line() {
        let operations = [
            (operation(&Method::GET, "/api/v1/pods", Some("watch=1")), 2),
            (
                operation(&Method::GET, "/api/v1/namespaces/a/pods/b/log", None),
                1,
            ),
        ];
        assert_eq!(summary(&operations), "watch pods=2, get pods/log=1");
    }
}
