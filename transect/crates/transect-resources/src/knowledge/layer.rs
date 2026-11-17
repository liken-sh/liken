//! The layer kind knowledge names for a namespace: the layer of the
//! kinds whose objects are in it.  When its kinds name different
//! layers, the highest of them wins, and the namespace card names each.

use std::collections::{BTreeMap, BTreeSet, HashMap};
use std::fmt;

use super::{Kinds, plural_label};
use crate::layers::Layers;
use transect_graph::edges::{Object, Plurals, Provenance};

/// The layers a namespace's kinds name, and the one that places it.
#[derive(Clone, Debug, PartialEq)]
pub struct Named {
    /// The highest layer among them.
    pub layer: String,
    /// Each kind, as a card prints its plural, with the layer it names,
    /// highest layer first.
    pub kinds: Vec<(String, String)>,
    /// The `layer` of the first kind knowledge that names `layer`.
    pub provenance: Provenance,
}

/// The layer each namespace's kinds name, by namespace, among `objects`.
/// `layers` gives the order, top to bottom; the knowledge in `kinds`
/// names only declared layers.
pub fn named<'a>(
    kinds: &Kinds,
    objects: impl IntoIterator<Item = &'a Object>,
    layers: &Layers,
    plurals: &Plurals,
) -> HashMap<String, Named> {
    let mut found: BTreeMap<&str, BTreeSet<(usize, String, String)>> = BTreeMap::new();
    for object in objects {
        let Some(namespace) = object.namespace.as_deref() else {
            continue;
        };
        let Some(knowledge) = kinds.of_object(object) else {
            continue;
        };
        let Some(layer) = knowledge.layer.as_deref() else {
            continue;
        };
        let Some(at) = layers.position(layer) else {
            continue;
        };
        let plural = plurals.get(&(object.group.clone(), object.kind.clone()));
        let label = plural_label(&object.kind, plural.map(String::as_str));
        let name = knowledge.name.clone();
        found
            .entry(namespace)
            .or_default()
            .insert((at, label, name));
    }
    found
        .into_iter()
        .filter_map(|(namespace, named)| {
            let (_, _, first) = named.first()?;
            let provenance = Provenance::Kinds {
                knowledge: first.clone(),
                field: "layer".into(),
            };
            let kinds: Vec<(String, String)> = named
                .into_iter()
                .filter_map(|(at, label, _)| Some((label, layers.names().nth(at)?.to_string())))
                .collect();
            let layer = kinds.first()?.1.clone();
            let named = Named {
                layer,
                kinds,
                provenance,
            };
            Some((namespace.to_string(), named))
        })
        .collect()
}

impl Named {
    /// The layer of the namespace, by name, for the map.
    pub fn layers(named: &HashMap<String, Self>) -> HashMap<String, String> {
        let layers = named
            .iter()
            .map(|(namespace, named)| (namespace.clone(), named.layer.clone()));
        layers.collect()
    }
}

/// "Its Players place it in apps.", or "Its Players place it in apps;
/// its Catalogs name platform." when its kinds name different layers.
impl fmt::Display for Named {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let placing: Vec<&str> = self
            .kinds
            .iter()
            .filter(|(_, layer)| *layer == self.layer)
            .map(|(label, _)| label.as_str())
            .collect();
        write!(f, "Its {} place it in {}", and(&placing), self.layer)?;
        let mut others: BTreeMap<&str, Vec<&str>> = BTreeMap::new();
        for (label, layer) in self.kinds.iter().filter(|(_, layer)| *layer != self.layer) {
            others.entry(layer).or_default().push(label);
        }
        for (layer, labels) in others {
            write!(f, "; its {} name {layer}", and(&labels))?;
        }
        write!(f, ".")
    }
}

/// "Players", "Players and Catalogs", or "Players, Remotes and Catalogs".
fn and(labels: &[&str]) -> String {
    match labels {
        [] => String::new(),
        [one] => one.to_string(),
        [rest @ .., last] => format!("{} and {last}", rest.join(", ")),
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::fixtures::knowledge;
    use super::*;

    fn layers() -> Layers {
        Layers::from_yaml(
            "apiVersion: transect.liken.sh/v1alpha1\nkind: Layers\nspec: {layers: [{name: apps}, {name: platform}]}\n",
        )
        .unwrap()
    }

    fn plurals() -> Plurals {
        Plurals::from([
            (
                ("widgets.example.com".into(), "Widget".into()),
                "widgets".into(),
            ),
            (
                ("widgets.example.com".into(), "Gadget".into()),
                "gadgets".into(),
            ),
        ])
    }

    fn kinds(widget: &str, gadget: &str) -> Kinds {
        let knowledge = [
            knowledge("widgets.widgets.example.com", widget),
            knowledge("gadgets.widgets.example.com", gadget),
        ];
        Kinds::resolve(&knowledge, &plurals(), &["apps", "platform"])
    }

    fn object(kind: &str, namespace: &str) -> Object {
        Object::new("widgets.example.com", kind, Some(namespace), "a")
    }

    #[rstest]
    #[case::one_kind("{layer: platform}", "{}", Some("Its Widgets place it in platform."))]
    #[case::the_highest_of_two(
        "{layer: platform}",
        "{layer: apps}",
        Some("Its Gadgets place it in apps; its Widgets name platform.")
    )]
    #[case::two_that_agree(
        "{layer: apps}",
        "{layer: apps}",
        Some("Its Gadgets and Widgets place it in apps.")
    )]
    #[case::no_layer("{}", "{}", None)]
    fn names_the_layer_its_kinds_name(
        #[case] widget: &str,
        #[case] gadget: &str,
        #[case] line: Option<&str>,
    ) {
        let objects = [
            object("Widget", "shop"),
            object("Gadget", "shop"),
            object("Widget", "blog"),
        ];
        let found = named(&kinds(widget, gadget), &objects, &layers(), &plurals());
        let shop = found.get("shop").map(ToString::to_string);
        assert_eq!(shop.as_deref(), line);
    }
}
