//! What kind knowledge declares about a findings report: an object a
//! scanner writes for one container's image, naming the workload it
//! scanned, the digest of the image, when it ran, and a list of
//! findings, each with an id, a severity, and what the scanner reports
//! about it.  A kind with this knowledge stays trimmed, and its trimmed
//! objects keep only the fields named here, so a store never holds a
//! finding's description, or its title unless the knowledge names it.

use serde::Deserialize;

use super::MetaKey;

/// How a findings report is read.
#[derive(Clone, Debug, PartialEq)]
pub struct Findings {
    /// The label or annotation naming the container scanned.
    pub container: Option<MetaKey>,
    /// The field holding the digest of the image scanned.
    pub digest: String,
    /// The field holding when the scan ran.
    pub scanned_at: Option<String>,
    /// The scanner's severities, most severe first, each with the field
    /// holding the report's own count of it.
    pub summary: Vec<Severity>,
    /// The list of findings, written with its `[]`.
    pub items: String,
    pub item: Item,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Severity {
    pub severity: String,
    pub path: String,
}

/// The fields of one finding, each a path inside it.
#[derive(Clone, Debug, PartialEq)]
pub struct Item {
    pub id: String,
    pub severity: String,
    pub score: Option<String>,
    pub package: Option<String>,
    pub installed: Option<String>,
    pub fixed: Option<String>,
    pub link: Option<String>,
    pub title: Option<String>,
}

impl Findings {
    /// Every path a trimmed watch keeps for the kind: the digest, the
    /// scan time, the summary counts, and each item field inside each
    /// finding.
    pub fn paths(&self) -> Vec<String> {
        let mut paths = vec![self.digest.clone()];
        paths.extend(self.scanned_at.clone());
        paths.extend(self.summary.iter().map(|severity| severity.path.clone()));
        let item = &self.item;
        let fields = [
            &item.score,
            &item.package,
            &item.installed,
            &item.fixed,
            &item.link,
            &item.title,
        ];
        let fields = [Some(&item.id), Some(&item.severity)]
            .into_iter()
            .chain(fields.into_iter().map(Option::as_ref))
            .flatten();
        paths.extend(fields.map(|field| format!("{}.{field}", self.items)));
        paths
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
pub(super) struct Written {
    subject: WrittenSubject,
    digest: String,
    scanned_at: Option<String>,
    #[serde(default)]
    summary: Vec<WrittenSeverity>,
    items: String,
    item: WrittenItem,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct WrittenSubject {
    owner: bool,
    container: Option<WrittenKey>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct WrittenKey {
    label: Option<String>,
    annotation: Option<String>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct WrittenSeverity {
    severity: String,
    path: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct WrittenItem {
    id: String,
    severity: String,
    score: Option<String>,
    package: Option<String>,
    installed: Option<String>,
    fixed: Option<String>,
    link: Option<String>,
    title: Option<String>,
}

/// The findings knowledge `written` declares, or why it does not
/// validate.
pub(super) fn read(written: Written) -> Result<Findings, String> {
    if !written.subject.owner {
        return Err(
            "findings.subject.owner is false, and the owner is the one subject transect reads"
                .into(),
        );
    }
    if !written.items.ends_with("[]") {
        return Err(format!(
            "findings.items is {:?}, and a list is written with its []",
            written.items
        ));
    }
    let container = match written.subject.container {
        None => None,
        Some(WrittenKey {
            label: Some(label),
            annotation: None,
        }) => Some(MetaKey::Label(label)),
        Some(WrittenKey {
            label: None,
            annotation: Some(annotation),
        }) => Some(MetaKey::Annotation(annotation)),
        Some(_) => {
            return Err(
                "findings.subject.container names a label or an annotation, and exactly one".into(),
            );
        }
    };
    let item = written.item;
    Ok(Findings {
        container,
        digest: written.digest,
        scanned_at: written.scanned_at,
        summary: written
            .summary
            .into_iter()
            .map(|one| Severity {
                severity: one.severity,
                path: one.path,
            })
            .collect(),
        items: written.items,
        item: Item {
            id: item.id,
            severity: item.severity,
            score: item.score,
            package: item.package,
            installed: item.installed,
            fixed: item.fixed,
            link: item.link,
            title: item.title,
        },
    })
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use crate::knowledge::fixtures::knowledge;

    const SPEC: &str = "{findings: {subject: {owner: true, container: {label: scan/container}}, digest: report.digest, scannedAt: report.at, summary: [{severity: HIGH, path: report.summary.high}], items: 'report.found[]', item: {id: id, severity: level, fixed: fix, title: heading}}}";

    #[rstest]
    fn keeps_each_field_it_names() {
        let known = knowledge("scans.scan.example.io", SPEC);
        let paths = known.findings.unwrap().paths();
        assert_eq!(
            paths,
            vec![
                "report.digest",
                "report.at",
                "report.summary.high",
                "report.found[].id",
                "report.found[].level",
                "report.found[].fix",
                "report.found[].heading",
            ]
        );
    }

    #[rstest]
    #[case::not_a_list(
        "{findings: {subject: {owner: true}, digest: d, items: report.found, item: {id: id, severity: s}}}"
    )]
    #[case::not_the_owner(
        "{findings: {subject: {owner: false}, digest: d, items: 'f[]', item: {id: id, severity: s}}}"
    )]
    #[case::two_keys(
        "{findings: {subject: {owner: true, container: {label: a, annotation: b}}, digest: d, items: 'f[]', item: {id: id, severity: s}}}"
    )]
    #[case::no_id(
        "{findings: {subject: {owner: true}, digest: d, items: 'f[]', item: {severity: s}}}"
    )]
    fn refuses_what_it_cannot_read(#[case] spec: &str) {
        let text = format!("metadata: {{name: scans.scan.example.io}}\nspec: {spec}\n");
        let parsed = crate::knowledge::parse(
            crate::resources::DocId::default(),
            serde_yaml::from_str(&text).unwrap(),
        );
        assert!(parsed.is_err());
    }
}
