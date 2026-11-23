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
pub mod catalog;
pub mod columns;
mod counts;
mod defined;
mod drawing;
pub mod elevation;
pub mod findings;
pub mod gitops;
pub mod grid;
mod guess;
pub mod jsonpath;
mod kinds;
mod layer;
mod matches;
mod parse;
pub mod path;
mod reach;
mod referenced;
mod references;
pub mod role;
mod stated;
pub mod through;
pub mod wire;

pub use age::age;
pub use card::{KnownThrough, Related};
pub use columns::{Column, Columns, KEPT, Read as ColumnRead};
pub use counts::{Count, counts};
pub use defined::Definitions;
pub use elevation::Elevation;
pub use gitops::{GitOpsFields, IgnoreChange, MetaKey};
pub use grid::Grid;
pub use guess::{Clue, Hint, clues};
pub use kinds::Kinds;
pub use layer::{Named, named};
pub use matches::{Key, Matches};
pub use parse::{edge_kind, parse};
pub use reach::{Reached, edges};
pub use referenced::{referrals, referred_edges};
pub use references::{Names, Relation, relations, target_of, written_secret};
pub use stated::Stated;
pub use through::{Followed, ReferenceEnds, Through, follow};

use std::sync::Arc;

use crate::resources::DocId;
use crate::shapes::{Shape, Shapes, library};
use transect_graph::edges::{Kind, Provenance};

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
    /// What says that an object works.
    pub healthy: Option<Healthy>,
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
    /// The name of the shape that draws an object of the kind as a part
    /// of any sketch, as the document writes it. The operator that owns
    /// the kind states it, so every sketch that draws one of its objects
    /// draws it the same way.
    pub shape: Option<String>,
    /// The definition that `shape` names, which `Kinds::find_shapes`
    /// finds. It is none until then, and when the kind states no shape
    /// or names one that is not loaded.
    pub drawn: Option<Arc<Shape>>,
    /// The roles that the elevations place, each stated once, in the
    /// order of the document.
    pub roles: Vec<role::Role>,
    /// The arrangements of roles that the author states for the kind, in
    /// the author's order. The page of an object draws the first in place
    /// of the ring of boxes, and each at a URL of its own.
    pub elevations: Vec<Elevation>,
    /// The pattern of `transect`'s catalog that the kind fills, whose
    /// elevations the page draws after the kind's own.
    pub pattern: Option<catalog::Pattern>,
}

/// What says that an object of a kind works.
#[derive(Clone, Debug, PartialEq)]
pub enum Healthy {
    /// A condition of the object's own: `True` means it works. A `False`
    /// condition with one of the `calm` reasons, such as `Idle`, means
    /// the object is at rest, not failing, and the page draws it gray.
    Condition {
        condition: String,
        calm: Vec<String>,
    },
    /// The object is as healthy as the object that one of its
    /// references names, by that object's own knowledge.
    Through(Through),
}

/// A field of the kind's spec or status that refers to another object,
/// an address, or a value.
#[derive(Clone, Debug, PartialEq)]
pub struct Reference {
    /// A field path, with `[]` for each item of a list, or the template
    /// of the referenced object's name when `by_name` is set. A role's
    /// `reference` names the entry by this text.
    pub path: String,
    /// Whether `path` is a template of the referenced object's name,
    /// with `{name}` and `{namespace}` for the referring object, which
    /// reads no field. An operator often names an object for the object
    /// that owns it and records the name in no field.
    pub by_name: bool,
    pub target: Target,
    pub edge: Kind,
    /// The phrase that a card shows for the relation, such as "amplified
    /// by".
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
    pub role: Option<Duty>,
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
    pub role: Option<Duty>,
}

