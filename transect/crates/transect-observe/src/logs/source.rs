//! The `LogSource` resource declares a log store: its address, its
//! query language, the store's name for each field that transect reads,
//! and the queries that transect fills in.
//!
//! A cluster declares at most one.  transect uses `url` before
//! `service`, so one resource works from a workstation through a
//! port-forward and from inside the cluster by the `Service`'s DNS
//! name.  `--log-source-url` overrides both.  transect sends no
//! credentials, because a port-forward needs none.

use serde::Deserialize;

use super::levels::Levels;
use super::query::{self, Language, Query};
use transect_graph::upstream::{self, Service, base_url};
use transect_resources::resources::DocId;

/// One `LogSource` resource, validated.
#[derive(Clone, Debug, PartialEq)]
pub struct LogSource {
    pub doc: DocId,
    pub name: String,
    pub service: Option<Service>,
    /// The base URL, with no trailing slash.
    pub url: Option<String>,
    pub language: Language,
    /// The path of the query endpoint, such as `/select/logsql/query`.
    pub path: String,
    pub fields: Fields,
    /// The level that each value of the level field means.  It is empty
    /// when the store has no level field.
    pub levels: Levels,
    pub tail: String,
    pub search: String,
    /// The query for the newest lines that match a search's filter
    /// across the cluster, which a finding shows as samples.  It is
    /// `None` when the source does not declare it.
    pub lines: Option<String>,
}

/// The store's name for each field that transect reads.  The node and
/// the level are optional.  For a store without a level field, transect
/// guesses each line's level from its text.  A store with a level field
/// declares in `levels` what the field's values mean.
#[derive(Clone, Debug, PartialEq, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Fields {
    pub namespace: String,
    pub pod: String,
    pub container: String,
    pub node: Option<String>,
    pub time: String,
    pub level: Option<String>,
    pub message: String,
    /// The rule that selects the lines that are a machine's own log,
    /// when a pod ships them.
    pub machine: Option<MachineLog>,
}

/// The lines that are a machine's own log, such as its kubelet's or its
/// kernel's, even though a pod ships them.  These are the lines whose
/// `field` holds one of `values`, such as the name of the `DaemonSet`
/// that reads each machine's journal.  The pod only ships these lines,
/// so transect assigns them to the machine that their node field names.
/// `unit` is the field that names which of the machine's logs a line
/// belongs to, and the container field is used when `unit` is left out.
/// A line with a node and no namespace is also a machine's own.
#[derive(Clone, Debug, PartialEq, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct MachineLog {
    pub field: String,
    pub values: Vec<String>,
    pub unit: Option<String>,
}

impl Fields {
    /// The field that names which of a machine's logs a line belongs to.
    pub fn unit(&self) -> &str {
        let unit = self
            .machine
            .as_ref()
            .and_then(|machine| machine.unit.as_ref());
        unit.unwrap_or(&self.container)
    }
}

