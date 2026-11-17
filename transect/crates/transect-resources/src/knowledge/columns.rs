//! The printer columns each CRD declares, which `kubectl get` prints:
//! a name, a type, and a JSONPath into the object.  An object's page
//! shows them as facts, read the way `kubectl get` reads them, for the
//! version of the kind it holds.
//!
//! The store keeps no spec for a kind watched trimmed, so its objects'
//! column values are read when an object is trimmed, before it enters
//! the store, and kept in its data under `KEPT`.  A kind watched whole
//! is read from the whole object at render.  Both go through `raw`, so
//! the two cannot differ.

use std::collections::HashMap;
use std::sync::OnceLock;

use k8s_openapi::apiextensions_apiserver::pkg::apis::apiextensions::v1::{
    CustomResourceDefinition, CustomResourceDefinitionVersion,
};
use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;
use k8s_openapi::jiff::Timestamp;
use kube::core::DynamicObject;
use serde_json::{Map, Value};

use super::jsonpath::{self, Step};
use transect_graph::untrusted::capped;

/// The longest a column's value runs.
const VALUE_CHARS: usize = 120;

/// The field of a trimmed object's data that holds its column values,
/// by column name.  No Kubernetes field name holds a `/`, so the key
/// cannot collide with one of the object's own fields.
pub const KEPT: &str = "transect.liken.sh/columns";

/// One printer column.
#[derive(Clone, Debug, PartialEq)]
pub struct Column {
    pub name: String,
    /// `string`, `integer`, `number`, `boolean`, or `date`.
    pub kind: String,
    pub path: String,
    /// 0 for a column `kubectl get` prints; more for one it prints only
    /// with `-o wide`.
    pub priority: i32,
    /// The path's steps, parsed once, or the part of it outside the
    /// subset `transect` reads.
    pub steps: Result<Vec<Step>, String>,
}

impl Column {
    /// The columns one version of a CRD declares, in its order.
    pub fn declared(version: &CustomResourceDefinitionVersion) -> Vec<Self> {
        let columns = version.additional_printer_columns.iter().flatten();
        let columns = columns.map(|column| Column {
            name: column.name.clone(),
            kind: column.type_.clone(),
            path: column.json_path.clone(),
            priority: column.priority.unwrap_or(0),
            steps: jsonpath::parse(&column.json_path),
        });
        columns.collect()
    }
}

/// One column read from one object.
#[derive(Clone, Debug, PartialEq)]
pub enum Read {
    /// Its value, as `kubectl get` prints it; empty for a field not set.
    Value { name: String, value: String },
    /// A column whose path uses JSONPath beyond the subset read here,
    /// with the part that is.
    Unread { name: String, part: String },
}

/// One kind's columns, by version, in the CRD's order.
type Versions = Vec<(String, Vec<Column>)>;

/// Each custom kind's columns, by group and kind, then by version.
#[derive(Debug, Default)]
pub struct Columns(HashMap<(String, String), Versions>);

impl Columns {
    pub fn read<'a>(crds: impl IntoIterator<Item = &'a CustomResourceDefinition>) -> Self {
        let read = crds.into_iter().map(|crd| {
            let key = (crd.spec.group.clone(), crd.spec.names.kind.clone());
            let versions = crd.spec.versions.iter();
            let versions =
                versions.map(|version| (version.name.clone(), Column::declared(version)));
            (key, versions.collect())
        });
        Self(read.collect())
    }

    /// The columns of `group`/`kind` at `version`, or at its first
    /// version when the CRD has no such version.
    pub fn of(&self, group: &str, kind: &str, version: &str) -> &[Column] {
        let Some(versions) = self.0.get(&(group.into(), kind.into())) else {
            return &[];
        };
        let found = versions.iter().find(|(name, _)| name == version);
        let found = found.or(versions.first());
        found
            .map(|(_, columns)| columns.as_slice())
            .unwrap_or_default()
    }
}

/// The value of each of `columns` in the object of `meta` and `data`,
/// by name, as text a trimmed object keeps: a date as its timestamp,
/// which a page turns into an age.  A column whose path `transect` does
/// not read is left out.
pub fn kept(columns: &[Column], meta: &ObjectMeta, data: &Value) -> Map<String, Value> {
    let meta_json = OnceLock::new();
    let read = columns.iter().filter_map(|column| {
        let steps = column.steps.as_ref().ok()?;
        let text = raw(steps, meta, &meta_json, data);
        Some((column.name.clone(), Value::String(text)))
    });
    read.collect()
}

/// Each of `columns` read from `object`, with a date read as its age at
/// `now`, as `kubectl get` reads it: from the values the trim kept, for
/// a trimmed object, or from the object itself.
pub fn read(columns: &[Column], object: &DynamicObject, now: Timestamp) -> Vec<Read> {
    let kept = object.data.get(KEPT).and_then(Value::as_object);
    let meta_json = OnceLock::new();
    columns
        .iter()
        .map(|column| match &column.steps {
            Ok(steps) => {
                let text = match kept {
                    Some(kept) => kept.get(&column.name).and_then(Value::as_str),
                    None => None,
                };
                let text = match (kept, text) {
                    (_, Some(text)) => text.to_string(),
                    (Some(_), None) => String::new(),
                    (None, None) => raw(steps, &object.metadata, &meta_json, &object.data),
                };
                Read::Value {
                    name: column.name.clone(),
                    value: shown(column, text, now),
                }
            }
            Err(part) => Read::Unread {
                name: column.name.clone(),
                part: part.clone(),
            },
        })
        .collect()
}