/// What an object does for the object that names it in an annotation,
/// or for an object that writes a `Secret` and refers to it.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Duty {
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
    /// `observedGeneration`, so `references`, `writes_secret`, and a
    /// `node` path outside `metadata` need the whole object. Each role
    /// of a `pattern` names an entry of `references`.
    pub fn reads_fields(&self) -> bool {
        !self.references.is_empty()
            || self.writes_secret.is_some()
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

    /// The condition type of the object's own that says it works, when
    /// the knowledge names one.
    pub fn condition(&self) -> Option<&str> {
        match &self.healthy {
            Some(Healthy::Condition { condition, .. }) => Some(condition),
            _ => None,
        }
    }

    /// The reasons of a `False` health condition that mean the object
    /// is at rest.
    pub fn calm(&self) -> &[String] {
        match &self.healthy {
            Some(Healthy::Condition { calm, .. }) => calm,
            _ => &[],
        }
    }

    /// Finds the shape that `shape` names in `shapes`: the author's
    /// shape in the kind's own group first, then the library's. It
    /// returns the message of a condition when the kind names a shape
    /// that is not there, and its objects are then drawn in the default
    /// shape.
    pub fn find_shape(&mut self, shapes: &Shapes) -> Option<String> {
        let written = self.shape.as_ref()?;
        self.drawn = shapes.resolve(written, &self.group).cloned();
        self.drawn.is_none().then(|| {
            format!(
                "the kind's shape is {written:?}, which transect has not loaded, so its objects are drawn as a {}",
                library::DEFAULT
            )
        })
    }

    /// Finds the shape of each role, and leaves out each wire that meets
    /// a role whose shape has no port on the facing edge, for the kind's
    /// own roles and for the roles of its pattern. `of_kind` gives the
    /// shape that the knowledge of a kind states, by group and kind. It
    /// returns the message of a condition for each role whose shape is
    /// not there, and for each wire it left out.
    pub fn draw_with(
        &mut self,
        shapes: &Shapes,
        of_kind: impl Fn(&str, &str) -> Option<Arc<Shape>>,
    ) -> (Vec<String>, Vec<String>) {
        let drawing = drawing::Drawing {
            group: &self.group,
            references: &self.references,
            shapes,
            of_kind: &of_kind,
        };
        let mut faults = drawing.draw(&mut self.roles, &mut self.elevations);
        if let Some(pattern) = &mut self.pattern {
            let (unknown, unported) = drawing.draw(&mut pattern.roles, &mut pattern.elevations);
            faults.0.extend(unknown);
            faults.1.extend(unported);
        }
        faults
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
    use transect_graph::edges::Plurals;

    /// The knowledge `spec` declares for `name`, with each role drawn
    /// in a shape of the library, as if no other kind stated a shape.
    pub fn knowledge(name: &str, spec: &str) -> KindKnowledge {
        let text = format!("metadata: {{name: {name}}}\nspec: {spec}\n");
        let mut known = parse(DocId::default(), serde_yaml_ng::from_str(&text).unwrap()).unwrap();
        known.draw_with(&Shapes::default(), |_, _| None);
        known
    }

    /// A cluster in which every reference names nothing, for a test that
    /// follows none.
    pub struct Nowhere;

    impl ReferenceEnds for Nowhere {
        fn ends(&self, _: &transect_graph::edges::Object, _: &str) -> Vec<Followed<'_>> {
            Vec::new()
        }
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

    use super::*;

    #[rstest]
    #[case::a_summary_and_a_controller(
        "{summary: Scans., controller: {image: example/scanner}}",
        false
    )]
    #[case::a_healthy_condition("{healthy: {condition: Ready}}", false)]
    #[case::a_reference("{references: [{path: spec.node, kind: Node}]}", true)]
    #[case::a_node_by_its_name("{node: metadata.name}", false)]
    #[case::a_node_in_its_status("{node: status.nodeName}", true)]
    #[case::an_elevation(
        "{references: [{path: spec.node, kind: Node}], roles: {node: {reference: spec.node}}, elevations: [{name: the rack, question: Where is it?, sketch: node}]}",
        true
    )]
    fn reports_whether_it_reads_fields_a_trimmed_watch_drops(
        #[case] spec: &str,
        #[case] reads: bool,
    ) {
        let known = fixtures::knowledge("widgets.widgets.example.com", spec);
        assert_eq!(known.reads_fields(), reads);
    }

    #[rstest]
    #[case::an_s("Dial", Some("dials"), "Dials")]
    #[case::capitals_inside("DialSetting", Some("dialsettings"), "DialSettings")]
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
}
