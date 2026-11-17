//! What a GitOps tool's kind knowledge declares: which objects of the
//! kind apply others and how an applied object names its applier, where
//! an applier fetches from, and the revision it last applied or fetched.
//! A tool keeps its source in a kind of its own or inline on the
//! applier, so every field here may appear on any kind, and a kind
//! holds the ones that apply to it.
//!
//! The same knowledge names the metadata keys a tool rewrites on
//! objects without changing anything, which a watch ignores when it
//! decides whether an object changed.

use serde::Deserialize;

/// A label or an annotation, by its key.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub enum MetaKey {
    Label(String),
    Annotation(String),
}

/// Where an applied object names one part of its applier: a label or
/// an annotation, and, when the value holds more than that part, the
/// templates that find it, tried in order, such as `{namespace}_{name}:*`
/// before `{name}:*`.
#[derive(Clone, Debug, PartialEq)]
pub struct Mark {
    pub key: MetaKey,
    pub formats: Vec<String>,
}

/// How an object this kind applied names the object that applied it.
#[derive(Clone, Debug, PartialEq)]
pub struct Applies {
    pub name: Mark,
    /// Where the applier's namespace is written.  Without it, the
    /// applier is the one object of the kind with that name.
    pub namespace: Option<Mark>,
    /// Which applier wins when an object names two: a chart's objects
    /// have its release's marks and the marks of whatever applied the
    /// release, and the higher precedence, the more specific, wins.
    pub precedence: i64,
    /// The `ServiceAccount` an applier applies as, when its object may
    /// name one.  Without it, an applier applies as its controller's
    /// account.
    pub account: Option<AppliesAs>,
}

/// Where an applier names the `ServiceAccount` it applies as: a field,
/// read in the applier's own namespace, and the account it applies as
/// when the field is empty, or its controller's when none is given.
#[derive(Clone, Debug, PartialEq)]
pub struct AppliesAs {
    pub field: String,
    pub default: Option<String>,
}

/// The revision an object last applied or fetched.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Revision {
    /// The fields that may hold it, tried in order; the first value read
    /// wins.
    pub paths: Vec<String>,
    /// The field holding when it was applied or fetched.
    pub at: Option<String>,
    /// How a revision splits into its ref and its commit, tried in
    /// order, such as `{ref}@sha1:{commit}`.  A revision none matches is
    /// all ref.
    pub formats: Vec<String>,
}

/// The fields that name a git ref.  `name` holds a ref that may be a
/// branch, a tag, or a commit, and does not state which.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Refs {
    pub branch: Option<String>,
    pub tag: Option<String>,
    pub commit: Option<String>,
    pub name: Option<String>,
}

/// A kind's GitOps fields, validated.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct GitOpsFields {
    pub applies: Option<Applies>,
    /// The fields that may hold a reference to the object this one
    /// fetches through, `{apiVersion, kind, name, namespace}`, tried in
    /// order.
    pub source: Vec<String>,
    /// The field holding the directory in the source it applies.
    pub directory: Option<String>,
    /// The field holding the chart it installs.
    pub chart: Option<String>,
    pub revision: Option<Revision>,
    /// Templates of the repository's URL, such as `{spec.url}`, each
    /// placeholder a field; the first whose fields all read wins.
    pub repository: Vec<String>,
    pub reference: Refs,
}

/// A metadata key a tool rewrites without changing anything, for the
/// kinds it names, or for every kind when it names none.
#[derive(Clone, Debug, PartialEq)]
pub struct IgnoreChange {
    pub key: MetaKey,
    pub kinds: Vec<GroupKind>,
}

/// A kind by its group, empty for the core group, and its name.
#[derive(Clone, Debug, PartialEq, Eq, Hash, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct GroupKind {
    #[serde(default)]
    pub group: String,
    pub kind: String,
}

