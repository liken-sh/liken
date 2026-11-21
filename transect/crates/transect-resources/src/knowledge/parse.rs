//! Parses one `KindKnowledge` document, and runs the checks that need
//! only that document: the form of the name, and the values of `edge`,
//! `role`, and `disruptions`.

use serde::Deserialize;

use super::{
    Disruptions, KindKnowledge, Reference, ReferencedBy, Role, Target, elevation, findings, gitops,
};
use crate::resources::DocId;
use transect_graph::edges::Kind;
use transect_graph::untrusted::capped;

/// A card shows at most this many characters of a summary.
const SUMMARY_CHARS: usize = 300;

#[derive(Deserialize)]
struct Document {
    metadata: Metadata,
    spec: Spec,
}

#[derive(Deserialize)]
struct Metadata {
    name: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
struct Spec {
    summary: Option<String>,
    layer: Option<String>,
    healthy: Option<Healthy>,
    #[serde(default)]
    references: Vec<Written>,
    #[serde(default)]
    referenced_by: Vec<Referrer>,
    writes_secret: Option<String>,
    controller: Option<Controller>,
    disruptions: Option<String>,
    primary: Option<String>,
    docs: Option<String>,
    applies: Option<gitops::WrittenApplies>,
    #[serde(default)]
    source: Vec<String>,
    directory: Option<String>,
    chart: Option<String>,
    revision: Option<gitops::WrittenRevision>,
    #[serde(default)]
    repository: Vec<String>,
    #[serde(rename = "ref")]
    reference: Option<gitops::WrittenRefs>,
    #[serde(default)]
    ignore_changes: Vec<gitops::WrittenIgnore>,
    node: Option<String>,
    findings: Option<findings::Written>,
    #[serde(default)]
    elevations: Vec<elevation::Written>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Healthy {
    condition: String,
    /// Reasons that, on a `False` condition, mean the object is at rest
    /// rather than broken, so the map draws it gray.
    #[serde(default)]
    calm: Vec<String>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Controller {
    image: String,
}

/// One `referencedBy` entry: an annotation on objects of another kind
/// whose value is the name of an object of this kind.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Referrer {
    kind: String,
    group: Option<String>,
    annotation: String,
    name: String,
    role: Option<String>,
}

/// One `references` entry, as written in the document.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Written {
    path: String,
    kind: Option<String>,
    group: Option<String>,
    #[serde(default)]
    address: bool,
    edge: Option<String>,
    label: Option<String>,
    /// The field of the referenced object that must equal this field's
    /// value.
    #[serde(rename = "match")]
    matched: Option<String>,
    /// Whether `match` may match an object in another namespace.
    #[serde(default, rename = "acrossNamespaces")]
    across_namespaces: bool,
    role: Option<String>,
}

pub fn parse(doc: DocId, value: serde_yaml::Value) -> Result<KindKnowledge, String> {
    let document: Document = serde_yaml::from_value(value).map_err(|error| error.to_string())?;
    let name = document.metadata.name;
    let (plural, group) = split_name(&name).ok_or_else(|| {
        format!("the name {name:?} is not <plural>.<group>, the form of a custom resource definition's name")
    })?;
    let spec = document.spec;
    let references = spec
        .references
        .into_iter()
        .enumerate()
        .map(|(at, written)| {
            reference(written).map_err(|error| format!("references[{at}]: {error}"))
        })
        .collect::<Result<Vec<_>, _>>()?;
    let elevations = elevation::read(spec.elevations, &references)?;
    let referenced_by = spec
        .referenced_by
        .into_iter()
        .enumerate()
        .map(|(at, written)| {
            referrer(written).map_err(|error| format!("referencedBy[{at}]: {error}"))
        })
        .collect::<Result<_, _>>()?;
    let disruptions = match spec.disruptions.as_deref() {
        None => None,
        Some("operator") => Some(Disruptions::Operator),
        Some(other) => {
            return Err(format!(
                "disruptions is {other:?}, and transect reads only \"operator\""
            ));
        }
    };
    let gitops = gitops::read(gitops::Written {
        applies: spec.applies,
        source: spec.source,
        directory: spec.directory,
        chart: spec.chart,
        revision: spec.revision,
        repository: spec.repository,
        reference: spec.reference,
    })?;
    let ignore_changes = gitops::ignores(spec.ignore_changes)?;
    let findings = spec.findings.map(findings::read).transpose()?;
    Ok(KindKnowledge {
        doc,
        plural: plural.into(),
        group: group.into(),
        name,
        summary: spec.summary.and_then(|text| capped(&text, SUMMARY_CHARS)),
        layer: spec.layer,
        calm: spec
            .healthy
            .as_ref()
            .map(|healthy| healthy.calm.clone())
            .unwrap_or_default(),
        healthy: spec.healthy.map(|healthy| healthy.condition),
        references,
        referenced_by,
        writes_secret: spec.writes_secret,
        controller_image: spec.controller.map(|controller| controller.image),
        disruptions,
        primary: spec.primary,
        docs: spec.docs,
        gitops,
        ignore_changes,
        node: spec.node,
        findings,
        elevations,
    })
}

/// Splits a custom resource definition's name, such as
/// `caches.cache.example.io`, into its plural and its group. The API
/// server requires the group of a custom resource definition to contain
/// a dot, so a name with only one dot is a built-in kind or a typo, and
/// returns `None`.
fn split_name(name: &str) -> Option<(&str, &str)> {
    let (plural, group) = name.split_once('.')?;
    let lower = |part: &str| {
        part.chars()
            .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == '-' || c == '.')
    };
    let named = !plural.is_empty() && group.contains('.') && !group.ends_with('.');
    (named && lower(plural) && lower(group)).then_some((plural, group))
}

