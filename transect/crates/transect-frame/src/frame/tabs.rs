//! The row of tabs on an object's page. Each tab is a different view
//! of the same object at its own URL, and a page shows only the tabs
//! that apply to its object.

/// One view of an object, shown as one tab.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Lens {
    Overview,
    Section,
    IfLost,
    Logs,
    Access,
    Vulnerabilities,
}

impl Lens {
    fn label(self) -> &'static str {
        match self {
            Self::Overview => "overview",
            Self::Section => "section",
            Self::IfLost => "if lost",
            Self::Logs => "logs",
            Self::Access => "access",
            Self::Vulnerabilities => "vulnerabilities",
        }
    }
}

/// The tabs of one page, in display order.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Tabs {
    pub tabs: Vec<Tab>,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Tab {
    pub label: &'static str,
    pub href: String,
    /// Whether this is the tab for the page being shown.
    pub current: bool,
}

impl Tabs {
    /// A workload has no "if lost" tab because its pods can run on
    /// several machines.
    pub fn workload(path: &str, current: Lens, access: bool) -> Self {
        let mut lenses = vec![
            (Lens::Overview, path.to_string()),
            (Lens::Section, format!("{path}/section")),
            (Lens::Logs, format!("{path}/logs")),
        ];
        if access {
            lenses.push((Lens::Access, format!("{path}/access")));
        }
        Self::of(&lenses, current)
    }

    /// Adds the access tab for `path`, unless the same tab with
    /// `current` false is already there.
    pub fn with_access(mut self, path: &str) -> Self {
        let tab = Tab {
            label: Lens::Access.label(),
            href: format!("{path}/access"),
            current: false,
        };
        if !self.tabs.contains(&tab) {
            self.tabs.push(tab);
        }
        self
    }

    /// Adds the vulnerabilities tab for the workload at `path`, unless a
    /// tab with that URL is already there.
    pub fn with_vulnerabilities(mut self, path: &str, current: bool) -> Self {
        let tab = Tab {
            label: Lens::Vulnerabilities.label(),
            href: format!("{path}/vulnerabilities"),
            current,
        };
        if !self.tabs.iter().any(|existing| existing.href == tab.href) {
            self.tabs.push(tab);
        }
        self
    }

    pub fn namespace(path: &str, current: Lens) -> Self {
        Self::of(
            &[
                (Lens::Overview, path.to_string()),
                (Lens::Access, format!("{path}/access")),
            ],
            current,
        )
    }

    pub fn pod(path: &str, current: Lens) -> Self {
        Self::of(
            &[
                (Lens::Overview, path.to_string()),
                (Lens::Logs, format!("{path}/logs")),
            ],
            current,
        )
    }

    /// The "if lost" tab links to the cluster map with `?lose={name}`.
    /// It has no path under the machine's page, because the map's `lose`
    /// parameter takes a comma-separated list of machines.
    pub fn machine(path: &str, name: &str, current: Lens) -> Self {
        Self::of(
            &[
                (Lens::Overview, path.to_string()),
                (Lens::IfLost, format!("/?lose={name}")),
            ],
            current,
        )
    }

    fn of(lenses: &[(Lens, String)], current: Lens) -> Self {
        let tabs = lenses.iter().map(|(lens, href)| Tab {
            label: lens.label(),
            href: href.clone(),
            current: *lens == current,
        });
        Self {
            tabs: tabs.collect(),
        }
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn shown(tabs: Tabs) -> Vec<(&'static str, String, bool)> {
        let tabs = tabs.tabs.into_iter();
        tabs.map(|tab| (tab.label, tab.href, tab.current)).collect()
    }

    fn tab(label: &'static str, href: &str, current: bool) -> (&'static str, String, bool) {
        (label, href.into(), current)
    }

    #[rstest]
    #[case::a_workload(
        Tabs::workload("/namespaces/shop/deployments/web", Lens::Section, false),
        vec![
            tab("overview", "/namespaces/shop/deployments/web", false),
            tab("section", "/namespaces/shop/deployments/web/section", true),
            tab("logs", "/namespaces/shop/deployments/web/logs", false),
        ]
    )]
    #[case::a_workload_that_serves_a_port(
        Tabs::workload("/namespaces/shop/deployments/web", Lens::Access, true),
        vec![
            tab("overview", "/namespaces/shop/deployments/web", false),
            tab("section", "/namespaces/shop/deployments/web/section", false),
            tab("logs", "/namespaces/shop/deployments/web/logs", false),
            tab("access", "/namespaces/shop/deployments/web/access", true),
        ]
    )]
    #[case::a_workload_a_report_names(
        Tabs::workload("/namespaces/shop/deployments/web", Lens::Vulnerabilities, true)
            .with_vulnerabilities("/namespaces/shop/deployments/web", true),
        vec![
            tab("overview", "/namespaces/shop/deployments/web", false),
            tab("section", "/namespaces/shop/deployments/web/section", false),
            tab("logs", "/namespaces/shop/deployments/web/logs", false),
            tab("access", "/namespaces/shop/deployments/web/access", false),
            tab("vulnerabilities", "/namespaces/shop/deployments/web/vulnerabilities", true),
        ]
    )]
    #[case::a_namespace(
        Tabs::namespace("/namespaces/shop", Lens::Access),
        vec![
            tab("overview", "/namespaces/shop", false),
            tab("access", "/namespaces/shop/access", true),
        ]
    )]
    #[case::a_pod(
        Tabs::pod("/namespaces/shop/pods/web-1", Lens::Overview),
        vec![
            tab("overview", "/namespaces/shop/pods/web-1", true),
            tab("logs", "/namespaces/shop/pods/web-1/logs", false),
        ]
    )]
    #[case::a_machine(
        Tabs::machine("/machines/alpha", "alpha", Lens::Overview),
        vec![tab("overview", "/machines/alpha", true), tab("if lost", "/?lose=alpha", false)]
    )]
    fn shows_the_tabs_that_apply(
        #[case] tabs: Tabs,
        #[case] expected: Vec<(&'static str, String, bool)>,
    ) {
        assert_eq!(shown(tabs), expected);
    }
}
