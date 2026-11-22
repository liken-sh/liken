//! Parses one `KindKnowledge` document, and runs the checks that need
//! only that document: the form of the name, and the values of `edge`,
//! `role`, and `disruptions`.

use serde::Deserialize;

use super::{
    Disruptions, Duty, Healthy, KindKnowledge, Reference, ReferencedBy, Target, Through, catalog,
    elevation, findings, gitops, role,
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
    healthy: Option<WrittenHealthy>,
    #[serde(default)]
    references: Vec<Written>,
    #[serde(default)]
    referenced_by: Vec<Referrer>,
    writes_secret: Option<String>,
    controller: Option<Controller>,
    disruptions: Option<String>,
    /// The field of the proof of concept that named the primary pod,
    /// read only to refuse it.
    primary: Option<serde_yaml::Value>,
    pattern: Option<serde_yaml::Value>,
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
    shape: Option<String>,
    roles: Option<serde_yaml::Mapping>,
    #[serde(default)]
    elevations: Vec<elevation::Written>,
}

/// `healthy`, as written: a condition of the object's own, with the
/// reasons that mean it is at rest, or a reference to follow.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct WrittenHealthy {
    condition: Option<String>,
    /// Reasons that, on a `False` condition, mean the object is at rest
    /// rather than broken, so the map draws it gray.
    #[serde(default)]
    calm: Vec<String>,
    through: Option<String>,
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
    path: Option<String>,
    /// The template of the referenced object's name, in place of a
    /// path.
    name: Option<String>,
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
    if spec.primary.is_some() {
        return Err(
            "primary is now a role of pattern: name the pattern primary-with-replicas, and fill its role primary with the reference to the primary pod"
                .into(),
        );
    }
    let references = spec
        .references
        .into_iter()
        .enumerate()
        .map(|(at, written)| {
            reference(written).map_err(|error| format!("references[{at}]: {error}"))
        })
        .collect::<Result<Vec<_>, _>>()?;
    let roles = role::read(spec.roles, &references, group)?;
    let pattern = spec
        .pattern
        .map(|written| catalog::fill(written, &references))
        .transpose()?;
    let names: Vec<&str> = roles.iter().map(|role| role.name.as_str()).collect();
    let elevations = elevation::read(spec.elevations, &names)?;
    let healthy = spec
        .healthy
        .map(|written| healthy(written, &references))
        .transpose()?;
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
        healthy,
        references,
        referenced_by,
        writes_secret: spec.writes_secret,
        controller_image: spec.controller.map(|controller| controller.image),
        disruptions,
        docs: spec.docs,
        gitops,
        ignore_changes,
        node: spec.node,
        findings,
        shape: spec.shape,
        drawn: None,
        roles,
        elevations,
        pattern,
    })
}

/// What `written` says works: its condition, or the reference to follow,
/// which must be an entry of `references` that names a kind.
fn healthy(written: WrittenHealthy, references: &[Reference]) -> Result<Healthy, String> {
    match (written.condition, written.through) {
        (Some(condition), None) => Ok(Healthy::Condition {
            condition,
            calm: written.calm,
        }),
        (None, Some(path)) if !written.calm.is_empty() => Err(format!(
            "healthy follows {path} and has calm, and the knowledge of the kind at the end of {path} states which reasons are calm"
        )),
        (None, Some(path)) => match super::references::target_of(references, &path) {
            Some(_) => Ok(Healthy::Through(Through::new(path))),
            None => Err(format!(
                "healthy follows {path}, which is the path of no entry of references that names a kind"
            )),
        },
        _ => Err("healthy has one of condition and through".into()),
    }
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

fn role(role: Option<&str>) -> Result<Option<Duty>, String> {
    match role {
        None => Ok(None),
        Some("issues-tls") => Ok(Some(Duty::IssuesTls)),
        Some(other) => Err(format!(
            "role is {other:?}, and transect reads only \"issues-tls\""
        )),
    }
}

fn reference(written: Written) -> Result<Reference, String> {
    let (path, by_name) = match (written.path, written.name) {
        (Some(_), Some(_)) => {
            return Err("the entry has both path and name, and it reads one of them".into());
        }
        (None, None) => {
            return Err("the entry has neither path nor name, so it reads nothing".into());
        }
        (Some(path), None) => (path, false),
        (None, Some(name)) => {
            template(&name, written.kind.is_some(), written.matched.is_some())?;
            (name, true)
        }
    };
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
        path,
        by_name,
        target,
        edge,
        label: written.label,
        matched: written.matched,
        across_namespaces: written.across_namespaces,
        role,
    })
}

/// Checks `name`, the template of a referenced object's name. It names
/// one object of a kind, so the entry needs `kind`, and no `match`,
/// which reads a field. It needs `{name}`, because without it every
/// object of the kind would name the same object. A brace is only part
/// of `{name}` or `{namespace}`, which `render` fills, so a role's
/// `reference` tells a template from a path, which never has a brace.
fn template(name: &str, kind: bool, matched: bool) -> Result<(), String> {
    if !kind {
        return Err(format!(
            "name {name:?} names an object, so kind is required"
        ));
    }
    if matched {
        return Err(format!(
            "name {name:?} names the object, and match reads a field, so the entry has one of them"
        ));
    }
    if !name.contains("{name}") {
        return Err(format!(
            "name {name:?} has no {{name}}, so every object of the kind would name the same object"
        ));
    }
    let rest = name.replace("{name}", "").replace("{namespace}", "");
    if rest.contains(['{', '}']) {
        return Err(format!(
            "name {name:?} has a brace that is not part of {{name}} or {{namespace}}"
        ));
    }
    Ok(())
}

/// The edge `Kind` for the text of a reference's `edge`. The kinds are
/// a fixed set because loop detection and the loss cascade match on
/// them. A reference that needs another verb writes it in `label`.
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
mod tests;
