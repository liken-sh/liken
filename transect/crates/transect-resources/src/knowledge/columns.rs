//! The printer columns that CRDs declare for `kubectl get`. An object's
//! page shows the columns of its version, with the values that
//! `kubectl get` would print.
//!
//! For a custom kind that `transect` does not read whole, the trim in
//! `transect-cluster` deletes every field except the metadata, the
//! conditions, and a few named fields before the object enters the
//! store. So the trim calls `kept` first and saves the column values
//! under `KEPT`. An object that the store keeps whole is read at render
//! time. Both paths call `path_text`, so they give the same text.

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

/// A column value longer than this many characters is cut.
const VALUE_CHARS: usize = 120;

/// The key in a trimmed object's data for its column values, by column
/// name. The key contains a `/`, which no top-level field of a
/// Kubernetes object has, so it cannot collide with one.
pub const KEPT: &str = "transect.liken.sh/columns";

/// One entry of a CRD version's `additionalPrinterColumns`.
#[derive(Clone, Debug, PartialEq)]
pub struct Column {
    pub name: String,
    /// `string`, `integer`, `number`, `boolean`, or `date`.
    pub kind: String,
    pub path: String,
    /// 0 for a column `kubectl get` always prints, higher for one that
    /// it prints only with `-o wide`.
    pub priority: i32,
    /// The parsed steps of the path, or the part of the path that uses
    /// JSONPath beyond the subset `transect` reads.
    pub steps: Result<Vec<Step>, String>,
}

impl Column {
    /// The columns that one CRD version declares, in declaration order.
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

/// The result of reading one column from one object.
#[derive(Clone, Debug, PartialEq)]
pub enum Read {
    /// The value as `kubectl get` prints it, empty when the field is
    /// not set.
    Value { name: String, value: String },
    /// A column whose path uses JSONPath beyond the subset `transect`
    /// reads. `part` is the part of the path that it cannot read.
    Unread { name: String, part: String },
}

/// One kind's columns for each version, in the CRD's order.
type Versions = Vec<(String, Vec<Column>)>;

/// The columns of every custom kind, keyed by group and kind.
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

    /// The columns of `group`/`kind` at `version`. When the CRD has no
    /// such version, the columns of its first version.
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

/// The text of each of `columns` for the object with `meta` and `data`,
/// by column name. A date stays a timestamp, which the page turns into
/// an age when it renders. A column whose path `transect` cannot read is
/// left out.
pub fn kept(columns: &[Column], meta: &ObjectMeta, data: &Value) -> Map<String, Value> {
    let meta_json = OnceLock::new();
    let read = columns.iter().filter_map(|column| {
        let steps = column.steps.as_ref().ok()?;
        let text = path_text(steps, meta, &meta_json, data);
        Some((column.name.clone(), Value::String(text)))
    });
    read.collect()
}

/// Reads each of `columns` from `object`, with a date shown as its age
/// at `now`. When the object has values under `KEPT`, it reads only
/// those, and a column missing from them is empty.
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
                    (None, None) => path_text(steps, &object.metadata, &meta_json, &object.data),
                };
                Read::Value {
                    name: column.name.clone(),
                    value: display(column, text, now),
                }
            }
            Err(part) => Read::Unread {
                name: column.name.clone(),
                part: part.clone(),
            },
        })
        .collect()
}

/// The values at `steps` in an object, joined with commas and capped.
/// `DynamicObject` keeps the metadata as a typed field outside `data`,
/// so a path under `metadata` reads `meta`, serialized into `meta_json`
/// on first use.
fn path_text(
    steps: &[Step],
    meta: &ObjectMeta,
    meta_json: &OnceLock<Value>,
    data: &Value,
) -> String {
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

/// A date becomes its age at `now`. Other text, and a date that does
/// not parse, is returned as it is.
fn display(column: &Column, text: String, now: Timestamp) -> String {
    match column.kind.as_str() {
        "date" => age(&text, now).unwrap_or(text),
        _ => text,
    }
}

/// The timestamp in `text` as an age at `now`, such as "5d".
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
    #[case::beyond_the_supported_jsonpath(column("First", "string", ".spec.hosts[0:1]"), Read::Unread { name: "First".into(), part: "[0:1]".into() })]
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
    fn reads_the_same_from_a_trimmed_object_as_from_the_whole_one(#[case] column: Column) {
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
    fn reads_the_columns_of_the_requested_version(
        #[case] version: &str,
        #[case] expected: &[&str],
    ) {
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
