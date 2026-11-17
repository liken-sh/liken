//! The logs lens: one pod's log lines at a time, full width, one
//! container at a time, with the lines each log pattern tags and the
//! source they came from.  A workload's lens lists its pods, the least
//! healthy first and then the newest, and opens on the first.

use std::sync::Arc;

use askama::Template;

use transect_frame::frame::crumbs::Crumb;
use transect_frame::frame::tabs::{Lens, Tabs};
use transect_observe::health::Level;
use transect_observe::logs::{self, Read, Tail, Tails};

/// The logs lens of a pod or a workload.  Its `main` block renders
/// alone as the fragment a pushed update replaces.
#[derive(Template)]
#[template(path = "lenses/logs.html", blocks = ["main"])]
pub struct LogsPage {
    pub namespace: String,
    /// The layer the namespace is in, which the crumb names.
    pub layer: String,
    /// The pod or workload the lens is of.
    pub subject: Subject,
    /// The lens's path with its query, which its event stream's URL
    /// includes, so a push renders the same pod and container.
    pub path: String,
    pub tabs: Tabs,
    /// The workload's pods to switch between; none for a pod's lens.
    pub pods: Vec<Choice>,
    pub containers: Vec<Choice>,
    /// The pod whose lines show, or none for a workload with no pods.
    pub pod: Option<String>,
    /// The container whose lines show.
    pub container: Option<String>,
    /// The lines a page load read, or none before the first.
    pub logs: Option<Arc<Tails>>,
}

/// What a logs lens is of.
#[derive(Clone, Debug, PartialEq)]
pub enum Subject {
    /// A pod, with the workload that runs it when one does, by its name
    /// and its page.
    Pod {
        name: String,
        workload: Option<(String, Option<String>)>,
    },
    /// A workload, by its name and its page.
    Workload { name: String, path: String },
}

/// One pod or container in a row to switch between.
#[derive(Clone, Debug, PartialEq)]
pub struct Choice {
    pub label: String,
    pub href: String,
    pub current: bool,
    /// A pod's health, which its dot shows; none for a container.
    pub level: Option<Level>,
}

impl Choice {
    /// The dot's class, as a search row's: clear when healthy.
    pub fn dot_class(&self) -> Option<&'static str> {
        Some(match self.level? {
            Level::Healthy => "dot dot-clear",
            Level::Notice => "dot concern-notice",
            Level::Warning => "dot concern-warning",
            Level::Failing => "dot concern-failing",
        })
    }
}

/// One container whose lines the lens can show: an app container, or
/// an init container that has started.
#[derive(Clone, Debug, PartialEq)]
pub struct Container {
    pub name: String,
    pub init: bool,
}

impl Container {
    /// Its name, and "(init)" after an init container's, so a reader
    /// knows why its lines stop.
    fn label(&self) -> String {
        match self.init {
            true => format!("{} (init)", self.name),
            false => self.name.clone(),
        }
    }
}

/// What the lens is built from.
pub struct Asked<'a> {
    pub namespace: &'a str,
    pub layer: String,
    pub subject: Subject,
    /// The workload's pods, by name with their health, in the order the
    /// lens lists them; for a pod's lens, the pod.
    pub pods: Vec<(String, Level)>,
    /// The pod asked for, when the query names one of `pods`.
    pub pod: Option<&'a str>,
    /// The containers of the shown pod whose logs can be read: its
    /// app containers in its spec's order, then its started init
    /// containers.
    pub containers: Vec<Container>,
    pub container: Option<&'a str>,
}

