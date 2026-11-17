//! The machines' own lines are the lines that a machine's kubelet,
//! container runtime, or kernel logs.  A log source stores them beside
//! the pods' lines.  When a pod ships them, such as a `DaemonSet` that
//! reads each machine's journal, the store records that pod as their
//! source.  If transect counted them as that pod's workload's lines,
//! the shipper would look like the workload in trouble.  The source's
//! `fields.machine` declares which lines these are, so each search runs
//! twice: once for the pods' lines alone, and once for the machines'
//! lines, counted by machine and unit.

use k8s_openapi::jiff::Timestamp;

use super::query::Row;
use super::search::named;
use super::source::LogSource;

/// One machine's own lines that a search matched, in one of its logs.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct MachineCount {
    pub node: String,
    /// The machine's log that the lines came from, such as `k3s` or
    /// `kernel`.
    pub unit: Option<String>,
    pub matches: usize,
    pub last: Option<Timestamp>,
}

/// `filter` split in two: a filter for the pods' lines alone, and a
/// filter for the machines' own lines.  The second is `None` for a
/// source that declares no machine lines.
pub fn split(filter: &str, source: &LogSource) -> (String, Option<String>) {
    let Some(machine) = &source.fields.machine else {
        return (filter.into(), None);
    };
    let language = source.language;
    let own = language.one_of(&machine.field, &machine.values);
    let pods = format!("({filter}) {}", language.not(&own));
    (pods, Some(format!("({filter}) {own}")))
}

/// The machine counts in a search's rows.  When `every` is true, as in
/// the search for the machines' lines, each row that names a node
/// counts for that machine.  When it is false, only a row that names a
/// node and no namespace counts, because no pod logged those lines.
pub fn counts(rows: &[Row], source: &LogSource, every: bool) -> Vec<MachineCount> {
    let fields = &source.fields;
    let counts = rows.iter().filter_map(|row| {
        let namespace = named(row, Some(&fields.namespace));
        if !every && namespace.is_some() {
            return None;
        }
        Some(MachineCount {
            node: named(row, fields.node.as_ref())?,
            unit: row
                .get(fields.unit())
                .filter(|unit| !unit.is_empty())
                .cloned(),
            matches: row.get("matches")?.parse().ok()?,
            last: row.get("last").and_then(|last| last.parse().ok()),
        })
    });
    let counts: Vec<MachineCount> = counts.filter(|count| count.matches > 0).collect();
    merged(counts)
}

/// `counts` merged to one entry for each machine and unit, because a
/// search that groups by pod returns a row for each shipper pod that a
/// machine had.
fn merged(counts: Vec<MachineCount>) -> Vec<MachineCount> {
    let mut merged: Vec<MachineCount> = Vec::new();
    for count in counts {
        let same = merged
            .iter_mut()
            .find(|one| one.node == count.node && one.unit == count.unit);
        match same {
            Some(one) => {
                one.matches += count.matches;
                one.last = one.last.max(count.last);
            }
            None => merged.push(count),
        }
    }
    merged.sort_by(|a, b| b.matches.cmp(&a.matches).then_with(|| a.node.cmp(&b.node)));
    merged
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::source::fixtures::source;
    use super::*;

    fn row(pairs: &[(&str, &str)]) -> Row {
        pairs
            .iter()
            .map(|(key, value)| (key.to_string(), value.to_string()))
            .collect()
    }

    fn count(node: &str, unit: Option<&str>, matches: usize) -> MachineCount {
        MachineCount {
            node: node.into(),
            unit: unit.map(Into::into),
            matches,
            last: None,
        }
    }

    #[rstest]
    fn splits_a_filter_into_the_pods_lines_and_the_machines() {
        assert_eq!(
            split(r#""refused""#, &source()),
            (
                r#"("refused") !(k8s.daemonset.name:in("journal"))"#.into(),
                Some(r#"("refused") k8s.daemonset.name:in("journal")"#.into())
            )
        );
    }

    #[rstest]
    fn leaves_a_filter_whole_without_a_machine_rule() {
        let mut source = source();
        source.fields.machine = None;
        assert_eq!(split("x", &source), ("x".into(), None));
    }

    const POD: (&str, &str) = ("k8s.pod.name", "journal-x7k2q");

    #[rstest]
    #[case::the_machines_search(
        true,
        vec![
            row(&[("k8s.namespace.name", "system"), POD, ("k8s.container.name", "k3s"), ("k8s.node.name", "machine-a"), ("matches", "3")]),
            row(&[("k8s.namespace.name", "system"), ("k8s.pod.name", "journal-b4n8z"), ("k8s.container.name", "k3s"), ("k8s.node.name", "machine-a"), ("matches", "2")]),
            row(&[("k8s.namespace.name", "system"), POD, ("k8s.container.name", "kernel"), ("k8s.node.name", "machine-b"), ("matches", "9")]),
        ],
        vec![count("machine-b", Some("kernel"), 9), count("machine-a", Some("k3s"), 5)]
    )]
    #[case::a_row_with_no_node(true, vec![row(&[("matches", "3")])], vec![])]
    #[case::the_pods_search_keeps_lines_from_no_pod(
        false,
        vec![
            row(&[("k8s.namespace.name", "shop"), ("k8s.node.name", "machine-a"), ("matches", "3")]),
            row(&[("k8s.node.name", "machine-a"), ("matches", "4")]),
        ],
        vec![count("machine-a", None, 4)]
    )]
    fn counts_each_machines_lines_by_unit(
        #[case] every: bool,
        #[case] rows: Vec<Row>,
        #[case] expected: Vec<MachineCount>,
    ) {
        assert_eq!(counts(&rows, &source(), every), expected);
    }
}