impl Fields {
    /// The store's name for the field that `{fields.<name>}` stands for.
    pub fn named(&self, name: &str) -> Option<&str> {
        match name {
            "namespace" => Some(&self.namespace),
            "pod" => Some(&self.pod),
            "container" => Some(&self.container),
            "node" => self.node.as_deref(),
            "time" => Some(&self.time),
            "level" => self.level.as_deref(),
            "message" => Some(&self.message),
            _ => None,
        }
    }
}

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
#[serde(deny_unknown_fields)]
struct Spec {
    service: Option<Service>,
    url: Option<String>,
    language: String,
    query: String,
    fields: Fields,
    levels: Option<Levels>,
    queries: Queries,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Queries {
    tail: String,
    search: String,
    lines: Option<String>,
}

/// The `LogSource` that `value` declares, or the reason it does not
/// validate.
pub fn parse(doc: DocId, value: serde_yaml::Value) -> Result<LogSource, String> {
    let document: Document = serde_yaml::from_value(value).map_err(|error| error.to_string())?;
    let spec = document.spec;
    let language = Language::named(&spec.language)?;
    let url = spec.url.as_deref().map(base_url).transpose()?;
    if url.is_none() && spec.service.is_none() {
        return Err("a LogSource needs a url or a service".into());
    }
    if !spec.query.starts_with('/') {
        return Err(format!(
            "query is the path of the store's query endpoint, starting with /, not {:?}",
            spec.query
        ));
    }
    if let Some(machine) = &spec.fields.machine {
        if spec.fields.node.is_none() {
            return Err("fields.machine needs fields.node, the machine a line belongs to".into());
        }
        if machine.values.is_empty() {
            return Err("fields.machine.values names no value".into());
        }
    }
    let levels = match (&spec.fields.level, spec.levels) {
        (Some(_), Some(levels)) => levels.checked()?,
        (None, None) => Levels::default(),
        (Some(_), None) => {
            return Err("fields.level needs levels, which define what its values mean".into());
        }
        (None, Some(_)) => {
            return Err("levels needs fields.level, the field whose values they name".into());
        }
    };
    query::check(Query::Tail, &spec.queries.tail, &spec.fields)?;
    query::check(Query::Search, &spec.queries.search, &spec.fields)?;
    if let Some(lines) = &spec.queries.lines {
        query::check(Query::Lines, lines, &spec.fields)?;
    }
    Ok(LogSource {
        doc,
        name: document.metadata.name,
        service: spec.service,
        url,
        language,
        path: spec.query,
        fields: spec.fields,
        levels,
        tail: spec.queries.tail,
        search: spec.queries.search,
        lines: spec.queries.lines,
    })
}

/// A log source with the endpoint that transect sends its queries to
/// from the place where transect runs.
#[derive(Clone, Debug, PartialEq)]
pub struct Reached {
    pub source: LogSource,
    pub endpoint: String,
}

impl LogSource {
    /// The query endpoint.  It uses the flag's URL first, then the
    /// resource's URL, then the `Service`'s DNS name in `domain`.
    pub fn reached(&self, flag: Option<&str>, domain: &str) -> Reached {
        let (url, service) = (self.url.as_deref(), self.service.as_ref());
        let base = upstream::base(flag, url, service, domain).unwrap_or_default();
        Reached {
            source: self.clone(),
            endpoint: format!("{base}{}", self.path),
        }
    }
}

#[cfg(any(test, feature = "fixtures"))]
pub mod fixtures {
    use super::*;

    /// A LogsQL source with every field mapped, which transect reads
    /// from `url`.
    pub const SPEC: &str = r#"
service: {namespace: logs, name: store, port: 9428}
url: http://localhost:9428
language: logsql
query: /select/logsql/query
fields:
  namespace: k8s.namespace.name
  pod: k8s.pod.name
  container: k8s.container.name
  node: k8s.node.name
  time: _time
  level: severity_text
  message: _msg
  machine: {field: k8s.daemonset.name, values: [journal], unit: k8s.container.name}
levels: {error: [err, fatal, panic, crit], warning: [warn], plain: [info, debug, trace, notice]}
queries:
  tail: '_time:>={since} {fields.namespace}:"{namespace}" {fields.pod}:"{pod}" | sort by ({fields.time}) desc | limit {limit}'
  search: '_time:{window} {pattern} | stats by ({fields.namespace}, {fields.pod}) count() matches, max({fields.time}) last | sort by (matches) desc | limit {limit}'
  lines: '_time:{window} {pattern} | sort by ({fields.time}) desc | limit {limit}'
"#;

    /// A `LogSource` document named `logs` with `spec`.
    pub fn document(spec: &str) -> serde_yaml::Value {
        let spec: String = spec.lines().map(|line| format!("  {line}\n")).collect();
        let text = format!(
            "apiVersion: transect.liken.sh/v1alpha1\nkind: LogSource\nmetadata: {{name: logs}}\nspec:\n{spec}"
        );
        serde_yaml::from_str(&text).unwrap()
    }

