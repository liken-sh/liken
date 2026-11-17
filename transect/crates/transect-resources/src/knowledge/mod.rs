//! Kind knowledge: what the operator that owns a custom kind declares
//! about it, in one small resource per custom resource definition.  It
//! names the condition that means an object works, the layer its
//! namespaces belong to, a one-line summary, the references in its spec
//! and status, the image its controller runs, and whether a budget that
//! lets no pod go is its operator's design.
//!
//! The resource is named for the definition, `<plural>.<group>`, so it
//! is matched to a kind through the plurals discovery found.  A name
//! the cluster does not serve, or a layer no `Layers` declares, gets a
//! condition and is left out of the reading.

mod age;
mod card;
pub mod columns;
mod counts;
mod defined;
pub mod findings;
pub mod gitops;
mod guess;
pub mod jsonpath;
mod layer;
mod matches;
mod parse;
pub mod path;
mod reach;
mod referenced;
mod references;

use std::collections::HashMap;

pub use age::age;
pub use card::{KnownThrough, Related};
pub use columns::{Column, Columns, KEPT, Read as ColumnRead};
pub use counts::{Count, counts};
pub use defined::Definitions;
pub use gitops::{GitOpsFields, IgnoreChange, MetaKey};
pub use guess::{Clue, Hint, clues};
pub use layer::{Named, named};
pub use matches::{Key, Matches};
pub use parse::{edge_kind, parse};
pub use reach::{Reached, edges};
pub use referenced::{referrals, referred_edges};
pub use references::{Names, Relation, relations, written_secret};

use crate::resources::{Condition, DocId};
use transect_graph::edges::{Kind, Object, Plurals, Provenance};

#[derive(Clone, Debug, PartialEq)]
pub struct KindKnowledge {
    pub doc: DocId,
    /// The custom resource definition's name, `<plural>.<group>`.
    pub name: String,
    pub plural: String,
    pub group: String,
    /// The opening line of a card for any object of the kind with no
    /// `why` of its own.
    pub summary: Option<String>,
    /// The layer of namespaces that hold objects of the kind, when the
    /// cluster's `Layers` declares it.
    pub layer: Option<String>,
    /// The condition whose `True` means an object works.
    pub healthy: Option<String>,
    /// The reasons on a `False` of `healthy` that mean the object is at
    /// rest, such as `Idle`: it draws gray, not as a failure.
    pub calm: Vec<String>,
    pub references: Vec<Reference>,
    /// The annotations on other kinds that name objects of this kind.
    pub referenced_by: Vec<ReferencedBy>,
    /// The field that names the `Secret` an object writes.
    pub writes_secret: Option<String>,
    /// The image repository its controller runs, without a registry
    /// host.
    pub controller_image: Option<String>,
    pub disruptions: Option<Disruptions>,
    /// The field that names the pod which takes writes, for a kind that
    /// runs one primary and replicas that follow it.  Losing that pod
    /// stops writes until the operator promotes a replica; losing a
    /// replica only leaves the kind short.
    pub primary: Option<String>,
    pub docs: Option<String>,
    /// What a GitOps tool's kind declares about applying and fetching,
    /// when it is one.
    pub gitops: Option<GitOpsFields>,
    /// The metadata keys a tool rewrites without changing anything,
    /// which a watch ignores when it decides whether an object changed.
    pub ignore_changes: Vec<IgnoreChange>,
    /// The field naming the `Node` an object of a cluster-scoped kind
    /// describes, such as `metadata.name` for a resource named for the
    /// machine under its `Node`.
    pub node: Option<String>,
    /// How a report of a scanner's findings is read, when the kind is
    /// one.
    pub findings: Option<findings::Findings>,
}

/// One field of the kind's spec or status that names something.
#[derive(Clone, Debug, PartialEq)]
pub struct Reference {
    /// A field path, with `[]` for each item of a list.
    pub path: String,
    pub target: Target,
    pub edge: Kind,
    /// The verb a card shows, such as "plays through".
    pub label: Option<String>,
    /// The field of the far object whose value equals this field's, for
    /// a reference by value, such as an address, in place of by name.
    pub matched: Option<String>,
    /// Whether a match may join a far object in another namespace than
    /// the referring object's.  A far kind that is not namespaced
    /// always may.
    pub across_namespaces: bool,
    /// What the far object does for the `Secret` this object writes, when
    /// a page reads it.
    pub role: Option<Role>,
}

