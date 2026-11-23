//! Kind knowledge for a scanner's findings report. A scanner writes one
//! report for the image of one container, owned by the scanned
//! workload, with the image digest, the time of the scan, and a list of
//! findings. `transect` keeps only the paths that `Findings::paths`
//! returns from such a report, so it never stores a finding's
//! description, and stores its title only when the knowledge sets
//! `item.title`.

use serde::Deserialize;

use super::MetaKey;

/// The fields to read from a findings report.
#[derive(Clone, Debug, PartialEq)]
pub struct Findings {
    /// The label or annotation that names the container that was
    /// scanned.
    pub container: Option<MetaKey>,
    /// The path of the digest of the scanned image.
    pub digest: String,
    /// The path of the time of the scan.
    pub scanned_at: Option<String>,
    /// The scanner's severity levels, most severe first, each with the
    /// path where the report counts the findings of that level.
    pub summary: Vec<Severity>,
    /// The path of the list of findings, which ends with `[]`.
    pub items: String,
    pub item: Item,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Severity {
    pub severity: String,
    pub path: String,
}

/// The fields of one finding. Each is a path relative to the finding.
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
    /// The paths that a trimmed watch keeps for the kind.
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

pub(super) fn read(written: Written) -> Result<Findings, String> {
    if !written.subject.owner {
        return Err(
            "findings.subject.owner is false, and transect reads only the owner as the subject"
                .into(),
        );
    }
    if !written.items.ends_with("[]") {
        return Err(format!(
            "findings.items is {:?}, and a list path must end with []",
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
                "findings.subject.container must set exactly one of label and annotation".into(),
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
            serde_yaml_ng::from_str(&text).unwrap(),
        );
        assert!(parsed.is_err());
    }
}