impl LogsPage {
    pub fn build(asked: Asked, logs: Option<Arc<Tails>>) -> Self {
        let base = match &asked.subject {
            Subject::Pod { name, .. } => transect_graph::links::pod(asked.namespace, name),
            Subject::Workload { path, .. } => path.clone(),
        };
        let lens = format!("{base}/logs");
        let pod_names: Vec<String> = asked.pods.iter().map(|(name, _)| name.clone()).collect();
        let pod = chosen(&pod_names, asked.pod);
        let names: Vec<String> = asked.containers.iter().map(|c| c.name.clone()).collect();
        let container = chosen(&names, asked.container);
        let is_workload = matches!(asked.subject, Subject::Workload { .. });
        let query = |pod: Option<&str>, container: Option<&str>| {
            let mut query = Vec::new();
            if let Some(pod) = pod.filter(|_| is_workload) {
                query.push(format!(
                    "pod={}",
                    transect_graph::links::percent_encoded(pod)
                ));
            }
            if let Some(container) = container {
                query.push(format!(
                    "container={}",
                    transect_graph::links::percent_encoded(container)
                ));
            }
            match query.is_empty() {
                true => lens.clone(),
                false => format!("{lens}?{}", query.join("&")),
            }
        };
        let pods = match is_workload {
            true => asked
                .pods
                .iter()
                .map(|(name, level)| Choice {
                    label: name.clone(),
                    href: query(Some(name), None),
                    current: Some(name) == pod.as_ref(),
                    level: Some(*level),
                })
                .collect(),
            false => Vec::new(),
        };
        let containers = match asked.containers.len() {
            0 | 1 => Vec::new(),
            _ => asked
                .containers
                .iter()
                .map(|shown| Choice {
                    label: shown.label(),
                    href: query(pod.as_deref(), Some(&shown.name)),
                    current: Some(&shown.name) == container.as_ref(),
                    level: None,
                })
                .collect(),
        };
        let path = query(pod.as_deref(), asked.container.and(container.as_deref()));
        let tabs = match &asked.subject {
            Subject::Pod { .. } => Tabs::pod(&base, Lens::Logs),
            Subject::Workload { .. } => Tabs::workload(&base, Lens::Logs, false),
        };
        Self {
            namespace: asked.namespace.into(),
            layer: asked.layer,
            subject: asked.subject,
            path,
            tabs,
            pods,
            containers,
            pod,
            container,
            logs,
        }
    }

    pub fn name(&self) -> &str {
        match &self.subject {
            Subject::Pod { name, .. } | Subject::Workload { name, .. } => name,
        }
    }

    pub fn overview(&self) -> &str {
        &self.tabs.tabs[0].href
    }

    /// The crumb: the cluster, the layer, the namespace, the workload,
    /// the pod for a pod's lens, and then "logs".
    pub fn crumbs(&self) -> Vec<Crumb> {
        let select = format!("/?select={}", self.namespace);
        let mut crumbs = vec![
            Crumb::home(),
            Crumb::link(&self.layer, format!("{select}#{}", self.layer)),
            Crumb::link_name(
                &self.namespace,
                transect_graph::links::namespace(&self.namespace),
            ),
        ];
        match &self.subject {
            Subject::Pod { name, workload } => {
                if let Some((workload, href)) = workload {
                    crumbs.push(match href {
                        Some(href) => Crumb::link_name(workload, href.clone()),
                        None => Crumb::current_name(workload),
                    });
                }
                crumbs.push(Crumb::link_name(
                    name,
                    transect_graph::links::pod(&self.namespace, name),
                ));
            }
            Subject::Workload { name, path } => {
                crumbs.push(Crumb::link_name(name, path.clone()));
            }
        }
        crumbs.push(Crumb::current("logs"));
        crumbs
    }

    pub fn events_url(&self) -> String {
        format!("/events{}", self.path)
    }

    /// The tail of the container that shows.
    pub fn tail<'t>(&self, tails: &'t Tails) -> Option<&'t Tail> {
        let named = tails
            .containers
            .iter()
            .find(|tail| Some(&tail.container) == self.container.as_ref());
        named.or(tails.containers.first())
    }

    /// Each pattern's lines from the container that shows.
    pub fn matching_lines<'t>(&self, matching: &'t logs::Matching) -> Vec<&'t logs::Line> {
        let shown = |container: &String| match &self.container {
            Some(chosen) => container == chosen,
            None => true,
        };
        let lines = matching
            .lines
            .iter()
            .filter(|(container, _)| shown(container));
        lines.map(|(_, line)| line).collect()
    }

    /// "3 lines since it started match", above one pattern's lines in
    /// this pod, or "The latest 50 lines that match" when the store's
    /// answer stopped at its limit.
    pub fn matching_heading(&self, count: usize) -> String {
        match count {
            1 => "1 line since it started matches".into(),
            count if count >= logs::TAIL_LINES as usize => {
                format!("The latest {count} lines that match")
            }
            count => format!("{count} lines since it started match"),
        }
    }

    /// How many lines of the container that shows, from where, and
    /// when they were read.
    pub fn note(&self, tails: &Tails) -> String {
        logs::note(tails, self.container.as_deref())
    }

    pub fn fallback(&self, tails: &Tails) -> Option<String> {
        logs::fallback(tails)
    }

    pub fn forbidden(&self) -> &'static str {
        logs::FORBIDDEN
    }

    pub fn empty(&self) -> &'static str {
        logs::EMPTY
    }

    /// The text shown in place of the lines when the workload runs no pod.
    pub fn no_pods(&self) -> &'static str {
        "It runs no pods now, so there are no lines to show."
    }

    pub fn lines<'t>(&self, read: &'t Read) -> Option<&'t [logs::Line]> {
        match read {
            Read::Lines(lines) => Some(lines),
            _ => None,
        }
    }
}

