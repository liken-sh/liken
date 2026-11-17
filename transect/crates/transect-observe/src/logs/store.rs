//! Queries to a log store.  This module sends the HTTP request, and
//! reads the tail of a pod through the source's tail query, one
//! container at a time.

use std::time::Duration;

use k8s_openapi::jiff::Timestamp;

use super::patterns::LogPattern;
use super::query::{Row, Values, fill};
use super::source::Reached;
use super::tags;
use super::tail::{self, Matching, Read, Tail};
use transect_graph::untrusted::capped;

/// The most bytes transect reads of one response.  A tail is 50 lines
/// and a search returns a row for each pod, so a response far larger
/// than that comes from a query that went wrong.
const MAX_BODY: usize = 1 << 20;

/// How many characters of a refusal's body the reason quotes.
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
    /// its response.  When there are no rows, the error is the reason,
    /// as a phrase that follows the source's name.
    pub async fn ask(
        &self,
        reached: &Reached,
        query: &str,
        timeout: Duration,
    ) -> Result<Vec<Row>, String> {
        let asked = async {
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
                let said = capped(&body, REFUSAL_CHARS).unwrap_or_default();
                return Err(format!(
                    "refused the query with {}: {said}",
                    status.as_u16()
                ));
            }
            reached.source.language.rows(&body)
        };
        match tokio::time::timeout(timeout, asked).await {
            Ok(rows) => rows,
            Err(_) => Err(format!("gave no answer in {}s", timeout.as_secs())),
        }
    }

    /// Reads each container's tail of the pod `namespace/name` from
    /// `since`, and tags each line with the `patterns` that match it.
    /// The error is the reason the store returned no tails.  When the
    /// store has no line of any container, this function returns an
    /// error, so the page shows the API's tail instead of a pod that
    /// looks silent.  A pattern that the store refuses tags nothing,
    /// and the resources page shows the reason from that pattern's
    /// search.  The same pattern queries also return each pattern's
    /// lines in the pod, which come back with the tails.
    ///
    /// This function sends the tail queries and the pattern queries at
    /// the same time, so a read takes as long as its slowest query, not
    /// the time of the tails plus the time of the patterns.  The cost is
    /// that a pod with no lines discards the patterns' responses.
    pub async fn tails(
        &self,
        reached: &Reached,
        pod: (&str, &str, Option<Timestamp>),
        containers: &[String],
        patterns: &[LogPattern],
        timeout: Duration,
    ) -> Result<(Vec<Tail>, Vec<Matching>), String> {
        let asks = containers.iter().map(|container| {
            let query = tail_query(reached, pod, container);
            async move { self.ask(reached, &query, timeout).await }
        });
        let tagged = patterns.iter().map(|pattern| {
            let query = tags::query(reached, pod, pattern);
            async move { self.ask(reached, &query, timeout).await }
        });
        let (answers, tagged) = futures::future::join(
            futures::future::join_all(asks),
            futures::future::join_all(tagged),
        )
        .await;
        let mut tails = Vec::new();
        for (container, rows) in containers.iter().zip(answers) {
            tails.push(Tail {
                container: container.clone(),
                read: tail::from_rows(&rows?, &reached.source),
            });
        }
        if tails.iter().all(|tail| tail.read == Read::Empty) {
            return Err("has no lines for this pod".into());
        }
        let mut matching = Vec::new();
        for (pattern, rows) in patterns.iter().zip(tagged) {
            if let Ok(rows) = rows {
                tags::tag(&mut tails, pattern, &rows, reached);
                matching.extend(tags::matching(pattern, &rows, reached));
            }
        }
        Ok((tails, matching))
    }
}

/// The tail query for one container of `pod`.  This function fills the
/// source's template, narrows the query to the container, and requests
/// only the fields that the page shows for a line.
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

/// The reason a request received no response.  The error leaves out
/// the URL, because the page already names the source.
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
        .map_err(|error| format!("stopped answering: {}", error.without_url()))?
    {
        body.extend_from_slice(&chunk);
        if body.len() > MAX_BODY {
            return Err(format!("answered with more than {} MiB", MAX_BODY >> 20));
        }
    }
    String::from_utf8(body).map_err(|_| "answered with text that is not UTF-8".into())
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::source::fixtures::source;
    use super::*;

    #[rstest]
    fn a_container_tail_asks_for_its_container_and_the_fields_a_line_shows() {
        let reached = source().reached(None, "cluster.local");
        let since = "2026-01-01T00:00:00Z".parse().ok();
        assert_eq!(
            tail_query(&reached, ("shop", "web-1", since), "web"),
            r#"(_time:>=2026-01-01T00:00:00Z k8s.namespace.name:"shop" k8s.pod.name:"web-1") k8s.container.name:="web" | sort by (_time) desc | limit 50 | fields _time, _msg, severity_text"#
        );
    }
}
