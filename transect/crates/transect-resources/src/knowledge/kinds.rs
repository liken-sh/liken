//! The kind knowledge that applies to one reading of the cluster: each
//! document matched to a kind that the cluster serves, with the
//! conditions of the checks that need the cluster or another document.

use std::collections::HashMap;
use std::sync::Arc;

use super::{Disruptions, KindKnowledge};
use crate::resources::{Condition, DocId};
use crate::shapes::{Shape, Shapes};
use transect_graph::edges::{Object, Plurals};

/// The kind knowledge for one reading of the cluster, by group and
/// kind, with the conditions that `resolve` created.
#[derive(Debug, Default)]
pub struct Kinds {
    by_kind: HashMap<(String, String), KindKnowledge>,
    pub conditions: Vec<(DocId, Condition)>,
}

impl Kinds {
    /// Matches each entry of `knowledge` to the kind that `plurals`
    /// serves under the entry's name. An entry whose kind is not served
    /// is left out, with a condition. In an entry whose layer is not in
    /// `layers`, the layer is cleared, with a condition, and the rest
    /// of the entry stays in use. Each `through` that the knowledge of
    /// another kind must confirm is checked last, against the entries
    /// that are served.
    pub fn resolve(knowledge: &[KindKnowledge], plurals: &Plurals, layers: &[&str]) -> Self {
        let mut kinds = Self::default();
        for one in knowledge {
            let served = plurals
                .iter()
                .find(|((group, _), plural)| *group == one.group && **plural == one.plural);
            let Some(((group, kind), _)) = served else {
                let message = format!("the cluster serves no {}", one.name);
                kinds
                    .conditions
                    .push((one.doc, Condition::not_served(message)));
                continue;
            };
            let mut one = one.clone();
            if let Some(layer) = &one.layer
                && !layers.contains(&layer.as_str())
            {
                let message = format!("layer {layer:?} is not one the cluster's Layers declares");
                kinds
                    .conditions
                    .push((one.doc, Condition::unknown_layer(message)));
                one.layer = None;
            }
            kinds.by_kind.insert((group.clone(), kind.clone()), one);
        }
        kinds.resolve_throughs();
        kinds
    }

    /// Finds the shape of each kind and of each role in `shapes`, for
    /// the pages that draw the elevations. The kinds come first, because
    /// a role draws its parts in the shape of their kind when it states
    /// none. A kind or a role whose shape is not there is drawn in the
    /// default shape, and the knowledge that names the shape gets the
    /// condition `UnknownShape`. A wire that meets a role whose shape has
    /// no port on the facing edge is not drawn, and its knowledge gets
    /// the condition `NoPort`. Only knowledge of a served kind is here,
    /// so the shapes of a kind the cluster does not serve are not
    /// checked, and a part of such a kind is drawn in its role's shape
    /// or the default.
    pub fn find_shapes(&mut self, shapes: &Shapes) {
        for one in self.by_kind.values_mut() {
            if let Some(message) = one.find_shape(shapes) {
                let condition = Condition::unknown_shape(message);
                self.conditions.push((one.doc, condition));
            }
        }
        let drawn = self.by_kind.iter();
        let of_kind: HashMap<(String, String), Arc<Shape>> = drawn
            .filter_map(|(key, one)| Some((key.clone(), one.drawn.clone()?)))
            .collect();
        let of_kind = |group: &str, kind: &str| of_kind.get(&(group.into(), kind.into())).cloned();
        for one in self.by_kind.values_mut() {
            let (unknown, unported) = one.draw_with(shapes, of_kind);
            let unknown = unknown.into_iter().map(Condition::unknown_shape);
            let unported = unported.into_iter().map(Condition::no_port);
            let conditions = unknown
                .chain(unported)
                .map(|condition| (one.doc, condition));
            self.conditions.extend(conditions);
        }
    }

    pub fn of(&self, group: &str, kind: &str) -> Option<&KindKnowledge> {
        self.by_kind.get(&(group.to_string(), kind.to_string()))
    }

    pub fn of_object(&self, object: &Object) -> Option<&KindKnowledge> {
        self.of(&object.group, &object.kind)
    }

    pub fn kinds(&self) -> impl Iterator<Item = &(String, String)> {
        self.by_kind.keys()
    }

    /// Whether `object`'s kind declares `disruptions: operator`.
    pub fn moves_before_eviction(&self, object: &Object) -> bool {
        let known = self.of_object(object);
        known.is_some_and(|k| k.disruptions == Some(Disruptions::Operator))
    }
}

mod resolve;

#[cfg(test)]
mod tests;
