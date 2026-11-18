//! The HTTP client of a log store. It sends a source's queries, and
//! reads a pod's tail with one tail query for each container.

use std::time::Duration;

use k8s_openapi::jiff::Timestamp;

use super::patterns::LogPattern;
use super::query::{Row, Values, fill};
use super::source::Reached;
use super::tags;
use super::tail::{self, Matching, Read, Tail};
use transect_graph::untrusted::capped;

/// The most bytes transect reads of one response. A tail is
/// `tail::TAIL_LINES` lines and a search returns a row for each pod, so
/// a larger response means the query is wrong.
const MAX_BODY: usize = 1 << 20;

/// The most characters of an error response's body that the error
/// quotes.
const REFUSAL_CHARS: usize = 200;

#[derive(Clone)]
pub struct Store {
    http: reqwest::Client,
}

impl Store {
    pub fn new() -> anyhow::Result<Self> {
        let http = reqwest::Client::builder()
            .user_agent(concat!("transect/", env!("CARGO_PKG_VERSION")))
            .build()?;
        Ok(Self { http })
    }

    /// Sends `query` to the store at `reached` and returns the rows of
    /// its response. On failure, the error is a phrase that completes a
    /// sentence that starts with the source's name.
    pub async fn run_query(
        &self,
        reached: &Reached,
        query: &str,
        timeout: Duration,
    ) -> Result<Vec<Row>, String> {
        let request = async {
            let response = self
                .http
                .get(&reached.endpoint)
                .query(&[("query", query)])
                .send()
                .await
                .map_err(unreachable)?;
            let status = response.status();
            let body = read(response).await?;
            if !status.is_success() {
                let excerpt = capped(&body, REFUSAL_CHARS).unwrap_or_default();
                return Err(format!(
                    "returned status {} for the query: {excerpt}",
                    status.as_u16()
                ));
            }
            reached.source.language.rows(&body)
        };
        match tokio::time::timeout(timeout, request).await {
            Ok(rows) => rows,
            Err(_) => Err(format!("did not respond within {}s", timeout.as_secs())),
        }
    }

    /// Reads the tail of each container of the pod `namespace/name`
    /// from `since`, tags each line with the `patterns` that match it,
    /// and returns each pattern's matching lines in the pod beside the
    /// tails. The tail queries and the pattern queries run concurrently.
    ///
    /// When a tail query fails, or no container has a line in the store,
    /// this returns an error, and the caller reads the tail from the
    /// Kubernetes API instead of showing a pod with no lines. A pattern
    /// whose query fails tags no line. The resources page shows the
    /// error from that pattern's cluster search.
    pub async fn tails(
        &self,
        reached: &Reached,
        pod: (&str, &str, Option<Timestamp>),
        containers: &[String],
        patterns: &[LogPattern],
        timeout: Duration,
    ) -> Result<(Vec<Tail>, Vec<Matching>), String> {
        let tail_queries = containers.iter().map(|container| {
            let query = tail_query(reached, pod, container);
            async move { self.run_query(reached, &query, timeout).await }
        });
        let pattern_queries = patterns.iter().map(|pattern| {
            let query = tags::query(reached, pod, pattern);
            async move { self.run_query(reached, &query, timeout).await }
        });
        let (tail_rows, pattern_rows) = futures::future::join(
            futures::future::join_all(tail_queries),
            futures::future::join_all(pattern_queries),
        )
        .await;
        let mut tails = Vec::new();
        for (container, rows) in containers.iter().zip(tail_rows) {
            tails.push(Tail {
                container: container.clone(),
                read: tail::from_rows(&rows?, &reached.source),
            });
        }
        if tails.iter().all(|tail| tail.read == Read::Empty) {
            return Err("has no lines for this pod".into());
        }
        let mut matching = Vec::new();
        for (pattern, rows) in patterns.iter().zip(pattern_rows) {
            if let Ok(rows) = rows {
                tags::tag(&mut tails, pattern, &rows, reached);
                matching.extend(tags::matching(pattern, &rows, reached));
            }
        }
        Ok((tails, matching))
    }
}

/// The tail query for one container of a pod: the source's tail
/// template, filtered to the container, with only the fields that the
/// page shows for a line.
pub fn tail_query(
    reached: &Reached,
    (namespace, name, since): (&str, &str, Option<Timestamp>),
    container: &str,
) -> String {
    let source = &reached.source;
    let language = source.language;
    let values = Values {
        namespace: Some(namespace),
        pod: Some(name),
        since,
        limit: Some(tail::TAIL_LINES as usize),
        ..Values::default()
    };
    let query = fill(&source.tail, language, &source.fields, &values);
    let query = language.narrow(
        &query,
        &language.equals(&source.fields.container, container),
    );
    let fields = &source.fields;
    let shown = [
        Some(&fields.time),
        Some(&fields.message),
        fields.level.as_ref(),
    ];
    let shown: Vec<&str> = shown.into_iter().flatten().map(String::as_str).collect();
    language.keep(&query, &shown)
}

/// The reason a request got no response. The text leaves out the URL,
/// because the page already names the source.
fn unreachable(error: reqwest::Error) -> String {
    match error.is_connect() {
        true => "is unreachable".into(),
        false => format!("is unreachable: {}", error.without_url()),
    }
}

/// The body of `response`, up to `MAX_BODY`.
async fn read(mut response: reqwest::Response) -> Result<String, String> {
    let mut body = Vec::new();
    while let Some(chunk) = response
        .chunk()
        .await
        .map_err(|error| format!("stopped sending the response: {}", error.without_url()))?
    {
        body.extend_from_slice(&chunk);
        if body.len() > MAX_BODY {
            return Err(format!(
                "returned a response of more than {} MiB",
                MAX_BODY >> 20
            ));
        }
    }
    String::from_utf8(body).map_err(|_| "returned a response that is not UTF-8".into())
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::source::fixtures::source;
    use super::*;

    #[rstest]
    fn a_container_tail_is_limited_to_its_container_and_the_fields_a_line_shows() {
        let reached = source().reached(None, "cluster.local");
        let since = "2026-01-01T00:00:00Z".parse().ok();
        assert_eq!(
            tail_query(&reached, ("shop", "web-1", since), "web"),
            r#"(_time:>=2026-01-01T00:00:00Z k8s.namespace.name:"shop" k8s.pod.name:"web-1") k8s.container.name:="web" | sort by (_time) desc | limit 50 | fields _time, _msg, severity_text"#
        );
    }
}
