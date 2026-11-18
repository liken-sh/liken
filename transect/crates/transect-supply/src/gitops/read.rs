//! Reads one object of a GitOps kind, using its kind's knowledge to find
//! the fields that transect shows: whether it is ready, the revision it
//! last applied or fetched and when, the source it fetches through, the
//! URL it fetches from, and the directory or chart it applies.
//!
//! A field that the knowledge names but the object lacks reads as
//! absent. So when another release of the GitOps tool moves a field, the
//! page shows less and does not fail.

use k8s_openapi::jiff::Timestamp;
use kube::core::DynamicObject;
use serde_json::Value;

use transect_graph::edges::Object;
use transect_resources::knowledge::gitops::placeholders;
use transect_resources::knowledge::{GitOpsFields, path};

/// The readiness that an object reports in the condition that its
/// kind's knowledge names, or in `Ready` when the knowledge names none.
#[derive(Clone, Debug, PartialEq)]
pub enum Ready {
    True,
    /// False, with the tool's message, such as a build error.
    False(String),
    /// Unknown, such as during a reconciliation, with the tool's
    /// message.
    Unknown(String),
    /// The object has no such condition yet, for example before its
    /// first reconciliation.
    Missing,
}

/// The ref that a source follows, read from the fields that its
/// knowledge's `ref` names.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct GitRef {
    pub branch: Option<String>,
    pub tag: Option<String>,
    pub commit: Option<String>,
    /// A ref that may be any of the three.
    pub name: Option<String>,
}

/// The fields that transect shows for one GitOps object.
#[derive(Clone, Debug, PartialEq)]
pub struct Found {
    pub object: Object,
    pub ready: Ready,
    /// When its condition last changed.
    pub ready_since: Option<Timestamp>,
    pub revision: Option<String>,
    /// When the object applied or fetched `revision`, if its status
    /// records the time.
    pub revision_at: Option<Timestamp>,
    /// The source that the object fetches through, from the field that
    /// its knowledge names.  This is the reference as written, before
    /// transect checks that it can read the source's kind.
    pub source: Option<SourceRef>,
    /// The repository URL: the first of the knowledge's `repository`
    /// templates whose fields the object has.
    pub url: Option<String>,
    pub reference: GitRef,
    pub directory: Option<String>,
    pub chart: Option<String>,
    /// The `ServiceAccount` in the object's own namespace that the
    /// object applies as.  It is the account that the `applies.account`
    /// field names, or the knowledge's default when the field is empty.
    /// `None` means that the object applies as its controller's account.
    pub account: Option<String>,
}

/// A reference to a source as an object writes it: a kind and a name,
/// with a group and a namespace when it gives them.
#[derive(Clone, Debug, PartialEq)]
pub struct SourceRef {
    pub group: Option<String>,
    pub kind: String,
    pub name: String,
    /// Its namespace, or the referring object's when it gives none.
    pub namespace: Option<String>,
}

/// Reads the fields that `fields` names from `found`, the API object of
/// `object`.  `healthy` is the condition type that gives its readiness.
pub fn read(object: Object, found: &DynamicObject, fields: &GitOpsFields, healthy: &str) -> Found {
    let data = &found.data;
    let first = |path: &str| path::read(data, path).into_iter().next();
    let revision = fields.revision.as_ref();
    let condition = condition(data, healthy);
    let source = fields
        .source
        .iter()
        .find_map(|path| source_ref(data, path, object.namespace.as_deref()));
    let refs = &fields.reference;
    let read_ref = |path: &Option<String>| path.as_deref().and_then(first);
    Found {
        ready: ready(condition),
        ready_since: condition
            .and_then(|condition| condition.get("lastTransitionTime"))
            .and_then(Value::as_str)
            .and_then(|at| at.parse().ok()),
        revision: revision.and_then(|r| r.paths.iter().find_map(|path| first(path))),
        revision_at: revision
            .and_then(|r| r.at.as_deref())
            .and_then(first)
            .and_then(|at| at.parse().ok()),
        source,
        url: fields
            .repository
            .iter()
            .find_map(|template| fill(data, template)),
        reference: GitRef {
            branch: read_ref(&refs.branch),
            tag: read_ref(&refs.tag),
            commit: read_ref(&refs.commit),
            name: read_ref(&refs.name),
        },
        directory: fields.directory.as_deref().and_then(first),
        chart: fields.chart.as_deref().and_then(first),
        account: fields
            .applies
            .as_ref()
            .and_then(|applies| applies.account.as_ref())
            .and_then(|account| {
                let named = first(&account.field).filter(|name| !name.is_empty());
                named.or_else(|| account.default.clone())
            }),
        object,
    }
}

