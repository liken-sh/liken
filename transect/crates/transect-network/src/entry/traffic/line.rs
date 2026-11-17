//! An `Ingress`'s traffic in words: one gray line per backend, or one
//! gray line that states what would show its traffic.

use super::{Backend, Figures, Traffic};
use transect_graph::edges::Object;
use transect_graph::links::Reference;
use transect_resources::resources::OffLine;

/// One backend's line: the `Service` and port it names, when the
/// `Ingress` routes to more than one, and its figures.
#[derive(Clone, Debug, PartialEq)]
pub struct BackendLine {
    pub backend: Option<Reference>,
    pub figures: String,
}

impl Traffic {
    /// The one line that the page shows in place of its figures when
    /// it has none: what would show them, or why they are missing.
    pub fn note(&self) -> Option<OffLine> {
        let said = |text: &str| Some(OffLine::said(text));
        match self {
            Self::Undeclared => said(
                "An IngressController with entry queries for its controller would show traffic here.",
            ),
            Self::Unloaded(name) => Some(OffLine {
                text: format!("No traffic: the IngressController {name} did not load"),
                see_resources: true,
            }),
            Self::Unread => said("No traffic yet: the entry queries have not answered."),
            Self::Unnamed => said("Its controller's metrics name none of its backends."),
            Self::Read { .. } => None,
        }
    }

    /// One line per backend, each naming its `Service` in `namespace`
    /// when there are several, or with `named`, always.
    pub fn lines(&self, namespace: &str, named: bool) -> Vec<BackendLine> {
        let Self::Read {
            backends, window, ..
        } = self
        else {
            return Vec::new();
        };
        let named = named || backends.len() > 1;
        backends
            .iter()
            .map(|backend| BackendLine {
                backend: named.then(|| reference(namespace, backend)),
                figures: match &backend.figures {
                    Some(figures) => figures.line(window),
                    None => format!("no requests counted over {window}"),
                },
            })
            .collect()
    }

    /// Where the figures came from, for each line's title.
    pub fn title(&self) -> Option<String> {
        let Self::Read { controller, .. } = self else {
            return None;
        };
        Some(format!(
            "Requests the ingress controller sent to each Service, from Prometheus, through the IngressController {controller}."
        ))
    }
}

/// The backend's `Service` and port, linked to the `Service`'s page.
fn reference(namespace: &str, backend: &Backend) -> Reference {
    let service = Object::new("", "Service", Some(namespace), &backend.service);
    Reference {
        text: format!("{}:{}", backend.service, backend.port),
        href: transect_graph::links::page(&service, &Default::default()),
    }
}

impl Figures {
    /// "3.2 requests/s · p95 120 ms · 0.4% 5xx, over 5m", or "no
    /// requests over 5m".
    pub fn line(&self, window: &str) -> String {
        let Self {
            requests,
            p95,
            errors,
        } = *self;
        if requests <= 0.0 {
            return format!("no requests over {window}");
        }
        let mut parts = vec![rate(requests)];
        if let Some(p95) = p95 {
            parts.push(format!("p95 {}", seconds(p95)));
        }
        parts.push(match errors / requests {
            share if share <= 0.0 => "no 5xx".into(),
            share => format!("{} 5xx", percent(share)),
        });
        format!("{}, over {window}", parts.join(" · "))
    }
}

/// "12 requests/s", "3.2 requests/s", "0.05 requests/s", or "under
/// 0.01 requests/s".
fn rate(requests: f64) -> String {
    let number = match requests {
        r if r >= 10.0 => format!("{r:.0}"),
        r if r >= 1.0 => format!("{r:.1}"),
        r if r >= 0.01 => format!("{r:.2}"),
        _ => return "under 0.01 requests/s".into(),
    };
    format!("{number} requests/s")
}

/// "120 ms", or "2.4 s" from a second up.
fn seconds(value: f64) -> String {
    match value {
        v if v >= 1.0 => format!("{v:.1} s"),
        v => format!("{:.0} ms", v * 1000.0),
    }
}

/// "0.4%", "12%", or "under 0.1%".
fn percent(share: f64) -> String {
    match share * 100.0 {
        p if p >= 10.0 => format!("{p:.0}%"),
        p if p >= 0.1 => format!("{p:.1}%"),
        _ => "under 0.1%".into(),
    }
}
