//! A small client for Alertmanager's API: the alerts that are firing
//! and neither silenced nor inhibited, and the link to each one's group
//! in Alertmanager's own interface.

use std::time::Duration;

use super::parse;
use super::{Alert, Source};

/// How long one request may take.  Without a limit, an Alertmanager
/// that stops responding holds a poll open forever, and the map keeps
/// showing the last alerts it read as if they still fired.
const TIMEOUT: Duration = Duration::from_secs(10);

#[derive(Clone)]
pub struct Alertmanager {
    client: reqwest::Client,
    /// The API's URL without a trailing slash, for `serves`.
    url: String,
    alerts_url: String,
    /// The base URL of the web interface that a browser opens.  It
    /// differs from `url` when transect reads the API through a
    /// port-forward, for example.
    ui: String,
}

/// Why a read returned no alerts.
#[derive(Debug)]
pub enum FetchError {
    Unreachable(reqwest::Error),
    Unreadable(anyhow::Error),
}

impl std::fmt::Display for FetchError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Unreachable(error) => write!(f, "Alertmanager is unreachable: {error}"),
            Self::Unreadable(error) => {
                write!(f, "Alertmanager's response was unreadable: {error:#}")
            }
        }
    }
}

impl Alertmanager {
    /// A client for the Alertmanager at `url`, such as
    /// `http://localhost:9093`, whose interface a browser opens at `ui`.
    pub fn new(url: &str, ui: &str) -> anyhow::Result<Self> {
        let client = reqwest::Client::builder().timeout(TIMEOUT).build()?;
        Ok(Self {
            client,
            url: url.trim_end_matches('/').into(),
            alerts_url: format!("{}/api/v2/alerts", url.trim_end_matches('/')),
            ui: ui.trim_end_matches('/').into(),
        })
    }

    /// Whether this client reads the Alertmanager at `url` and links to `ui`.
    pub fn serves(&self, url: &str, ui: &str) -> bool {
        self.url == url.trim_end_matches('/') && self.ui == ui.trim_end_matches('/')
    }

    /// The alerts that are firing now, parsed with the conventions of
    /// `source`, each with its link.
    pub async fn firing(&self, source: &Source) -> Result<Vec<Alert>, FetchError> {
        let response = self
            .client
            .get(&self.alerts_url)
            .query(&[
                ("active", "true"),
                ("silenced", "false"),
                ("inhibited", "false"),
            ])
            .send()
            .await
            .map_err(FetchError::Unreachable)?;
        let status = response.status();
        let body = response
            .text()
            .await
            .map_err(|error| FetchError::Unreadable(error.into()))?;
        if !status.is_success() {
            let error = anyhow::anyhow!("the status is {status}");
            return Err(FetchError::Unreadable(error));
        }
        let mut alerts = parse::alerts(&body, source).map_err(FetchError::Unreadable)?;
        for alert in &mut alerts {
            alert.link = Some(group_link(&self.ui, &alert.name));
        }
        Ok(alerts)
    }
}

/// Alertmanager's page for the alerts named `name`.  `/api/v2/alerts`
/// does not say which group an alert is in, and an alert is in a group
/// for each route that it matches.  So the link filters on `alertname`,
/// which routes conventionally group by.
fn group_link(ui: &str, name: &str) -> String {
    let escaped = name.replace('\\', "\\\\").replace('"', "\\\"");
    let filter = format!("{{alertname=\"{escaped}\"}}");
    format!(
        "{ui}/#/alerts?filter={}",
        transect_graph::links::percent_encoded(&filter)
    )
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::plain(
        "WebDown",
        "http://alerts.example/#/alerts?filter=%7Balertname%3D%22WebDown%22%7D"
    )]
    #[case::quoted(
        "a\"b<c",
        "http://alerts.example/#/alerts?filter=%7Balertname%3D%22a%5C%22b%3Cc%22%7D"
    )]
    fn links_to_the_alerts_group_by_name(#[case] name: &str, #[case] expected: &str) {
        assert_eq!(group_link("http://alerts.example", name), expected);
    }
}