fn condition<'a>(data: &'a Value, kind: &str) -> Option<&'a Value> {
    let conditions = data.get("status")?.get("conditions")?.as_array()?;
    conditions
        .iter()
        .find(|condition| condition.get("type").and_then(Value::as_str) == Some(kind))
}

fn ready(condition: Option<&Value>) -> Ready {
    let Some(condition) = condition else {
        return Ready::Missing;
    };
    let text = |key: &str| condition.get(key).and_then(Value::as_str);
    let message = text("message").unwrap_or_default().to_string();
    match text("status") {
        Some("True") => Ready::True,
        Some("False") => Ready::False(message),
        _ => Ready::Unknown(message),
    }
}

/// The source reference at `path`, or `None` when it has no kind or no
/// name.
fn source_ref(data: &Value, path: &str, namespace: Option<&str>) -> Option<SourceRef> {
    let field = |name: &str| {
        path::read(data, &format!("{path}.{name}"))
            .into_iter()
            .next()
    };
    let group = field("apiVersion").map(|version| match version.split_once('/') {
        Some((group, _)) => group.to_string(),
        None => String::new(),
    });
    Some(SourceRef {
        group,
        kind: field("kind")?,
        name: field("name")?,
        namespace: field("namespace").or(namespace.map(Into::into)),
    })
}

/// `template` with each `{field}` replaced by the value of that field
/// in `data`, or `None` when one of the fields has no value.
fn fill(data: &Value, template: &str) -> Option<String> {
    let mut filled = template.to_string();
    for name in placeholders(template) {
        let value = path::read(data, &name).into_iter().next()?;
        filled = filled.replacen(&format!("{{{name}}}"), &value, 1);
    }
    Some(filled)
}

#[cfg(test)]
pub mod fixtures {
    use super::*;

