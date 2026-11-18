//! A client for Prometheus instant queries. It takes one PromQL
//! expression and returns one sample for each series.

use std::collections::HashMap;
use std::time::Duration;

use anyhow::{Context, bail};
use serde::Deserialize;

/// How long one request may take. Without a timeout, a Prometheus that
/// stops responding would stall a poll forever, and the map would keep
/// showing the last numbers it read as if they were current.
const TIMEOUT: Duration = Duration::from_secs(10);

#[derive(Clone)]
pub struct Prometheus {
    client: reqwest::Client,
    /// The URL given to `Prometheus::new`, without a trailing slash.
    base: String,
    query_url: String,
}

/// One series of an instant vector: its labels and its value at the
/// time of the query.
#[derive(Debug, PartialEq)]
pub struct Sample {
    pub labels: HashMap<String, String>,
    pub value: f64,
}

/// Why a query failed. The map shows it in one line, so a reader can
/// tell a Prometheus that transect cannot reach from one that returned
/// an error.
#[derive(Debug)]
pub enum QueryError {
    Unreachable(reqwest::Error),
    Failed(anyhow::Error),
}

impl std::fmt::Display for QueryError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Unreachable(error) => write!(f, "Prometheus is unreachable: {error}"),
            Self::Failed(error) => write!(f, "the Prometheus response is unusable: {error:#}"),
        }
    }
}

impl Prometheus {
    /// A client for the Prometheus at `url`, such as
    /// `http://localhost:9090`.
    pub fn new(url: &str) -> anyhow::Result<Self> {
        let client = reqwest::Client::builder().timeout(TIMEOUT).build()?;
        let base = url.trim_end_matches('/');
        Ok(Self {
            client,
            base: base.into(),
            query_url: format!("{base}/api/v1/query"),
        })
    }

    /// The base URL that this client sends its requests to.
    pub fn url(&self) -> &str {
        &self.base
    }

    /// Runs `promql` as an instant query at the current time.
    pub async fn query(&self, promql: &str) -> Result<Vec<Sample>, QueryError> {
        let response = self
            .client
            .get(&self.query_url)
            .query(&[("query", promql)])
            .send()
            .await
            .map_err(QueryError::Unreachable)?;
        // For a bad query, Prometheus returns a 4xx status with the same
        // JSON envelope as a success, and the envelope's `error` field
        // describes the failure better than the status code. So this
        // parses the body whatever the status.
        let body = response
            .text()
            .await
            .map_err(|error| QueryError::Failed(error.into()))?;
        parse(&body).map_err(QueryError::Failed)
    }
}

impl Prometheus {
    /// The host name that Prometheus reports in its runtime information.
    /// In a pod, that is the pod's name.
    pub async fn hostname(&self) -> Result<String, QueryError> {
        let url = self
            .query_url
            .replace("/api/v1/query", "/api/v1/status/runtimeinfo");
        let response = self
            .client
            .get(&url)
            .send()
            .await
            .map_err(QueryError::Unreachable)?;
        let body = response
            .text()
            .await
            .map_err(|error| QueryError::Failed(error.into()))?;
        runtime_hostname(&body).map_err(QueryError::Failed)
    }
}

#[derive(Deserialize)]
struct RuntimeInfo {
    status: String,
    data: Option<Runtime>,
}

#[derive(Deserialize)]
struct Runtime {
    hostname: String,
}

fn runtime_hostname(body: &str) -> anyhow::Result<String> {
    let info: RuntimeInfo = serde_json::from_str(body).context("reading the response")?;
    if info.status != "success" {
        bail!("Prometheus returned status {}", info.status);
    }
    info.data
        .map(|data| data.hostname)
        .context("the response has no hostname")
}