/// An annotation on objects of another kind that names objects of this
/// kind, such as an ingress controller's annotation that lists the
/// middlewares a route passes through.
#[derive(Clone, Debug, PartialEq)]
pub struct ReferencedBy {
    /// The annotated kind's group, empty for the core group.
    pub group: String,
    pub kind: String,
    pub annotation: String,
    /// How the annotation writes one object's name, with `{namespace}`
    /// and `{name}` for the object's own.  Without `{namespace}`, it
    /// names an object of a namespaced kind in the annotated object's
    /// own namespace.
    pub name: String,
    /// What the named object does for the annotated one, when a page
    /// reads it.
    pub role: Option<Role>,
}

/// What an object named by another's annotation, or by a reference of
/// an object that writes a `Secret`, does.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Role {
    /// It issues the TLS certificate of the `Ingress` that names it, or
    /// that serves the `Secret` the referring object writes, so the
    /// entry reads "TLS from" it.
    IssuesTls,
}

/// What a reference's field names.
#[derive(Clone, Debug, PartialEq)]
pub enum Target {
    /// An object, by kind, and by group when it is not the core group.
    Object { kind: String, group: Option<String> },
    /// An address, which address resolution reads.
    Address,
    /// Something `transect` has no object for, such as a monitor, which a
    /// card shows as the field's value.
    Value,
}

/// Who handles evictions of the kind's pods.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Disruptions {
    /// Its operator moves work off a pod before eviction, so a budget
    /// that lets none go is expected.
    Operator,
}

/// The kind knowledge in use for one reading of the cluster, by the
/// group and kind it describes, and the conditions the matching wrote.
#[derive(Debug, Default)]
pub struct Kinds {
    by_kind: HashMap<(String, String), KindKnowledge>,
    pub conditions: Vec<(DocId, Condition)>,
}

impl Kinds {
    /// Each of `knowledge` matched to the kind `plurals` serves under
    /// its name.  A layer that `layers` does not declare is dropped,
    /// with a condition, and the rest of the knowledge stays in use.
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
        kinds
    }

    /// The knowledge for objects of `group` and `kind`.
    pub fn of(&self, group: &str, kind: &str) -> Option<&KindKnowledge> {
        self.by_kind.get(&(group.to_string(), kind.to_string()))
    }

    /// The knowledge for `object`'s kind.
    pub fn of_object(&self, object: &Object) -> Option<&KindKnowledge> {
        self.of(&object.group, &object.kind)
    }

    /// Every kind with knowledge, by group and kind.
    pub fn kinds(&self) -> impl Iterator<Item = &(String, String)> {
        self.by_kind.keys()
    }

    /// Whether `object`'s operator moves its work before an eviction,
    /// so a budget that lets none of its pods go is by design.
    pub fn moves_before_eviction(&self, object: &Object) -> bool {
        let known = self.of_object(object);
        known.is_some_and(|k| k.disruptions == Some(Disruptions::Operator))
    }
}

/// A kind's plural as a card prints it: "Players" for a `Player` whose
/// plural is `players`.  The plural is the API's, with the kind's own
/// capitals over the letters the two share at the start, so
/// `BackendTLSPolicy` and `backendtlspolicies` print
/// "BackendTLSPolicies", and `Person` and `people` print "People".  A
/// plural that shares no letter with the kind, or none at all, leaves
/// the kind's name alone.
pub fn plural_label(kind: &str, plural: Option<&str>) -> String {
    let Some(plural) = plural else {
        return kind.to_string();
    };
    let shared = kind
        .char_indices()
        .zip(plural.chars())
        .take_while(|((_, k), p)| k.eq_ignore_ascii_case(p))
        .last()
        .map(|((at, k), _)| at + k.len_utf8());
    match shared {
        Some(at) if plural.is_char_boundary(at) => format!("{}{}", &kind[..at], &plural[at..]),
        _ => kind.to_string(),
    }
}

impl KindKnowledge {
    /// Whether it reads fields of an object beyond the metadata, the
    /// conditions, and the `observedGeneration` a trimmed watch keeps:
    /// a reference, the `Secret` it writes, its primary, or a `Node`
    /// named outside its metadata.  A summary, a layer, a
    /// healthy condition, a controller image, a disruption rule, docs,
    /// and the annotations of other kinds that name it read none.
    pub fn reads_fields(&self) -> bool {
        !self.references.is_empty()
            || self.writes_secret.is_some()
            || self.primary.is_some()
            || self
                .node
                .as_deref()
                .is_some_and(|path| !path.starts_with("metadata."))
    }

    /// The fields a trimmed watch keeps for the kind beyond its
    /// metadata and conditions: those its GitOps knowledge and its
    /// findings knowledge read.
    pub fn kept_paths(&self) -> Vec<String> {
        let gitops = self.gitops.iter().flat_map(GitOpsFields::paths);
        let findings = self.findings.iter().flat_map(findings::Findings::paths);
        gitops.chain(findings).collect()
    }