    /// An object of `group` and `kind` in `namespace`, with `body` as
    /// the YAML of its spec and status.
    pub fn dynamic(
        group: &str,
        kind: &str,
        namespace: &str,
        name: &str,
        body: &str,
    ) -> DynamicObject {
        serde_yaml::from_str(&format!(
            "apiVersion: {group}/v1\nkind: {kind}\nmetadata: {{namespace: {namespace}, name: {name}}}\n{body}"
        ))
        .unwrap()
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::fixtures::dynamic;
    use super::*;
    use transect_resources::knowledge::fixtures::knowledge;

    const GROUP: &str = "apply.example.test";

    fn read_with(spec: &str, body: &str) -> Found {
        let fields = knowledge("appliers.apply.example.test", spec)
            .gitops
            .unwrap();
        let found = dynamic(GROUP, "Applier", "shop", "shop", body);
        let object = Object::new(GROUP, "Applier", Some("shop"), "shop");
        read(object, &found, &fields, "Ready")
    }

    fn source(group: Option<&str>, namespace: &str) -> Option<SourceRef> {
        Some(SourceRef {
            group: group.map(Into::into),
            kind: "Repo".into(),
            name: "fleet".into(),
            namespace: Some(namespace.into()),
        })
    }

    #[rstest]
    #[case::in_its_own_namespace(
        "spec: {sourceRef: {kind: Repo, name: fleet}}",
        source(None, "shop")
    )]
    #[case::in_another(
        "spec: {sourceRef: {kind: Repo, name: fleet, namespace: ops}}",
        source(None, "ops")
    )]
    #[case::with_a_group(
        "spec: {sourceRef: {apiVersion: fetch.example.test/v1, kind: Repo, name: fleet}}",
        source(Some("fetch.example.test"), "shop")
    )]
    #[case::the_second_path("spec: {chartRef: {kind: Repo, name: fleet}}", source(None, "shop"))]
    #[case::no_name("spec: {sourceRef: {kind: Repo}}", None)]
    #[case::none("", None)]
    fn reads_the_first_reference_to_a_source(
        #[case] body: &str,
        #[case] expected: Option<SourceRef>,
    ) {
        let found = read_with("{source: [spec.sourceRef, spec.chartRef]}", body);
        assert_eq!(found.source, expected);
    }

    #[rstest]
    #[case::named(
        "{applies: {name: {label: a}, account: {field: spec.serviceAccountName}}}",
        "spec: {serviceAccountName: deployer}",
        Some("deployer")
    )]
    #[case::empty(
        "{applies: {name: {label: a}, account: {field: spec.serviceAccountName}}}",
        "spec: {serviceAccountName: ''}",
        None
    )]
    #[case::the_default(
        "{applies: {name: {label: a}, account: {field: spec.serviceAccountName, default: applier}}}",
        "spec: {}",
        Some("applier")
    )]
    #[case::no_field(
        "{applies: {name: {label: a}}}",
        "spec: {serviceAccountName: deployer}",
        None
    )]
    fn reads_the_account_it_applies_as(
        #[case] spec: &str,
        #[case] body: &str,
        #[case] expected: Option<&str>,
    ) {
        let found = read_with(spec, body);
        assert_eq!(found.account.as_deref(), expected);
    }

    #[rstest]
    #[case::ready(
        "status: {conditions: [{type: Ready, status: 'True', message: Applied}]}",
        Ready::True
    )]
    #[case::not_ready(
        "status: {conditions: [{type: Ready, status: 'False', message: build failed}]}",
        Ready::False("build failed".into())
    )]
    #[case::reconciling(
        "status: {conditions: [{type: Ready, status: Unknown, message: Reconciling}]}",
        Ready::Unknown("Reconciling".into())
    )]
    #[case::another_condition_only(
        "status: {conditions: [{type: Healthy, status: 'True'}]}",
        Ready::Missing
    )]
    #[case::no_status("", Ready::Missing)]
    fn reads_its_condition(#[case] body: &str, #[case] expected: Ready) {
        let found = read_with("{directory: spec.path}", body);
        assert_eq!(found.ready, expected);
    }

    #[rstest]
    #[case::the_first_path(
        "status: {lastAppliedRevision: main@sha1:0a1b2c3, history: [{chartVersion: 1.4.0}]}",
        Some("main@sha1:0a1b2c3")
    )]
    #[case::the_newest_of_a_list(
        "status: {history: [{chartVersion: 1.4.0}, {chartVersion: 1.3.2}]}",
        Some("1.4.0")
    )]
    #[case::none("", None)]
    fn reads_the_first_revision_its_paths_hold(#[case] body: &str, #[case] expected: Option<&str>) {
        let found = read_with(
            "{revision: {paths: [status.lastAppliedRevision, 'status.history[].chartVersion']}}",
            body,
        );
        assert_eq!(found.revision.as_deref(), expected);
    }

    #[rstest]
    #[case::a_url(
        "spec: {url: 'https://forge.example.test/acme/fleet'}",
        Some("https://forge.example.test/acme/fleet")
    )]
    #[case::a_bucket(
        "spec: {endpoint: storage.example.test, bucketName: fleet}",
        Some("storage.example.test/fleet")
    )]
    #[case::neither("spec: {endpoint: storage.example.test}", None)]
    fn fills_the_first_url_whose_fields_read(#[case] body: &str, #[case] expected: Option<&str>) {
        let found = read_with(
            "{repository: ['{spec.url}', '{spec.endpoint}/{spec.bucketName}']}",
            body,
        );
        assert_eq!(found.url.as_deref(), expected);
    }

    #[rstest]
    fn lists_every_path_a_source_reads() {
        let fields = knowledge(
            "repos.apply.example.test",
            "{repository: ['{spec.endpoint}/{spec.bucket}'], ref: {branch: spec.ref.branch, commit: spec.ref.commit}, revision: {paths: [status.artifact.revision], at: status.artifact.lastUpdateTime}}",
        );
        assert_eq!(
            fields.gitops.unwrap().paths(),
            vec![
                "status.artifact.revision",
                "status.artifact.lastUpdateTime",
                "spec.endpoint",
                "spec.bucket",
                "spec.ref.branch",
                "spec.ref.commit",
            ]
        );
    }

    #[rstest]
    fn reads_the_ref_and_when_it_fetched() {
        let found = read_with(
            "{ref: {branch: spec.ref.branch, commit: spec.ref.commit}, revision: {paths: [status.artifact.revision], at: status.artifact.lastUpdateTime}}",
            "spec: {ref: {branch: main, commit: '0a1b2c3'}}\nstatus: {artifact: {revision: main@sha1:0a1b2c3, lastUpdateTime: '2026-01-01T10:00:00Z'}}",
        );
        assert_eq!(
            (found.reference, found.revision_at.map(|at| at.to_string())),
            (
                GitRef {
                    branch: Some("main".into()),
                    commit: Some("0a1b2c3".into()),
                    ..GitRef::default()
                },
                Some("2026-01-01T10:00:00Z".into())
            )
        );
    }
}