/// The text `steps` reach in an object, capped: a path under `metadata`
/// reads `meta`, serialized once into `meta_json` when a path first
/// needs it, and any other reads `data`.
fn raw(steps: &[Step], meta: &ObjectMeta, meta_json: &OnceLock<Value>, data: &Value) -> String {
    let found = match steps.split_first() {
        Some((Step::Field(field), rest)) if field == "metadata" => {
            let meta = meta_json.get_or_init(|| serde_json::to_value(meta).unwrap_or_default());
            jsonpath::values(meta, rest)
        }
        _ => jsonpath::values(data, steps),
    };
    let found: Vec<String> = found.into_iter().map(jsonpath::text).collect();
    capped(&found.join(","), VALUE_CHARS).unwrap_or_default()
}

/// `text` as a page shows it: a date as its age at `now`.
fn shown(column: &Column, text: String, now: Timestamp) -> String {
    match column.kind.as_str() {
        "date" => age(&text, now).unwrap_or(text),
        _ => text,
    }
}

/// A timestamp's age at `now`, as "5d".
fn age(text: &str, now: Timestamp) -> Option<String> {
    let then: Timestamp = text.parse().ok()?;
    let minutes = now.duration_since(then).as_secs() / 60;
    Some(super::age(minutes))
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn column(name: &str, kind: &str, path: &str) -> Column {
        Column {
            name: name.into(),
            kind: kind.into(),
            path: path.into(),
            priority: 0,
            steps: jsonpath::parse(path),
        }
    }

    fn object() -> DynamicObject {
        serde_yaml::from_str(
            "apiVersion: example.io/v1\nkind: Widget\n\
             metadata: {name: a, creationTimestamp: '2026-11-06T12:00:00Z'}\n\
             spec: {hosts: [a.test], tls: {secret: s}}\n\
             status: {conditions: [{type: Ready, status: 'True', lastTransitionTime: '2026-11-09T12:00:00Z'}]}",
        )
        .unwrap()
    }

    fn now() -> Timestamp {
        "2026-11-09T13:00:00Z".parse().unwrap()
    }

    fn value(name: &str, value: &str) -> Read {
        Read::Value {
            name: name.into(),
            value: value.into(),
        }
    }

    #[rstest]
    #[case::text(
        column("Ready", "string", r#".status.conditions[?(@.type=="Ready")].status"#),
        value("Ready", "True")
    )]
    #[case::a_date(
        column("Age", "date", ".metadata.creationTimestamp"),
        value("Age", "3d")
    )]
    #[case::a_condition_date(
        column(
            "Since",
            "date",
            r#".status.conditions[?(@.type=="Ready")].lastTransitionTime"#
        ),
        value("Since", "1h")
    )]
    #[case::a_list(column("Hosts", "string", ".spec.hosts"), value("Hosts", "a.test"))]
    #[case::an_object(column("TLS", "string", ".spec.tls"), value("TLS", "{1 field}"))]
    #[case::not_set(column("Phase", "string", ".status.phase"), value("Phase", ""))]
    #[case::beyond_the_subset(column("First", "string", ".spec.hosts[0:1]"), Read::Unread { name: "First".into(), part: "[0:1]".into() })]
    fn reads_each_column_as_kubectl_does(#[case] column: Column, #[case] expected: Read) {
        assert_eq!(read(&[column], &object(), now()), vec![expected]);
    }

    #[rstest]
    #[case::a_spec_field(column("Hosts", "string", ".spec.hosts[*]"))]
    #[case::a_date(column("Age", "date", ".metadata.creationTimestamp"))]
    #[case::a_condition_date(column(
        "Since",
        "date",
        r#".status.conditions[?(@.type=="Ready")].lastTransitionTime"#
    ))]
    #[case::not_set(column("Phase", "string", ".status.phase"))]
    fn a_trimmed_object_reads_what_the_whole_one_read(#[case] column: Column) {
        let whole = object();
        let columns = [column];
        let mut trimmed = whole.clone();
        let kept = kept(&columns, &whole.metadata, &whole.data);
        trimmed.data = serde_json::json!({ KEPT: kept });
        assert_eq!(
            read(&columns, &trimmed, now()),
            read(&columns, &whole, now())
        );
    }

    #[rstest]
    fn keeps_a_date_as_its_timestamp() {
        let whole = object();
        let columns = [column("Age", "date", ".metadata.creationTimestamp")];
        let kept = kept(&columns, &whole.metadata, &whole.data);
        assert_eq!(kept["Age"], "2026-11-06T12:00:00Z");
    }

    #[rstest]
    #[case::its_version("v1", &["New"])]
    #[case::another_version("v2", &["Old"])]
    fn reads_the_columns_of_the_version_it_holds(#[case] version: &str, #[case] expected: &[&str]) {
        let crd: CustomResourceDefinition = serde_yaml::from_str(
            "metadata: {name: widgets.example.io}\n\
             spec: {group: example.io, scope: Namespaced, names: {kind: Widget, plural: widgets}, versions: [\
             {name: v1beta1, served: true, storage: false, additionalPrinterColumns: [{name: Old, type: string, jsonPath: .a}]},\
             {name: v1, served: true, storage: true, additionalPrinterColumns: [{name: New, type: string, jsonPath: .b}]}]}",
        )
        .unwrap();
        let columns = Columns::read([&crd]);
        let names: Vec<&str> = columns
            .of("example.io", "Widget", version)
            .iter()
            .map(|column| column.name.as_str())
            .collect();
        assert_eq!(names, expected);
    }
}