impl GroupKind {
    #[cfg(any(test, feature = "fixtures"))]
    pub fn new(group: &str, kind: &str) -> Self {
        Self {
            group: group.into(),
            kind: kind.into(),
        }
    }
}

impl IgnoreChange {
    /// Whether it holds for objects of `kind` in `group`.
    pub fn holds_for(&self, group: &str, kind: &str) -> bool {
        let named = |named: &GroupKind| named.group == group && named.kind == kind;
        self.kinds.is_empty() || self.kinds.iter().any(named)
    }
}

/// The key a label or an annotation names: exactly one of the two.
fn key(label: Option<String>, annotation: Option<String>) -> Result<MetaKey, String> {
    match (label, annotation) {
        (Some(label), None) => Ok(MetaKey::Label(label)),
        (None, Some(annotation)) => Ok(MetaKey::Annotation(annotation)),
        _ => Err("names a label or an annotation, and exactly one".into()),
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct WrittenMark {
    label: Option<String>,
    annotation: Option<String>,
    #[serde(default)]
    formats: Vec<String>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct WrittenApplies {
    name: WrittenMark,
    namespace: Option<WrittenMark>,
    #[serde(default)]
    precedence: i64,
    account: Option<WrittenAccount>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct WrittenAccount {
    field: String,
    default: Option<String>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct WrittenRevision {
    paths: Vec<String>,
    at: Option<String>,
    #[serde(default)]
    formats: Vec<String>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct WrittenRefs {
    branch: Option<String>,
    tag: Option<String>,
    commit: Option<String>,
    name: Option<String>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct WrittenIgnore {
    label: Option<String>,
    annotation: Option<String>,
    #[serde(default)]
    kinds: Vec<GroupKind>,
}

/// The GitOps fields of a kind knowledge's spec, as written.
#[derive(Default)]
pub(super) struct Written {
    pub applies: Option<WrittenApplies>,
    pub source: Vec<String>,
    pub directory: Option<String>,
    pub chart: Option<String>,
    pub revision: Option<WrittenRevision>,
    pub repository: Vec<String>,
    pub reference: Option<WrittenRefs>,
}

/// The placeholders an applier's mark may hold, and those a revision's
/// format may hold.
const MARK_PLACEHOLDERS: [&str; 2] = ["name", "namespace"];
const REVISION_PLACEHOLDERS: [&str; 2] = ["ref", "commit"];

/// The fields `written` declares, or none when it declares none.
pub(super) fn read(written: Written) -> Result<Option<GitOpsFields>, String> {
    let applies = written.applies.map(applies).transpose()?;
    let revision = match written.revision {
        None => None,
        Some(revision) => {
            for (at, format) in revision.formats.iter().enumerate() {
                check(format, &REVISION_PLACEHOLDERS)
                    .map_err(|error| format!("revision.formats[{at}]: {error}"))?;
            }
            Some(Revision {
                paths: revision.paths,
                at: revision.at,
                formats: revision.formats,
            })
        }
    };
    for (at, template) in written.repository.iter().enumerate() {
        if placeholders(template).is_empty() {
            return Err(format!(
                "repository[{at}] {template:?} has no {{field}}, so it would name one URL for every object"
            ));
        }
    }
    let reference = written.reference.map(|refs| Refs {
        branch: refs.branch,
        tag: refs.tag,
        commit: refs.commit,
        name: refs.name,
    });
    let fields = GitOpsFields {
        applies,
        source: written.source,
        directory: written.directory,
        chart: written.chart,
        revision,
        repository: written.repository,
        reference: reference.unwrap_or_default(),
    };
    Ok((fields != GitOpsFields::default()).then_some(fields))
}

fn applies(written: WrittenApplies) -> Result<Applies, String> {
    let name = mark(written.name).map_err(|error| format!("applies.name {error}"))?;
    let namespace = written.namespace.map(mark).transpose();
    let namespace = namespace.map_err(|error| format!("applies.namespace {error}"))?;
    Ok(Applies {
        name,
        namespace,
        precedence: written.precedence,
        account: written.account.map(|account| AppliesAs {
            field: account.field,
            default: account.default,
        }),
    })
}

fn mark(written: WrittenMark) -> Result<Mark, String> {
    let key = key(written.label, written.annotation)?;
    for (at, format) in written.formats.iter().enumerate() {
        check(format, &MARK_PLACEHOLDERS).map_err(|error| format!("formats[{at}]: {error}"))?;
    }
    Ok(Mark {
        key,
        formats: written.formats,
    })
}

/// The ignored keys as written, each validated.
pub(super) fn ignores(written: Vec<WrittenIgnore>) -> Result<Vec<IgnoreChange>, String> {
    let read = written.into_iter().enumerate().map(|(at, written)| {
        let key = key(written.label, written.annotation);
        let key = key.map_err(|error| format!("ignoreChanges[{at}] {error}"))?;
        Ok(IgnoreChange {
            key,
            kinds: written.kinds,
        })
    });
    read.collect()
}

/// Whether `template`'s placeholders are all among `known`.
fn check(template: &str, known: &[&str]) -> Result<(), String> {
    let unknown = placeholders(template)
        .into_iter()
        .find(|name| !known.contains(&name.as_str()));
    match unknown {
        Some(name) => Err(format!(
            "{template:?} contains {{{name}}}, and transect reads only {}",
            known
                .iter()
                .map(|name| format!("{{{name}}}"))
                .collect::<Vec<_>>()
                .join(" and ")
        )),
        None => Ok(()),
    }
}

impl GitOpsFields {
    /// Every field path these fields read, which a trimmed watch keeps.
    pub fn paths(&self) -> Vec<String> {
        let mut paths: Vec<String> = self.source.clone();
        paths.extend(self.directory.iter().chain(&self.chart).cloned());
        let account = self.applies.as_ref().and_then(|a| a.account.as_ref());
        paths.extend(account.map(|account| account.field.clone()));
        if let Some(revision) = &self.revision {
            paths.extend(revision.paths.iter().chain(&revision.at).cloned());
        }
        paths.extend(self.repository.iter().flat_map(|url| placeholders(url)));
        let refs = &self.reference;
        let refs = [&refs.branch, &refs.tag, &refs.commit, &refs.name];
        paths.extend(refs.into_iter().flatten().cloned());
        paths
    }
}

/// The names of `template`'s `{placeholders}`, in order.
pub fn placeholders(template: &str) -> Vec<String> {
    let mut found = Vec::new();
    let mut rest = template;
    while let Some((_, after)) = rest.split_once('{') {
        let Some((name, after)) = after.split_once('}') else {
            break;
        };
        found.push(name.to_string());
        rest = after;
    }
    found
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::fixtures::knowledge;
    use super::*;

    fn fields(spec: &str) -> GitOpsFields {
        knowledge("trees.apply.example.test", spec).gitops.unwrap()
    }

    #[rstest]
    fn reads_an_appliers_fields() {
        let found = fields(
            "{applies: {name: {label: apply.example.test/name}, namespace: {label: apply.example.test/namespace}, precedence: 1, account: {field: spec.serviceAccountName}}, source: [spec.sourceRef], directory: spec.path, revision: {paths: [status.lastAppliedRevision], formats: ['{ref}@sha1:{commit}']}}",
        );
        let label = |key: &str| Mark {
            key: MetaKey::Label(key.into()),
            formats: vec![],
        };
        assert_eq!(
            found,
            GitOpsFields {
                applies: Some(Applies {
                    name: label("apply.example.test/name"),
                    namespace: Some(label("apply.example.test/namespace")),
                    precedence: 1,
                    account: Some(AppliesAs {
                        field: "spec.serviceAccountName".into(),
                        default: None,
                    }),
                }),
                source: vec!["spec.sourceRef".into()],
                directory: Some("spec.path".into()),
                revision: Some(Revision {
                    paths: vec!["status.lastAppliedRevision".into()],
                    at: None,
                    formats: vec!["{ref}@sha1:{commit}".into()],
                }),
                ..GitOpsFields::default()
            }
        );
    }

    #[rstest]
    #[case::none("{summary: Applies.}", false)]
    #[case::a_directory("{directory: spec.path}", true)]
    fn reads_no_fields_when_none_are_declared(#[case] spec: &str, #[case] declared: bool) {
        let found = knowledge("trees.apply.example.test", spec).gitops;
        assert_eq!(found.is_some(), declared);
    }

    #[rstest]
    #[case::both_keys(
        "{applies: {name: {label: a, annotation: b}}}",
        "applies.name names a label or an annotation, and exactly one"
    )]
    #[case::an_unknown_mark_placeholder(
        "{applies: {name: {annotation: a, formats: ['{name}:*', '{app}:*']}}}",
        "applies.name formats[1]: \"{app}:*\" contains {app}"
    )]
    #[case::an_unknown_revision_placeholder(
        "{revision: {paths: [status.revision], formats: ['{branch}@{commit}']}}",
        "revision.formats[0]: \"{branch}@{commit}\" contains {branch}"
    )]
    #[case::a_repository_with_no_field("{repository: [https://example.test]}", "repository[0]")]
    #[case::an_ignored_key_with_neither(
        "{ignoreChanges: [{kinds: [{kind: Namespace}]}]}",
        "ignoreChanges[0]"
    )]
    fn refuses_what_it_cannot_read(#[case] spec: &str, #[case] error: &str) {
        let text = format!("metadata: {{name: trees.apply.example.test}}\nspec: {spec}\n");
        let found = super::super::parse(
            crate::resources::DocId::default(),
            serde_yaml::from_str(&text).unwrap(),
        )
        .unwrap_err();
        assert!(found.contains(error), "{found}");
    }

    #[rstest]
    fn reads_the_keys_a_tool_rewrites() {
        let found = knowledge(
            "trees.apply.example.test",
            "{ignoreChanges: [{label: tree/name, kinds: [{kind: Namespace}]}, {annotation: tree/checksum}]}",
        );
        assert_eq!(
            found.ignore_changes,
            vec![
                IgnoreChange {
                    key: MetaKey::Label("tree/name".into()),
                    kinds: vec![GroupKind::new("", "Namespace")],
                },
                IgnoreChange {
                    key: MetaKey::Annotation("tree/checksum".into()),
                    kinds: vec![],
                },
            ]
        );
    }

    #[rstest]
    #[case::for_its_kind(&[("", "Namespace")], ("", "Namespace"), true)]
    #[case::not_for_another(&[("", "Namespace")], ("", "ConfigMap"), false)]
    #[case::not_for_its_name_in_another_group(&[("", "Namespace")], ("example.test", "Namespace"), false)]
    #[case::for_every_kind(&[], ("", "ConfigMap"), true)]
    fn says_which_kinds_an_ignored_key_holds_for(
        #[case] kinds: &[(&str, &str)],
        #[case] kind: (&str, &str),
        #[case] holds: bool,
    ) {
        let ignore = IgnoreChange {
            key: MetaKey::Label("tree/name".into()),
            kinds: kinds.iter().map(|(g, k)| GroupKind::new(g, k)).collect(),
        };
        assert_eq!(ignore.holds_for(kind.0, kind.1), holds);
    }

    #[rstest]
    #[case::none("plain", vec![])]
    #[case::two("{spec.endpoint}/{spec.bucket}", vec!["spec.endpoint", "spec.bucket"])]
    #[case::unclosed("{spec.url", vec![])]
    fn finds_a_templates_placeholders(#[case] template: &str, #[case] expected: Vec<&str>) {
        assert_eq!(placeholders(template), expected);
    }
}
