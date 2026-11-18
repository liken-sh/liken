//! An `Outside` declares one system outside the cluster, such as a
//! storage appliance or an identity provider, and the addresses and CSI
//! drivers that reach it.  Kubernetes identifies a backend only by an
//! address or a driver, so a NAS that serves NFS at one address and
//! block volumes through a driver looks like two backends.  The map
//! draws all the backends of an `Outside` as one box in its band of
//! outside systems, under the `Outside`'s title, and the cards and the
//! section view use the same title for each of those backends.

mod parse;

use std::collections::HashMap;

pub use parse::parse;

use crate::storage::Storage;
use transect_graph::edges::{Edge, Endpoint, Provenance, Stated};
use transect_resources::intent::Line;
use transect_resources::resources::{Condition, DocId};

#[derive(Clone, Debug, PartialEq)]
pub struct Outside {
    pub doc: DocId,
    pub name: String,
    /// The name that the map shows for the system, such as "The NAS".
    pub title: String,
    /// The host names or IP addresses of the system, written the way
    /// volumes and dependencies write them, because a backend matches
    /// only on that text.
    pub addresses: Vec<String>,
    /// The CSI drivers whose volumes are stored on the system.
    pub drivers: Vec<String>,
    pub why: Option<String>,
    pub gotcha: Option<String>,
    /// The system's own console, such as a NAS's web interface.  The
    /// page links only a plain `http` or `https` URL and shows any other
    /// value as text.
    pub url: Option<String>,
}

/// The `Outside`s, indexed by the backends they list.
#[derive(Debug, Default)]
pub struct Outsides {
    /// The index in `outsides` of the `Outside` that lists each backend.
    /// Address keys are lower case, so an address matches without regard
    /// to case.
    by_backend: HashMap<String, usize>,
    outsides: Vec<Outside>,
    /// Each CSI driver's console URLs, from the `console` template that
    /// the driver's publisher writes on its `CSIDriver`.
    consoles: HashMap<String, Vec<String>>,
    pub conditions: Vec<(DocId, Condition)>,
}

impl Outsides {
    /// Indexes `outsides` by backend.  A backend belongs to the first
    /// `Outside` in name order that lists it.  When two `Outside`s list
    /// the same backend, it records a conflict condition on both.
    pub fn new(outsides: &[Outside]) -> Self {
        let mut sorted = outsides.to_vec();
        sorted.sort_by(|a, b| a.name.cmp(&b.name));
        let mut found = Self::default();
        for (at, outside) in sorted.iter().enumerate() {
            let addresses = outside.addresses.iter().map(|a| a.to_ascii_lowercase());
            for backend in addresses.chain(outside.drivers.iter().cloned()) {
                match found.by_backend.get(&backend) {
                    Some(&first) => {
                        let first = &sorted[first];
                        let message = format!(
                            "{backend} is listed by {} and {}, and {} draws it because its name sorts first",
                            first.name, outside.name, first.name
                        );
                        found
                            .conditions
                            .push((first.doc, Condition::conflict(message.clone())));
                        found
                            .conditions
                            .push((outside.doc, Condition::conflict(message)));
                    }
                    None => {
                        found.by_backend.insert(backend, at);
                    }
                }
            }
        }
        found.outsides = sorted;
        found
    }

    /// The `Outside` that lists the backend `name`, an address or a
    /// driver.  An address matches without regard to case.
    pub fn of(&self, name: &str) -> Option<&Outside> {
        let at = self
            .by_backend
            .get(name)
            .or_else(|| self.by_backend.get(&name.to_ascii_lowercase()))?;
        self.outsides.get(*at)
    }

    pub fn titled(&self, title: &str) -> Option<&Outside> {
        self.outsides.iter().find(|outside| outside.title == title)
    }

    /// Every `Outside`, in name order.
    pub fn iter(&self) -> impl Iterator<Item = &Outside> {
        self.outsides.iter()
    }

    /// The `Outside` whose `metadata.name` is `name`.
    pub fn named(&self, name: &str) -> Option<&Outside> {
        self.outsides.iter().find(|outside| outside.name == name)
    }

    /// The `Outside` that `name` matches by the title of its box, by its
    /// own name, or by an address or driver it lists, in that order.
    pub fn resolve(&self, name: &str) -> Option<&Outside> {
        self.titled(name)
            .or_else(|| self.named(name))
            .or_else(|| self.of(name))
    }