#[derive(Deserialize)]
struct Envelope {
    status: String,
    error: Option<String>,
    data: Option<Data>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct Data {
    result_type: String,
    /// The shape of `result` depends on `result_type`, so `parse`
    /// decodes it only after it checks `result_type`.
    result: serde_json::Value,
}

#[derive(Deserialize)]
struct Series {
    metric: HashMap<String, String>,
    /// A timestamp in seconds, then the value as a string. The value is
    /// a string because JSON has no number for `NaN` or `+Inf`.
    value: (f64, String),
}

/// Parses the body of an instant query that returned a vector. Series
/// whose value is not a finite number are left out, because a `NaN` from
/// a division by zero is not a measurement.
fn parse(body: &str) -> anyhow::Result<Vec<Sample>> {
    let envelope: Envelope = serde_json::from_str(body).context("reading the response")?;
    if envelope.status != "success" {
        bail!(
            "{}",
            envelope
                .error
                .as_deref()
                .unwrap_or("the response has no error message")
        );
    }
    let data = envelope.data.context("the response has no data")?;
    if data.result_type != "vector" {
        bail!("the result type is {}, want vector", data.result_type);
    }
    let result: Vec<Series> = serde_json::from_value(data.result).context("reading the vector")?;
    Ok(result
        .into_iter()
        .filter_map(|series| {
            let value: f64 = series.value.1.parse().ok()?;
            value.is_finite().then_some(Sample {
                labels: series.metric,
                value,
            })
        })
        .collect())
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    fn parses_each_series_of_a_vector() {
        let samples = parse(
            r#"{"status":"success","data":{"resultType":"vector","result":[
                {"metric":{"node":"alpha"},"value":[1700000000.123,"1.5"]},
                {"metric":{"instance":"beta:10250","job":"kubelet"},"value":[1700000000.123,"2e9"]}
            ]}}"#,
        )
        .unwrap();
        assert_eq!(
            samples,
            vec![
                Sample {
                    labels: HashMap::from([("node".into(), "alpha".into())]),
                    value: 1.5,
                },
                Sample {
                    labels: HashMap::from([
                        ("instance".into(), "beta:10250".into()),
                        ("job".into(), "kubelet".into()),
                    ]),
                    value: 2e9,
                },
            ]
        );
    }

    #[rstest]
    fn an_empty_vector_has_no_samples() {
        let samples =
            parse(r#"{"status":"success","data":{"resultType":"vector","result":[]}}"#).unwrap();
        assert_eq!(samples, vec![]);
    }

    #[rstest]
    #[case::nan("NaN")]
    #[case::infinity("+Inf")]
    #[case::negative_infinity("-Inf")]
    fn leaves_out_values_that_are_not_finite(#[case] value: &str) {
        let body = format!(
            r#"{{"status":"success","data":{{"resultType":"vector","result":[
                {{"metric":{{"node":"alpha"}},"value":[1700000000,"{value}"]}}
            ]}}}}"#
        );
        assert_eq!(parse(&body).unwrap(), vec![]);
    }

    #[rstest]
    #[case::error(
        r#"{"status":"error","errorType":"bad_data","error":"parse error at char 5"}"#,
        "parse error at char 5"
    )]
    #[case::scalar(
        r#"{"status":"success","data":{"resultType":"scalar","result":[1700000000,"1"]}}"#,
        "the result type is scalar, want vector"
    )]
    #[case::not_json("<html>502 Bad Gateway</html>", "reading the response")]
    fn reports_a_response_that_is_not_a_vector(#[case] body: &str, #[case] message: &str) {
        let error = parse(body).unwrap_err();
        assert!(
            format!("{error:#}").contains(message),
            "the error is {error:#}, want it to mention {message:?}"
        );
    }

    #[rstest]
    #[case::a_pod(
        r#"{"status":"success","data":{"startTime":"2026-01-01T00:00:00Z","hostname":"prometheus-0"}}"#,
        Some("prometheus-0")
    )]
    #[case::an_error(r#"{"status":"error","error":"no"}"#, None)]
    fn reads_the_hostname_from_the_runtime_information(
        #[case] body: &str,
        #[case] expected: Option<&str>,
    ) {
        assert_eq!(runtime_hostname(body).ok().as_deref(), expected);
    }
}