/// `asked` when `names` contains it, or else the first of `names`.
fn chosen(names: &[String], asked: Option<&str>) -> Option<String> {
    let found = asked.and_then(|asked| names.iter().find(|name| *name == asked));
    found.or(names.first()).cloned()
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn asked<'a>(pod: Option<&'a str>, container: Option<&'a str>) -> Asked<'a> {
        Asked {
            namespace: "shop",
            layer: "apps".into(),
            subject: Subject::Workload {
                name: "web".into(),
                path: "/namespaces/shop/deployments/web".into(),
            },
            pods: vec![
                ("web-1".into(), Level::Healthy),
                ("web-2".into(), Level::Healthy),
            ],
            pod,
            containers: ["app", "proxy"]
                .map(|name| Container {
                    name: name.into(),
                    init: false,
                })
                .into(),
            container,
        }
    }

    /// The pod and container that show, and the path a push renders.
    fn shown(
        pod: Option<&str>,
        container: Option<&str>,
    ) -> (Option<String>, Option<String>, String) {
        let page = LogsPage::build(asked(pod, container), None);
        (page.pod, page.container, page.path)
    }

    fn some(text: &str) -> Option<String> {
        Some(text.into())
    }

    #[rstest]
    #[case::opens_on_the_first(None, None, (some("web-1"), some("app"), "/namespaces/shop/deployments/web/logs?pod=web-1".into()))]
    #[case::a_pod_asked_for(Some("web-2"), None, (some("web-2"), some("app"), "/namespaces/shop/deployments/web/logs?pod=web-2".into()))]
    #[case::a_container_asked_for(Some("web-2"), Some("proxy"), (some("web-2"), some("proxy"), "/namespaces/shop/deployments/web/logs?pod=web-2&container=proxy".into()))]
    #[case::a_pod_it_does_not_run(Some("gone-1"), None, (some("web-1"), some("app"), "/namespaces/shop/deployments/web/logs?pod=web-1".into()))]
    fn shows_the_pod_and_container_asked_for(
        #[case] pod: Option<&str>,
        #[case] container: Option<&str>,
        #[case] expected: (Option<String>, Option<String>, String),
    ) {
        assert_eq!(shown(pod, container), expected);
    }

    #[test]
    fn switches_between_pods_and_containers() {
        let page = LogsPage::build(asked(Some("web-2"), Some("proxy")), None);
        let hrefs = |choices: &[Choice]| -> Vec<(String, bool)> {
            choices
                .iter()
                .map(|c| (c.href.clone(), c.current))
                .collect()
        };
        assert_eq!(
            (hrefs(&page.pods), hrefs(&page.containers)),
            (
                vec![
                    (
                        "/namespaces/shop/deployments/web/logs?pod=web-1".into(),
                        false
                    ),
                    (
                        "/namespaces/shop/deployments/web/logs?pod=web-2".into(),
                        true
                    ),
                ],
                vec![
                    (
                        "/namespaces/shop/deployments/web/logs?pod=web-2&container=app".into(),
                        false
                    ),
                    (
                        "/namespaces/shop/deployments/web/logs?pod=web-2&container=proxy".into(),
                        true
                    ),
                ],
            )
        );
    }
}
