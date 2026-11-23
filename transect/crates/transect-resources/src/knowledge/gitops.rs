//! The fields of a GitOps tool's kind knowledge: which objects of the
//! kind apply other objects, how an applied object names its applier,
//! where an applier fetches from, and which revision it last applied or
//! fetched. A tool can keep its source in a separate kind or inline in
//! the applier, so any kind may declare any of these fields.

use serde::Deserialize;

/// A label or an annotation, by its key.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub enum MetaKey {
    Label(String),
    Annotation(String),
}

/// The label or annotation in which an applied object records its
/// applier's name or namespace. When the value holds more than that,
/// `formats` are templates such as `{namespace}_{name}:*`, tried in
/// order, and the first that matches gives the parts. With no formats,
/// the whole value is the part.
#[derive(Clone, Debug, PartialEq)]
pub struct Mark {
    pub key: MetaKey,
    pub formats: Vec<String>,
}

/// How an object that this kind applied names the object that applied
/// it.
#[derive(Clone, Debug, PartialEq)]
pub struct Applies {
    pub name: Mark,
    /// Where the applier's namespace is recorded. Without it, the
    /// namespace comes from a `{namespace}` in the formats of `name`.
    /// When neither gives a namespace, the applier is found only when
    /// exactly one object of the kind has that name.
    pub namespace: Option<Mark>,
    /// When an object has the marks of two appliers, the one with the
    /// higher precedence applied it. A chart's objects have the marks of
    /// its release and of whatever applied the release, so the release
    /// needs the higher precedence.
    pub precedence: i64,
    /// The `ServiceAccount` that an applier applies as, when its object
    /// can name one. Without it, an applier applies as its controller's
    /// account.
    pub account: Option<AppliesAs>,
}

/// The field of an applier that names the `ServiceAccount` it applies
/// as, which is in the applier's namespace. When the field is empty,
/// the account is `default`, and without a `default` it is the
/// controller's account.
#[derive(Clone, Debug, PartialEq)]
pub struct AppliesAs {
    pub field: String,
    pub default: Option<String>,
}

/// The revision an object last applied or fetched.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Revision {
    /// The fields that may hold the revision. The first that has a
    /// value is used.
    pub paths: Vec<String>,
    /// The field that holds the time of the apply or fetch.
    pub at: Option<String>,
    /// Templates that split a revision into its ref and its commit,
    /// tried in order, such as `{ref}@sha1:{commit}`. A revision that
    /// no template matches is all ref.
    pub formats: Vec<String>,
}

/// The fields that hold a git ref. `name` holds a ref that can be a
/// branch, a tag, or a commit, without saying which.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Refs {
    pub branch: Option<String>,
    pub tag: Option<String>,
    pub commit: Option<String>,
    pub name: Option<String>,
}

/// The GitOps fields of a kind, after validation.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct GitOpsFields {
    pub applies: Option<Applies>,
    /// The fields that may hold a reference to the source object that
    /// this one fetches from, with `apiVersion`, `kind`, `name`, and
    /// `namespace`. They are tried in order.
    pub source: Vec<String>,
    /// The field that holds the directory of the source that it
    /// applies.
    pub directory: Option<String>,
    /// The field that holds the chart that it installs.
    pub chart: Option<String>,
    pub revision: Option<Revision>,
    /// Templates of the repository URL, such as `{spec.url}`, where
    /// each placeholder is a field path. The first template whose
    /// fields all have values is used.
    pub repository: Vec<String>,
    pub reference: Refs,
}

/// A label or an annotation that a tool rewrites without changing
/// anything else, on objects of `kinds`, or of every kind when `kinds`
/// is empty. A watch ignores a change to it when it decides whether an
/// object changed.
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
    /// Whether the key is ignored on objects of `kind` in `group`.
    pub fn holds_for(&self, group: &str, kind: &str) -> bool {
        let named = |named: &GroupKind| named.group == group && named.kind == kind;
        self.kinds.is_empty() || self.kinds.iter().any(named)
    }
}

/// The key of a mark or an ignored key. Exactly one of `label` and
/// `annotation` must be set.
fn key(label: Option<String>, annotation: Option<String>) -> Result<MetaKey, String> {
    match (label, annotation) {
        (Some(label), None) => Ok(MetaKey::Label(label)),
        (None, Some(annotation)) => Ok(MetaKey::Annotation(annotation)),
        _ => Err("must set exactly one of label and annotation".into()),
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

/// The placeholders that the formats of a mark and the formats of a
/// revision may use.
const MARK_PLACEHOLDERS: [&str; 2] = ["name", "namespace"];
const REVISION_PLACEHOLDERS: [&str; 2] = ["ref", "commit"];

/// The validated fields, or none when `written` declares no field.
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
                "repository[{at}] {template:?} has no {{field}}, so it would produce the same URL for every object"
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

/// Fails when `template` has a placeholder that is not in `known`.
fn check(template: &str, known: &[&str]) -> Result<(), String> {
    let unknown = placeholders(template)
        .into_iter()
        .find(|name| !known.contains(&name.as_str()));
    match unknown {
        Some(name) => Err(format!(
            "{template:?} contains {{{name}}}, but transect reads only {}",
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
    /// Every field path that these fields read, which a trimmed watch
    /// keeps.
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
        "applies.name must set exactly one"
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
            serde_yaml_ng::from_str(&text).unwrap(),
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
    fn reports_which_kinds_an_ignored_key_applies_to(
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
