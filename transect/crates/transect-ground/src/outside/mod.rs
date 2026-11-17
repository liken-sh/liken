//! Outsides.  An `Outside` names one system outside the
//! cluster, such as a storage appliance or an identity provider, once,
//! with the addresses and CSI drivers that reach it.  The Kubernetes API
//! names a backend only by an address or a driver, so one appliance that
//! serves NFS through one address and block volumes through a driver
//! appears as two backends.  An `Outside` groups them into one ground
//! box, with its title and its intent.
//!
//! The box is named by the `Outside`'s title.  Every place that draws
//! the backend shows that title: the ground band, a card's ends, and the
//! section view.

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
    /// What the box shows, such as "The NAS".
    pub title: String,
    /// The hosts that reach the system, written as a volume or a
    /// dependency names them.
    pub addresses: Vec<String>,
    /// The CSI drivers whose volumes the system stores.
    pub drivers: Vec<String>,
    pub why: Option<String>,
    pub gotcha: Option<String>,
    /// The system's own console, such as a NAS's web interface.  The
    /// page shows only a plain `http` or `https` URL as a link, and
    /// shows any other value as text.
    pub url: Option<String>,
}

/// The Outsides in use, by each backend they gather.
#[derive(Debug, Default)]
pub struct Outsides {
    /// The index of the `Outside` that each address or driver belongs
    /// to, keyed by the backend's name.  An address key is in lower
    /// case.
    by_backend: HashMap<String, usize>,
    outsides: Vec<Outside>,
    /// Each CSI driver's console URLs, from the `console` template that
    /// the driver's publisher writes on its `CSIDriver`.
    consoles: HashMap<String, Vec<String>>,
    pub conditions: Vec<(DocId, Condition)>,
}

impl Outsides {
    /// Indexes `outsides` by backend.  Each backend belongs to the first
    /// `Outside`, sorted by name, that lists it.  When two of them list
    /// the same backend, this function records a conflict condition on
    /// both.
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
                            "{backend} is listed by {} and {}; the first by name, {}, draws it",
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
    /// driver.
    pub fn of(&self, name: &str) -> Option<&Outside> {
        let at = self
            .by_backend
            .get(name)
            .or_else(|| self.by_backend.get(&name.to_ascii_lowercase()))?;
        self.outsides.get(*at)
    }

    /// The `Outside` whose title is `title`, which is the name of its
    /// ground box.
    pub fn titled(&self, title: &str) -> Option<&Outside> {
        self.outsides.iter().find(|outside| outside.title == title)
    }

    /// Every Outside, by name.
    pub fn iter(&self) -> impl Iterator<Item = &Outside> {
        self.outsides.iter()
    }

    /// The Outside named `name` in its metadata.
    pub fn named(&self, name: &str) -> Option<&Outside> {
        self.outsides.iter().find(|outside| outside.name == name)
    }

    /// The `Outside` that `name` matches in any of the ways a page or a
    /// link may name it: its box's title, its own name, or an address
    /// or driver it lists.
    pub fn resolve(&self, name: &str) -> Option<&Outside> {
        self.titled(name)
            .or_else(|| self.named(name))
            .or_else(|| self.of(name))
    }

    /// The path of the page for the ground box `name`, a title or a
    /// backend.  The path is `/ground/` and the `Outside`'s name, or the
    /// backend's own name when no `Outside` lists the backend.
    pub fn page_of(&self, name: &str) -> String {
        let name = self.resolve(name).map_or(name, |outside| &outside.name);
        format!("/ground/{}", transect_graph::links::segment(name))
    }

    /// `self` with each CSI driver's console URLs.
    pub fn with_consoles(self, consoles: HashMap<String, Vec<String>>) -> Self {
        Self { consoles, ..self }
    }

    /// The URL that the ground box `name` links to.  When the box's
    /// `Outside` gives a `url`, the link is that URL if a browser may
    /// open it, and none otherwise.  Without a `url`, the link is the
    /// drivers' console when they give exactly one.
    pub fn link_of(&self, name: &str) -> Option<String> {
        if let Some(outside) = self.resolve(name).filter(|o| o.url.is_some()) {
            return outside.link().map(str::to_string);
        }
        match self.consoles_of(name).as_slice() {
            [one] => Some(one.clone()),
            _ => None,
        }
    }

    /// The console URLs of the drivers in the ground box `name`.  The
    /// list is empty when the box's `Outside` gives a `url`, because
    /// that URL overrides the consoles.
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

    /// The name of the box that shows the backend `name`: its
    /// `Outside`'s title, or the backend's own name.
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

    /// `end`, renamed to its box when it is outside the cluster.
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
    /// Its `url`, when it is a plain `http` or `https` URL a browser
    /// can open without running anything.
    pub fn link(&self) -> Option<&str> {
        let url = self.url.as_deref()?;
        let lower = url.to_ascii_lowercase();
        let plain = lower.starts_with("https://") || lower.starts_with("http://");
        let spaced = url.chars().any(|c| c.is_whitespace() || c == '"');
        (plain && !spaced).then_some(url)
    }

    /// Its `why` and `gotcha`, each marked as written in this resource.
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

/// The maximum length of a line of an `Outside`'s intent, in characters.
const LINE_CHARS: usize = 300;

#[cfg(any(test, feature = "fixtures"))]
pub mod fixtures {
    use super::*;

    /// The Outside `spec` declares as `name`.
    pub fn outside(name: &str, spec: &str) -> Outside {
        let text = format!("metadata: {{name: {name}}}\nspec: {spec}\n");
        parse(DocId::default(), serde_yaml::from_str(&text).unwrap()).unwrap()
    }
}

#[cfg(test)]
mod tests;
