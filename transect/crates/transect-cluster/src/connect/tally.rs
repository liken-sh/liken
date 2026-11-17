//! A count of the requests `transect` sends the API server, by what each
//! asks for, so its load on the server can be read from its own log.
//!
//! Each request is counted where the client passes it to a connection,
//! below the retry, so a request the server answered 429 and the retry
//! after it count as two, as the server receives them.  The first
//! minute's count is logged once, at info, because it includes the
//! lists that `transect` sends at the start.  Every later minute's count
//! is logged at debug, and on a quiet cluster it should include nothing
//! but the watches that resume.

use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};
use std::task::{Context, Poll};
use std::time::Duration;

use futures::future::BoxFuture;
use http::{Method, Request, Response};

/// How often the count is logged, and emptied.
const EVERY: Duration = Duration::from_secs(60);

/// What one request asked for: its verb, as RBAC names it, with
/// `watch` for a list that streams, and its resource, with the group
/// after a dot as kubectl writes it, or `discovery` for a path that
/// names no resource.
#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub struct Asked {
    pub verb: String,
    pub resource: String,
}

/// What the requests since the count was last emptied asked for, and
/// how many the server answered 429.
#[derive(Default)]
struct Counts {
    asked: BTreeMap<Asked, u64>,
    busy: u64,
}

/// The count, shared by every clone of the service.
#[derive(Clone, Default)]
pub struct Tally {
    counts: Arc<Mutex<Counts>>,
}

impl Tally {
    /// `connections`, with each request it sends counted.
    pub fn around<S>(&self, connections: S) -> Counting<S> {
        Counting {
            inner: connections,
            tally: self.clone(),
        }
    }

    /// Logs the count every minute, with `streams` giving how many
    /// streams are open on each connection, when the client spreads its
    /// requests over several.
    pub fn log(self, streams: impl Fn() -> Option<Vec<usize>> + Send + 'static) {
        tokio::spawn(async move {
            let mut first = true;
            let mut every = tokio::time::interval_at(tokio::time::Instant::now() + EVERY, EVERY);
            loop {
                every.tick().await;
                let (asked, busy) = self.take();
                let total: u64 = asked.iter().map(|(_, count)| count).sum();
                let asked = summary(&asked);
                let streams = streams();
                if first {
                    tracing::info!(
                        total,
                        busy,
                        asked,
                        ?streams,
                        "requests to the API server in the first minute"
                    );
                } else {
                    tracing::debug!(
                        total,
                        busy,
                        asked,
                        ?streams,
                        "requests to the API server in the last minute"
                    );
                }
                first = false;
            }
        });
    }

    fn count(&self, asked: Asked) {
        let mut counts = self.counts.lock().unwrap();
        *counts.asked.entry(asked).or_default() += 1;
    }

    fn busy(&self) {
        self.counts.lock().unwrap().busy += 1;
    }

    /// The count since it was last taken, the most asked first, and the
    /// 429s.
    fn take(&self) -> (Vec<(Asked, u64)>, u64) {
        let counts = std::mem::take(&mut *self.counts.lock().unwrap());
        let mut asked: Vec<(Asked, u64)> = counts.asked.into_iter().collect();
        asked.sort_by(|(a, one), (b, other)| other.cmp(one).then(a.cmp(b)));
        (asked, counts.busy)
    }
}

/// `asked` as one line: `watch pods=1, get pods/log=3`.
fn summary(asked: &[(Asked, u64)]) -> String {
    let each = asked.iter().map(|(asked, count)| {
        let Asked { verb, resource } = asked;
        format!("{verb} {resource}={count}")
    });
    each.collect::<Vec<_>>().join(", ")
}

/// What a request with `method`, `path`, and `query` asks for.  A path
/// outside the API, such as `/v2/`, which k3s's embedded registry
/// answers on the API server's address, is named by its first segment.
pub fn asked(method: &Method, path: &str, query: Option<&str>) -> Asked {
    let parts: Vec<&str> = path.trim_matches('/').split('/').collect();
    let (group, rest) = match parts.as_slice() {
        ["api", _version, rest @ ..] => ("", rest),
        ["apis", group, _version, rest @ ..] => (*group, rest),
        ["api" | "apis", ..] => ("", &[][..]),
        [first, ..] => {
            return Asked {
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
            return Asked {
                verb: other.as_str().to_lowercase(),
                resource,
            };
        }
    };
    Asked {
        verb: verb.into(),
        resource,
    }
}

/// A service that counts each request before `inner` sends it.
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
            .count(asked(request.method(), uri.path(), uri.query()));
        let tally = self.tally.clone();
        let answered = self.inner.call(request);
        Box::pin(async move {
            let response = answered.await?;
            if response.status() == http::StatusCode::TOO_MANY_REQUESTS {
                tally.busy();
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
    fn names_what_a_request_asks_for(
        #[case] method: &str,
        #[case] path: &str,
        #[case] query: Option<&str>,
        #[case] verb: &str,
        #[case] resource: &str,
    ) {
        let method = Method::from_bytes(method.as_bytes()).unwrap();
        let expected = Asked {
            verb: verb.into(),
            resource: resource.into(),
        };
        assert_eq!(asked(&method, path, query), expected);
    }

    #[rstest]
    fn takes_the_most_asked_first_and_empties_the_count() {
        let tally = Tally::default();
        let list = asked(&Method::GET, "/api/v1/pods", None);
        let watch = asked(&Method::GET, "/api/v1/pods", Some("watch=1"));
        tally.count(watch.clone());
        tally.count(list.clone());
        tally.count(list.clone());
        tally.busy();
        assert_eq!(tally.take(), (vec![(list, 2), (watch, 1)], 1));
        assert_eq!(tally.take(), (vec![], 0));
    }

    #[rstest]
    fn writes_the_count_on_one_line() {
        let asked = [
            (asked(&Method::GET, "/api/v1/pods", Some("watch=1")), 2),
            (
                asked(&Method::GET, "/api/v1/namespaces/a/pods/b/log", None),
                1,
            ),
        ];
        assert_eq!(summary(&asked), "watch pods=2, get pods/log=1");
    }
}