fn referrer(written: Referrer) -> Result<ReferencedBy, String> {
    if !written.name.contains("{name}") {
        return Err(format!(
            "name {:?} has no {{name}}, so it would match every object of the kind",
            written.name
        ));
    }
    let role = role(written.role.as_deref())?;
    Ok(ReferencedBy {
        group: written.group.unwrap_or_default(),
        kind: written.kind,
        annotation: written.annotation,
        name: written.name,
        role,
    })
}

fn role(role: Option<&str>) -> Result<Option<Role>, String> {
    match role {
        None => Ok(None),
        Some("issues-tls") => Ok(Some(Role::IssuesTls)),
        Some(other) => Err(format!(
            "role is {other:?}, and transect reads only \"issues-tls\""
        )),
    }
}

fn reference(written: Written) -> Result<Reference, String> {
    if written.matched.is_some() && written.kind.is_none() {
        return Err("match reads a field of the referenced object, so kind is required".into());
    }
    if written.across_namespaces && written.matched.is_none() {
        return Err("acrossNamespaces applies to match, so match is required".into());
    }
    let role = role(written.role.as_deref())?;
    if role.is_some() && written.kind.is_none() {
        return Err("role applies to a referenced object, so kind is required".into());
    }
    let target = match (written.kind, written.address) {
        (Some(_), true) => {
            return Err("kind and address are both set, and a reference has one target".into());
        }
        (Some(kind), false) => Target::Object {
            kind,
            group: written.group,
        },
        (None, true) => Target::Address,
        (None, false) => Target::Value,
    };
    let edge = match written.edge.as_deref() {
        None => Kind::DependsOn,
        Some(edge) => edge_kind(edge).ok_or_else(|| {
            format!(
                "edge {edge:?} is not one of runs on, stores on, holds, depends on, managed by, authenticates through, reconciles from, or part of"
            )
        })?,
    };
    Ok(Reference {
        path: written.path,
        target,
        edge,
        label: written.label,
        matched: written.matched,
        across_namespaces: written.across_namespaces,
        role,
    })
}

