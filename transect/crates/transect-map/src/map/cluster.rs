//! The cluster elevation: one band per layer, each holding a parcel per
//! namespace.

use std::collections::HashMap;

use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;

use transect_graph::edges::Loop;
use transect_ground::loss::{self, Loss};
use transect_observe::health::{Health, Level};
use transect_resources::layers::Layers;

use super::relation::{self, Relation};

pub const UNSORTED: &str = "unsorted";

#[derive(Debug)]
pub struct ClusterMap {
    pub bands: Vec<Band>,
}

#[derive(Debug)]
pub struct Band {
    pub name: String,
    pub parcels: Vec<Parcel>,
}

#[derive(Debug, PartialEq)]
pub struct Parcel {
    pub namespace: String,
    pub workloads: usize,
    /// The namespace's health, with the reasons the sidebar repeats.
    pub health: Health,
    pub selected: bool,
    /// How the selected namespace's edges relate it to this one.
    pub relations: Vec<Relation>,
    /// Whether the namespace is in a loop.  The parcel shows the loop mark
    /// whatever is selected, apart from the tags, because a loop is a fact
    /// about the cluster and not about the selection.
    pub looped: bool,
    /// What the loss the page shows takes from the namespace, if any.
    pub loss: Option<loss::Parcel>,
}

/// The selected namespace and the band that contains it, which the crumb
/// names.
#[derive(Debug, PartialEq)]
pub struct Selected {
    pub namespace: String,
    pub layer: String,
}

impl Parcel {
    /// How much room the parcel takes in its band.  A namespace with no
    /// workloads still gets the room of one, so its name stays readable.
    pub fn weight(&self) -> usize {
        self.workloads.max(1)
    }

    /// The class that colors the parcel: the worse of its health and
    /// what the loss the page shows takes from it, so a namespace failing
    /// today keeps its color under a loss.  A healthy parcel has none and
    /// stays gray.
    pub fn health_class(&self) -> &'static str {
        let lost = self.loss.as_ref().map(|loss| loss.level);
        match self.health.level.max(lost.unwrap_or_default()) {
            Level::Healthy | Level::Notice => "",
            Level::Warning => " parcel-warning",
            Level::Failing => " parcel-failing",
        }
    }

    /// The line that replaces the workload count while the selected
    /// namespace has a relation to the parcel, or nothing when it has
    /// none.  The line names the most important relation and how many
    /// more there are.
    pub fn relation(&self) -> String {
        let Some(first) = self.relations.first() else {
            return String::new();
        };
        match self.relations.len() {
            1 => first.short(),
            many => format!("{} +{}", first.short(), many - 1),
        }
    }

    /// Every relation, for the parcel's title.
    pub fn relation_title(&self) -> String {
        let said: Vec<String> = self
            .relations
            .iter()
            .map(|relation| relation.long())
            .collect();
        said.join(" · ")
    }

    /// Where a click on the parcel goes: the map with it selected, or,
    /// once it is selected, `away`, where the map goes with nothing
    /// selected.  A second click undoes the first rather than leaving the
    /// map, and the card's name opens the namespace's page.
    pub fn href(&self, away: &str) -> String {
        match self.selected {
            true => away.into(),
            false => format!("/?select={}", self.namespace),
        }
    }
}

impl ClusterMap {
    /// Marks `namespace`'s parcel selected and returns its place, or
    /// `None` when no band contains it.  Selection marks a parcel and moves
    /// nothing, so the map keeps its layout whatever is selected.
    pub fn select(&mut self, namespace: &str) -> Option<Selected> {
        self.bands.iter_mut().find_map(|band| {
            let parcel = band
                .parcels
                .iter_mut()
                .find(|parcel| parcel.namespace == namespace)?;
            parcel.selected = true;
            Some(Selected {
                namespace: parcel.namespace.clone(),
                layer: band.name.clone(),
            })
        })
    }

    /// Adds `relation` to `namespace`'s parcel, once for its kind and
    /// direction, and keeps the parcel's relations in the order the parcel
    /// shows them.
    pub fn relate(&mut self, namespace: &str, relation: Relation) {
        let parcels = self.bands.iter_mut().flat_map(|band| &mut band.parcels);
        for parcel in parcels.filter(|parcel| parcel.namespace == namespace) {
            relation::add(&mut parcel.relations, relation);
            relation::arrange(&mut parcel.relations);
        }
    }

    /// Marks the parcel of each namespace in one of `loops`.
    pub fn mark_loops(&mut self, loops: &[Loop]) {
        let parcels = self.bands.iter_mut().flat_map(|band| &mut band.parcels);
        for parcel in parcels {
            parcel.looped = loops.iter().any(|found| found.holds(&parcel.namespace));
        }
    }

    /// Gives each parcel what `loss` takes from its namespace.
    pub fn mark_loss(&mut self, loss: &Loss) {
        let parcels = self.bands.iter_mut().flat_map(|band| &mut band.parcels);
        for parcel in parcels {
            parcel.loss = loss.parcel(&parcel.namespace, parcel.workloads);
        }
    }

    /// The name of the band that contains `namespace`, which the crumb of
    /// every elevation below the cluster names.
    pub fn layer_of(&self, namespace: &str) -> Option<&str> {
        let band = self.bands.iter().find(|band| {
            band.parcels
                .iter()
                .any(|parcel| parcel.namespace == namespace)
        })?;
        Some(&band.name)
    }
}

impl ClusterMap {
    /// Places each namespace in its layer's band, in layer order, with the
    /// parcels by name, so a parcel keeps its place as its workloads
    /// come and go and a reader finds a name in alphabetical order.  A
    /// parcel's width still grows with its workloads.  The unsorted band
    /// comes last, and only when some namespace was placed in it.  A
    /// namespace missing from `health` is healthy.  `known` maps each
    /// namespace to the layer that its kinds' knowledge names, which
    /// places a namespace that nothing else places.
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
            let parcel = Parcel {
                workloads: workloads.get(&name).copied().unwrap_or(0),
                health: health.remove(&name).unwrap_or_default(),
                selected: false,
                relations: Vec::new(),
                looped: false,
                loss: None,
                namespace: name,
            };
            match layers.place(namespace, known.get(&parcel.namespace).map(String::as_str)) {
                Some(index) => bands[index].parcels.push(parcel),
                None => unsorted.parcels.push(parcel),
            }
        }
        if !unsorted.parcels.is_empty() {
            bands.push(unsorted);
        }
        for band in &mut bands {
            band.parcels
                .sort_by(|a, b| by_name(&a.namespace, &b.namespace));
        }
        Self { bands }
    }
}

/// Two namespaces' names in alphabetical order, ignoring case.
fn by_name(a: &str, b: &str) -> std::cmp::Ordering {
    a.to_lowercase()
        .cmp(&b.to_lowercase())
        .then_with(|| a.cmp(b))
}

impl Band {
    fn named(name: &str) -> Self {
        Self {
            name: name.into(),
            parcels: Vec::new(),
        }
    }
}

#[cfg(test)]
mod tests;