    /// The path of the page for `name`, matched as `resolve` matches it:
    /// `/ground/` followed by the `Outside`'s name, or by `name` itself
    /// when no `Outside` matches.
    pub fn page_of(&self, name: &str) -> String {
        let name = self.resolve(name).map_or(name, |outside| &outside.name);
        format!("/ground/{}", transect_graph::links::segment(name))
    }

    pub fn with_consoles(self, consoles: HashMap<String, Vec<String>>) -> Self {
        Self { consoles, ..self }
    }

    /// The URL that the box for `name` links to.  When the `Outside` has
    /// a `url`, the link is that URL if `Outside::link` accepts it, and
    /// there is no link otherwise.  Without a `url`, the link is the
    /// drivers' console URL when there is exactly one.
    pub fn link_of(&self, name: &str) -> Option<String> {
        if let Some(outside) = self.resolve(name).filter(|o| o.url.is_some()) {
            return outside.link().map(str::to_string);
        }
        match self.consoles_of(name).as_slice() {
            [one] => Some(one.clone()),
            _ => None,
        }
    }

    /// The console URLs of the drivers in the box for `name`, sorted and
    /// without duplicates.  When no `Outside` matches, `name` is taken as
    /// a driver.  The list is empty when the `Outside` has a `url`,
    /// because that URL replaces the consoles.
    pub fn consoles_of(&self, name: &str) -> Vec<String> {
        let outside = self.resolve(name);
        if outside.is_some_and(|outside| outside.url.is_some()) {
            return Vec::new();
        }
        let drivers = match outside {
            Some(outside) => outside.drivers.clone(),
            None => vec![name.to_string()],
        };
        let mut urls: Vec<String> = drivers
            .iter()
            .filter_map(|driver| self.consoles.get(driver))
            .flatten()
            .cloned()
            .collect();
        urls.sort();
        urls.dedup();
        urls
    }

    /// The name of the box that shows the backend `name`: the title of
    /// its `Outside`, or the backend's own name when none lists it.
    pub fn box_of<'a>(&'a self, name: &'a str) -> &'a str {
        self.of(name).map_or(name, |outside| outside.title.as_str())
    }

    /// `edges`, each end outside the cluster renamed to its box.
    pub fn fold(&self, edges: Vec<Edge>) -> Vec<Edge> {
        edges
            .into_iter()
            .map(|edge| Edge {
                from: self.fold_end(edge.from),
                to: self.fold_end(edge.to),
                ..edge
            })
            .collect()
    }

    pub fn fold_end(&self, end: Endpoint) -> Endpoint {
        match end {
            Endpoint::Outside(name) => Endpoint::Outside(self.box_of(&name).to_string()),
            other => other,
        }
    }

    /// `storage` with each backend renamed to its box.
    pub fn fold_storage(&self, storage: Storage) -> Storage {
        storage.renamed(|name| self.box_of(name).to_string())
    }
}

impl Outside {
    /// The `url` when it is an `http` or `https` URL with no whitespace
    /// or quote, so a page can link it and opening it runs no script.
    pub fn link(&self) -> Option<&str> {
        let url = self.url.as_deref()?;
        let lower = url.to_ascii_lowercase();
        let plain = lower.starts_with("https://") || lower.starts_with("http://");
        let spaced = url.chars().any(|c| c.is_whitespace() || c == '"');
        (plain && !spaced).then_some(url)
    }

    /// The `why` and `gotcha` lines, each cut to `LINE_CHARS` and
    /// marked as intent that this `Outside` declares.
    pub fn lines(&self) -> Vec<Line> {
        let line = |field: &str, text: &Option<String>| {
            Some(Line {
                text: transect_graph::untrusted::capped(text.as_deref()?, LINE_CHARS)?,
                provenance: Provenance::Intent(Stated::Outside {
                    field: field.into(),
                    name: self.name.clone(),
                }),
            })
        };
        [line("why", &self.why), line("gotcha", &self.gotcha)]
            .into_iter()
            .flatten()
            .collect()
    }
}

/// The length in characters at which a `why` or `gotcha` line is cut.
const LINE_CHARS: usize = 300;

#[cfg(any(test, feature = "fixtures"))]
pub mod fixtures {
    use super::*;

    /// Parses an `Outside` named `name` with the YAML `spec`.
    pub fn outside(name: &str, spec: &str) -> Outside {
        let text = format!("metadata: {{name: {name}}}\nspec: {spec}\n");
        parse(DocId::default(), serde_yaml::from_str(&text).unwrap()).unwrap()
    }
}

#[cfg(test)]
mod tests;