/// The edge `Kind` for the text of a reference's `edge`. The kinds are
/// a fixed set because loop detection, the loss cascade, and the map's
/// edge row match on them. A reference that needs another verb writes
/// it in `label`.
pub fn edge_kind(edge: &str) -> Option<Kind> {
    Some(match edge {
        "runs on" => Kind::RunsOn,
        "stores on" => Kind::StoresOn,
        "holds" => Kind::Holds,
        "depends on" => Kind::DependsOn,
        "managed by" => Kind::ControlledBy,
        "authenticates through" => Kind::AuthenticatesThrough,
        "part of" => Kind::PartOf,
        "reconciles from" => Kind::ReconcilesFrom,
        _ => return None,
    })
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn read(name: &str, spec: &str) -> Result<KindKnowledge, String> {
        let text = format!(
            "apiVersion: transect.liken.sh/v1alpha1\nkind: KindKnowledge\nmetadata: {{name: {name}}}\nspec: {spec}\n"
        );
        parse(DocId::default(), serde_yaml::from_str(&text).unwrap())
    }

    #[rstest]
    #[case::none_given("{healthy: {condition: Ready}}", vec![])]
    #[case::two("{healthy: {condition: Ready, calm: [Idle, Inventory]}}", vec!["Idle", "Inventory"])]
    fn reads_the_calm_reasons(#[case] spec: &str, #[case] expected: Vec<&str>) {
        let knowledge = read("caches.cache.example.io", spec).unwrap();
        assert_eq!(knowledge.calm, expected);
    }

    #[rstest]
    fn reads_every_field() {
        let knowledge = read(
            "caches.cache.example.io",
            "{summary: Keeps sessions., layer: platform, healthy: {condition: Ready}, references: [{path: status.primary.node, kind: Node, edge: runs on}, {path: spec.upstream, address: true, label: fills from}, {path: status.display}], writesSecret: spec.credentialsSecret, controller: {image: example/cache-operator}, disruptions: operator, primary: status.currentPrimary, docs: 'https://cache.example.io/docs/'}",
        )
        .unwrap();
        assert_eq!(
            (
                knowledge.plural.as_str(),
                knowledge.group.as_str(),
                knowledge.summary.as_deref(),
                knowledge.layer.as_deref(),
                knowledge.healthy.as_deref(),
                knowledge.writes_secret.as_deref(),
                knowledge.controller_image.as_deref(),
                knowledge.disruptions,
                knowledge.primary.as_deref(),
            ),
            (
                "caches",
                "cache.example.io",
                Some("Keeps sessions."),
                Some("platform"),
                Some("Ready"),
                Some("spec.credentialsSecret"),
                Some("example/cache-operator"),
                Some(Disruptions::Operator),
                Some("status.currentPrimary"),
            )
        );
        let references: Vec<(&str, &Target, Kind, Option<&str>)> = knowledge
            .references
            .iter()
            .map(|r| (r.path.as_str(), &r.target, r.edge, r.label.as_deref()))
            .collect();
        let node = Target::Object {
            kind: "Node".into(),
            group: None,
        };
        assert_eq!(
            references,
            vec![
                ("status.primary.node", &node, Kind::RunsOn, None),
                (
                    "spec.upstream",
                    &Target::Address,
                    Kind::DependsOn,
                    Some("fills from")
                ),
                ("status.display", &Target::Value, Kind::DependsOn, None),
            ]
        );
    }

    #[rstest]
    #[case::by_its_name("{node: metadata.name}", Some("metadata.name"))]
    #[case::unsaid("{}", None)]
    fn reads_the_field_that_names_a_node(#[case] spec: &str, #[case] node: Option<&str>) {
        let knowledge = read("machines.metal.example.io", spec).unwrap();
        assert_eq!(knowledge.node.as_deref(), node);
    }

    #[rstest]
    #[case::reconciles_from("reconciles from", Kind::ReconcilesFrom)]
    #[case::part_of("part of", Kind::PartOf)]
    fn reads_an_edge_from_the_vocabulary(#[case] edge: &str, #[case] expected: Kind) {
        let spec = format!("{{references: [{{path: spec.repository, kind: Repo, edge: {edge}}}]}}");
        let knowledge = read("caches.cache.example.io", &spec).unwrap();
        assert_eq!(knowledge.references[0].edge, expected);
    }

    #[rstest]
    fn reads_the_annotations_that_name_the_kind() {
        let knowledge = read(
            "caches.cache.example.io",
            "{referencedBy: [{kind: Ingress, group: networking.k8s.io, annotation: example.com/caches, name: '{namespace}-{name}@cache'}]}",
        )
        .unwrap();
        assert_eq!(
            knowledge.referenced_by,
            vec![ReferencedBy {
                group: "networking.k8s.io".into(),
                kind: "Ingress".into(),
                annotation: "example.com/caches".into(),
                name: "{namespace}-{name}@cache".into(),
                role: None,
            }]
        );
    }

    #[rstest]
    fn reads_what_a_referenced_object_does() {
        let knowledge = read(
            "certificates.certs.example.io",
            "{writesSecret: spec.secretName, references: [{path: spec.issuerRef.name, kind: ClusterIssuer, group: certs.example.io, role: issues-tls}]}",
        )
        .unwrap();
        assert_eq!(knowledge.references[0].role, Some(Role::IssuesTls));
    }

    #[rstest]
    fn reads_what_a_named_object_does() {
        let knowledge = read(
            "issuers.certs.example.io",
            "{referencedBy: [{kind: Ingress, group: networking.k8s.io, annotation: certs.example.io/issuer, name: '{name}', role: issues-tls}]}",
        )
        .unwrap();
        assert_eq!(knowledge.referenced_by[0].role, Some(Role::IssuesTls));
    }

    #[rstest]
    #[case::a_plural_alone("caches")]
    #[case::a_core_kind("pods.v1")]
    #[case::a_kind_not_a_plural("Cache.cache.example.io")]
    #[case::a_trailing_dot("caches.cache.example.")]
    fn refuses_a_name_that_is_not_plural_dot_group(#[case] name: &str) {
        let error = read(name, "{}").unwrap_err();
        assert!(error.contains("<plural>.<group>"), "{error}");
    }

    #[rstest]
    #[case::an_unknown_edge(
        "{references: [{path: spec.a, edge: calls}]}",
        "references[0]: edge \"calls\" is not one of"
    )]
    #[case::a_kind_and_an_address(
        "{references: [{path: spec.a, kind: Service, address: true}]}",
        "references[0]: kind and address are both set"
    )]
    #[case::a_match_with_no_kind(
        "{references: [{path: status.radio, match: status.address}]}",
        "references[0]: match reads a field of the referenced object"
    )]
    #[case::a_role_with_no_kind(
        "{references: [{path: spec.issuer, role: issues-tls}]}",
        "references[0]: role applies to a referenced object"
    )]
    #[case::an_unknown_field("{healthy: {condition: Ready}, colour: blue}", "unknown field")]
    #[case::other_disruptions("{disruptions: never}", "transect reads only \"operator\"")]
    #[case::a_name_with_no_name(
        "{referencedBy: [{kind: Ingress, annotation: example.com/caches, name: '{namespace}'}]}",
        "referencedBy[0]: name \"{namespace}\" has no {name}"
    )]
    #[case::an_unknown_role(
        "{referencedBy: [{kind: Ingress, annotation: example.com/caches, name: '{name}', role: guards}]}",
        "referencedBy[0]: role is \"guards\", and transect reads only"
    )]
    fn refuses_what_it_cannot_read(#[case] spec: &str, #[case] error: &str) {
        let found = read("caches.cache.example.io", spec).unwrap_err();
        assert!(found.contains(error), "{found}");
    }
}
