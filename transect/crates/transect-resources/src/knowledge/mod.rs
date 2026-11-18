//! Kind knowledge is a `transect` resource that tells `transect` how to
//! read the objects of one custom resource definition: which condition
//! means an object works, which layer its namespaces belong to, which
//! fields refer to other objects, which image its controller runs, and
//! more. The project that owns the kind writes it.
//!
//! A kind knowledge resource has the name of its definition,
//! `<plural>.<group>`, so `Kinds::resolve` matches it to a kind through
//! the plurals that discovery returned.

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
    /// The first line of a card for an object of this kind that has no
    /// `why` of its own.
    pub summary: Option<String>,
    /// The layer that the namespaces of its objects belong to. It is
    /// kept only when the cluster's `Layers` declares it.
    pub layer: Option<String>,
    /// The condition type whose status `True` means an object works.
    pub healthy: Option<String>,
    /// Reasons of a `False` `healthy` condition that mean the object is
    /// idle, not failing, such as `Idle`. The page draws such an object
    /// gray.
    pub calm: Vec<String>,
    pub references: Vec<Reference>,
    /// The annotations on objects of other kinds that name objects of
    /// this kind.
    pub referenced_by: Vec<ReferencedBy>,
    /// The field that names the `Secret` an object writes.
    pub writes_secret: Option<String>,
    /// The image repository that its controller runs, without a
    /// registry host.
    pub controller_image: Option<String>,
    pub disruptions: Option<Disruptions>,
    /// The field that names the primary pod, for a kind with one
    /// primary that takes writes and replicas that follow it. Losing the
    /// primary stops writes until the operator promotes a replica, and
    /// losing a replica does not.
    pub primary: Option<String>,
    pub docs: Option<String>,
    /// How the kind applies objects and fetches sources, when it is a
    /// GitOps tool's kind.
    pub gitops: Option<GitOpsFields>,
    pub ignore_changes: Vec<IgnoreChange>,
    /// The field that holds the name of the `Node` that an object of a
    /// cluster-scoped kind describes, such as `metadata.name` when the
    /// object is named for its `Node`.
    pub node: Option<String>,
    /// How to read the findings in an object, when the kind is a
    /// vulnerability scanner's report.
    pub findings: Option<findings::Findings>,
}

/// A field of the kind's spec or status that refers to another object,
/// an address, or a value.
#[derive(Clone, Debug, PartialEq)]
pub struct Reference {
    /// A field path, with `[]` for each item of a list.
    pub path: String,
    pub target: Target,
    pub edge: Kind,
    /// The phrase that a card shows for the relation, such as "plays
    /// through".
    pub label: Option<String>,
    /// The field of the referenced object whose value equals this
    /// field's. It is set for a reference by value, such as an address,
    /// instead of by name.
    pub matched: Option<String>,
    /// Whether a match may select an object in another namespace than
    /// the referring object's. An object of a cluster-scoped kind always
    /// matches.
    pub across_namespaces: bool,
    /// What the referenced object does for the `Secret` that this
    /// object writes, when a page reads that role.
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
    /// The template of one object's name in the annotation's value,
    /// with `{namespace}` and `{name}` as placeholders. A template
    /// without `{namespace}` matches only an object in the annotated
    /// object's namespace, or of a cluster-scoped kind.
    pub name: String,
    /// What the named object does for the annotated one, when a page
    /// reads that role.
    pub role: Option<Role>,
}

/// What an object does for the object that names it in an annotation,
/// or for an object that writes a `Secret` and refers to it.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Role {
    /// The object issues the TLS certificate of the `Ingress` that
    /// names it, or of the `Ingress` that serves the `Secret` that the
    /// referring object writes. The page labels the entry "TLS from".
    IssuesTls,
}

/// What the field of a reference refers to.
#[derive(Clone, Debug, PartialEq)]
pub enum Target {
    /// An object, by kind, and by group when it is not the core group.
    Object { kind: String, group: Option<String> },
    /// An address, which `transect` resolves to the object that has it.
    Address,
    /// A thing that `transect` has no object for, such as a monitor. A
    /// card shows the field's value.
    Value,
}

/// Who prepares the kind's pods for eviction.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Disruptions {
    /// The kind's operator moves work off a pod before the pod is
    /// evicted, so a `PodDisruptionBudget` that allows no eviction is
    /// expected.
    Operator,
}

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
    /// of the entry stays in use.
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

/// The plural of a kind as a card prints it. The API plural is lower
/// case, so the label keeps the kind's spelling for the prefix that the
/// two share, ignoring case, and takes the rest from the plural:
/// `BackendTLSPolicy` and `backendtlspolicies` print
/// "BackendTLSPolicies", and `Person` and `people` print "People". With
/// no plural, or one that does not start like the kind, the label is
/// the kind.
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
    /// Whether the knowledge reads fields that a trimmed watch drops. A
    /// trimmed watch keeps only the metadata, the conditions, and
    /// `observedGeneration`, so `references`, `writes_secret`,
    /// `primary`, and a `node` path outside `metadata` need the whole
    /// object.
    pub fn reads_fields(&self) -> bool {
        !self.references.is_empty()
            || self.writes_secret.is_some()
            || self.primary.is_some()
            || self
                .node
                .as_deref()
                .is_some_and(|path| !path.starts_with("metadata."))
    }

    /// The field paths that a trimmed watch keeps for the kind beyond
    /// the metadata and conditions: the ones that its `gitops` and
    /// `findings` knowledge read.
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
    fn reports_whether_it_reads_fields_a_trimmed_watch_drops(
        #[case] spec: &str,
        #[case] reads: bool,
    ) {
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
    fn labels_a_kinds_plural(
        #[case] kind: &str,
        #[case] plural: Option<&str>,
        #[case] label: &str,
    ) {
        assert_eq!(plural_label(kind, plural), label);
    }

    #[rstest]
    #[case::operator("{disruptions: operator}", true)]
    #[case::not_declared("{}", false)]
    fn reports_whether_the_operator_moves_work_before_eviction(
        #[case] spec: &str,
        #[case] moves: bool,
    ) {
        let kinds = resolved("widgets.widgets.example.com", spec);
        let widget = Object::new("widgets.example.com", "Widget", Some("shop"), "a");
        assert_eq!(kinds.moves_before_eviction(&widget), moves);
    }
}