    pub fn source() -> LogSource {
        parse(DocId::default(), document(SPEC)).unwrap()
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::fixtures::{SPEC, document, source};
    use super::*;

    fn with(from: &str, to: &str) -> Result<LogSource, String> {
        assert!(SPEC.contains(from), "{from:?} is not in the spec");
        parse(DocId::default(), document(&SPEC.replacen(from, to, 1)))
    }

    #[rstest]
    #[case::another_language("language: logsql", "language: logql", "LogsQL")]
    #[case::neither_url_nor_service(
        "service: {namespace: logs, name: store, port: 9428}\nurl: http://localhost:9428\n",
        "",
        "url or a service"
    )]
    #[case::a_url_that_is_not_http(
        "url: http://localhost:9428",
        "url: localhost:9428",
        "http or https"
    )]
    #[case::a_relative_query_path("query: /select", "query: select", "starting with /")]
    #[case::an_unknown_placeholder(
        "limit {limit}'\n  search",
        "limit {lines}'\n  search",
        "{lines}"
    )]
    #[case::a_field_it_does_not_know(
        "({fields.time}) desc",
        "({fields.colour}) desc",
        "{fields.colour}"
    )]
    #[case::a_tail_with_no_pod(r#"{fields.pod}:"{pod}""#, "", "{pod}")]
    #[case::a_search_with_no_pattern(" {pattern} |", " |", "{pattern}")]
    #[case::lines_with_no_window("lines: '_time:{window}", "lines: '_time:1h", "{window}")]
    #[case::an_unknown_field("  message: _msg\n", "  message: _msg\n  colour: hue\n", "colour")]
    #[case::a_machine_rule_with_no_node("  node: k8s.node.name\n", "", "fields.node")]
    #[case::a_machine_rule_with_no_value("values: [journal]", "values: []", "no value")]
    #[case::a_level_field_with_no_levels(
        "levels: {error: [err, fatal, panic, crit], warning: [warn], plain: [info, debug, trace, notice]}\n",
        "",
        "fields.level needs levels"
    )]
    #[case::levels_with_no_level_field("  level: severity_text\n", "", "levels needs fields.level")]
    fn says_why_a_source_does_not_validate(
        #[case] from: &str,
        #[case] to: &str,
        #[case] says: &str,
    ) {
        let error = with(from, to).unwrap_err();
        assert!(error.contains(says), "{error:?} does not say {says:?}");
    }

    #[rstest]
    fn says_when_a_query_names_a_field_left_unmapped() {
        let spec = SPEC.replacen("  node: k8s.node.name\n", "", 1);
        let spec = spec.replacen("  machine: {field: k8s.daemonset.name, values: [journal], unit: k8s.container.name}\n", "", 1).replacen(
            "({fields.time}) desc",
            "({fields.node}) desc",
            1,
        );
        let error = parse(DocId::default(), document(&spec)).unwrap_err();
        assert!(error.contains("{fields.node}"), "{error:?}");
    }

    #[rstest]
    #[case::the_flag_wins(Some("http://127.0.0.1:1"), "http://127.0.0.1:1/select/logsql/query")]
    #[case::then_the_url(None, "http://localhost:9428/select/logsql/query")]
    fn reaches_the_store_by_the_flag_then_the_url(
        #[case] flag: Option<&str>,
        #[case] endpoint: &str,
    ) {
        assert_eq!(source().reached(flag, "cluster.local").endpoint, endpoint);
    }

    #[rstest]
    fn reaches_the_store_by_its_service_without_a_url() {
        let source = with("url: http://localhost:9428\n", "").unwrap();
        assert_eq!(
            source.reached(None, "cluster.local").endpoint,
            "http://store.logs.svc.cluster.local:9428/select/logsql/query"
        );
    }
}