    /// The provenance of a fact read through `field` of this knowledge.
    pub fn provenance(&self, field: &str) -> Provenance {
        Provenance::Kinds {
            knowledge: self.name.clone(),
            field: field.into(),
        }
    }
}

#[cfg(any(test, feature = "fixtures"))]
pub mod fixtures {
    use super::*;

    /// The knowledge `spec` declares for `name`.
    pub fn knowledge(name: &str, spec: &str) -> KindKnowledge {
        let text = format!("metadata: {{name: {name}}}\nspec: {spec}\n");
        parse(DocId::default(), serde_yaml::from_str(&text).unwrap()).unwrap()
    }

    /// Plurals that serve `widgets.widgets.example.com` as `Widget`.
    pub fn plurals() -> Plurals {
        Plurals::from([(
            ("widgets.example.com".to_string(), "Widget".to_string()),
            "widgets".to_string(),
        )])
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::fixtures::{knowledge, plurals};
    use super::*;

    fn resolved(name: &str, spec: &str) -> Kinds {
        Kinds::resolve(&[knowledge(name, spec)], &plurals(), &["apps", "platform"])
    }

    fn reasons(kinds: &Kinds) -> Vec<&'static str> {
        kinds.conditions.iter().map(|(_, c)| c.reason).collect()
    }

    #[rstest]
    #[case::served("widgets.widgets.example.com", "{}", true, vec![])]
    #[case::not_served("gadgets.widgets.example.com", "{}", false, vec!["NotServed"])]
    #[case::an_unknown_layer("widgets.widgets.example.com", "{layer: edge}", true, vec!["UnknownLayer"])]
    #[case::a_declared_layer("widgets.widgets.example.com", "{layer: platform}", true, vec![])]
    fn matches_each_name_to_a_served_kind(
        #[case] name: &str,
        #[case] spec: &str,
        #[case] found: bool,
        #[case] expected: Vec<&'static str>,
    ) {
        let kinds = resolved(name, spec);
        let widget = kinds.of("widgets.example.com", "Widget");
        assert_eq!((widget.is_some(), reasons(&kinds)), (found, expected));
    }

    #[rstest]
    fn drops_a_layer_no_layers_declares() {
        let kinds = resolved(
            "widgets.widgets.example.com",
            "{layer: edge, summary: Turns.}",
        );
        let widget = kinds.of("widgets.example.com", "Widget").unwrap();
        assert_eq!(
            (widget.layer.as_deref(), widget.summary.as_deref()),
            (None, Some("Turns."))
        );
    }

    #[rstest]
    #[case::a_summary_and_a_controller(
        "{summary: Scans., controller: {image: example/scanner}}",
        false
    )]
    #[case::a_healthy_condition("{healthy: {condition: Ready}}", false)]
    #[case::a_reference("{references: [{path: spec.node, kind: Node}]}", true)]
    #[case::a_primary("{primary: status.currentPrimary}", true)]
    #[case::a_node_by_its_name("{node: metadata.name}", false)]
    #[case::a_node_in_its_status("{node: status.nodeName}", true)]
    fn says_whether_it_reads_fields_a_trimmed_watch_drops(#[case] spec: &str, #[case] reads: bool) {
        let known = fixtures::knowledge("widgets.widgets.example.com", spec);
        assert_eq!(known.reads_fields(), reads);
    }

    #[rstest]
    #[case::an_s("Player", Some("players"), "Players")]
    #[case::capitals_inside("MediaPreference", Some("mediapreferences"), "MediaPreferences")]
    #[case::the_same("Redis", Some("redis"), "Redis")]
    #[case::a_y_made_ies("BackendTLSPolicy", Some("backendtlspolicies"), "BackendTLSPolicies")]
    #[case::irregular("Person", Some("people"), "People")]
    #[case::not_from_the_kind("Widget", Some("gizmos"), "Widget")]
    #[case::no_plural("Widget", None, "Widget")]
    fn reads_a_kinds_plural(#[case] kind: &str, #[case] plural: Option<&str>, #[case] label: &str) {
        assert_eq!(plural_label(kind, plural), label);
    }

    #[rstest]
    #[case::operator("{disruptions: operator}", true)]
    #[case::unsaid("{}", false)]
    fn says_whether_its_operator_moves_work_first(#[case] spec: &str, #[case] moves: bool) {
        let kinds = resolved("widgets.widgets.example.com", spec);
        let widget = Object::new("widgets.example.com", "Widget", Some("shop"), "a");
        assert_eq!(kinds.moves_before_eviction(&widget), moves);
    }
}
