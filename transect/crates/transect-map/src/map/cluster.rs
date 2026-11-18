//! The cluster map: one band for each layer, and in each band one tile
//! for each namespace.

use std::collections::HashMap;

use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;

use transect_graph::edges::Loop;
use transect_ground::loss::{self, Loss};
use transect_observe::health::{Health, Level};
use transect_resources::layers::Layers;

use super::relation::{self, Relation};

/// The name of the band for namespaces that no layer places.
pub const UNSORTED: &str = "unsorted";

#[derive(Debug)]
pub struct ClusterMap {
    pub bands: Vec<Band>,
}

#[derive(Debug)]
pub struct Band {
    pub name: String,
    pub tiles: Vec<NamespaceTile>,
}

#[derive(Debug, PartialEq)]
pub struct NamespaceTile {
    pub namespace: String,
    pub workloads: usize,
    /// The namespace's health, with the reasons that the sidebar
    /// repeats.
    pub health: Health,
    pub selected: bool,
    /// How the edges of the selected namespace relate it to this one.
    pub relations: Vec<Relation>,
    /// Whether the namespace is in a loop. The tile shows the loop
    /// mark whichever namespace is selected, because a loop does not
    /// depend on the selection.
    pub looped: bool,
    /// What the namespace loses when the page shows machines as lost,
    /// or `None` when it loses nothing.
    pub loss: Option<loss::NamespaceTile>,
}

/// The selected namespace and the band that contains it, which the crumb
/// names.
#[derive(Debug, PartialEq)]
pub struct Selected {
    pub namespace: String,
    pub layer: String,
}

impl NamespaceTile {
    /// How wide the tile is in its band. A namespace with no
    /// workloads gets the width of one, so its name stays readable.
    pub fn weight(&self) -> usize {
        self.workloads.max(1)
    }

    /// The CSS class that colors the tile, from the worse of its
    /// health and its loss level, so a namespace that is already failing
    /// keeps its color while a loss is shown. A healthy tile has no
    /// class and stays gray.
    pub fn health_class(&self) -> &'static str {
        let lost = self.loss.as_ref().map(|loss| loss.level);
        match self.health.level.max(lost.unwrap_or_default()) {
            Level::Healthy | Level::Notice => "",
            Level::Warning => " ns-tile-warning",
            Level::Failing => " ns-tile-failing",
        }
    }

    /// The text that replaces the workload count while the selected
    /// namespace has a relation to this tile, or an empty string when
    /// it has none. The text is the first relation in the order that
    /// `relate` sorts them, then a count of the others.
    pub fn relation(&self) -> String {
        let Some(first) = self.relations.first() else {
            return String::new();
        };
        match self.relations.len() {
            1 => first.short(),
            many => format!("{} +{}", first.short(), many - 1),
        }
    }

    /// Every relation, joined into one line for the tile's title.
    pub fn relation_title(&self) -> String {
        let long: Vec<String> = self
            .relations
            .iter()
            .map(|relation| relation.long())
            .collect();
        long.join(" · ")
    }

    /// Where a click on the tile goes. An unselected tile links to
    /// the map with it selected. A selected tile links to `away`, the
    /// map with nothing selected, so a second click undoes the first.
    /// The card's name opens the namespace's page.
    pub fn href(&self, away: &str) -> String {
        match self.selected {
            true => away.into(),
            false => format!("/?select={}", self.namespace),
        }
    }
}

impl ClusterMap {
    /// Marks `namespace`'s tile as selected and returns where it is,
    /// or `None` when no band contains it. Selection does not change the
    /// layout.
    pub fn select(&mut self, namespace: &str) -> Option<Selected> {
        self.bands.iter_mut().find_map(|band| {
            let tile = band
                .tiles
                .iter_mut()
                .find(|tile| tile.namespace == namespace)?;
            tile.selected = true;
            Some(Selected {
                namespace: tile.namespace.clone(),
                layer: band.name.clone(),
            })
        })
    }

    /// Adds `relation` to `namespace`'s tile, once for each kind and
    /// direction, and sorts the tile's relations into display order.
    pub fn relate(&mut self, namespace: &str, relation: Relation) {
        let tiles = self.bands.iter_mut().flat_map(|band| &mut band.tiles);
        for tile in tiles.filter(|tile| tile.namespace == namespace) {
            relation::add(&mut tile.relations, relation);
            relation::arrange(&mut tile.relations);
        }
    }

    pub fn mark_loops(&mut self, loops: &[Loop]) {
        let tiles = self.bands.iter_mut().flat_map(|band| &mut band.tiles);
        for tile in tiles {
            tile.looped = loops.iter().any(|found| found.holds(&tile.namespace));
        }
    }

    pub fn mark_loss(&mut self, loss: &Loss) {
        let tiles = self.bands.iter_mut().flat_map(|band| &mut band.tiles);
        for tile in tiles {
            tile.loss = loss.tile(&tile.namespace, tile.workloads);
        }
    }

    /// The name of the band that contains `namespace`, which the crumb
    /// of each page below the cluster map shows.
    pub fn layer_of(&self, namespace: &str) -> Option<&str> {
        let band = self
            .bands
            .iter()
            .find(|band| band.tiles.iter().any(|tile| tile.namespace == namespace))?;
        Some(&band.name)
    }
}

impl ClusterMap {
    /// Places each namespace in the band of its layer, with the bands in
    /// layer order. The tiles in a band are sorted by name, so a
    /// tile keeps its position as its workload count changes and a
    /// reader finds a name alphabetically. A tile's width still
    /// follows its workload count. The unsorted band comes last and
    /// exists only when some namespace was placed in it. A namespace
    /// missing from `health` is healthy. `known` maps a namespace to the
    /// layer that the kind knowledge gives for it. `Layers::place` uses
    /// that layer only when neither the namespace's `layer` annotation
    /// nor a layer's namespace selector places the namespace.
    pub fn build<'a>(
        layers: &Layers,
        namespaces: impl IntoIterator<Item = &'a ObjectMeta>,
        workloads: &HashMap<String, usize>,
        mut health: HashMap<String, Health>,
        known: &HashMap<String, String>,
    ) -> Self {
        let mut bands: Vec<Band> = layers.names().map(Band::named).collect();
        let mut unsorted = Band::named(UNSORTED);
        for namespace in namespaces {
            let Some(name) = namespace.name.clone() else {
                continue;
            };
            let tile = NamespaceTile {
                workloads: workloads.get(&name).copied().unwrap_or(0),
                health: health.remove(&name).unwrap_or_default(),
                selected: false,
                relations: Vec::new(),
                looped: false,
                loss: None,
                namespace: name,
            };
            match layers.place(namespace, known.get(&tile.namespace).map(String::as_str)) {
                Some(index) => bands[index].tiles.push(tile),
                None => unsorted.tiles.push(tile),
            }
        }
        if !unsorted.tiles.is_empty() {
            bands.push(unsorted);
        }
        for band in &mut bands {
            band.tiles
                .sort_by(|a, b| by_name(&a.namespace, &b.namespace));
        }
        Self { bands }
    }
}

/// Compares two namespace names alphabetically, ignoring case, and by
/// exact name when they differ only in case.
fn by_name(a: &str, b: &str) -> std::cmp::Ordering {
    a.to_lowercase()
        .cmp(&b.to_lowercase())
        .then_with(|| a.cmp(b))
}

impl Band {
    fn named(name: &str) -> Self {
        Self {
            name: name.into(),
            tiles: Vec::new(),
        }
    }
}

#[cfg(test)]
mod tests;
