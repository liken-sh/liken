//! The last revision that each applier applied, and when.  The time
//! comes from the applier's status when the status records one.
//! Otherwise it is the time that the applier's source fetched that
//! revision, because an applier applies what its source fetched.  When
//! the source has since fetched a newer revision, that time belongs to
//! the newer revision, so transect uses the `lastTransitionTime` of the
//! applier's health condition.  An applier with none of these times is
//! left out.

use k8s_openapi::jiff::Timestamp;

use super::GitOps;
use super::template::captures;
use transect_graph::edges::Object;

/// The fewest hex digits that transect accepts as a commit.  Git's
/// default short hash has the same length.
const SHORTEST_COMMIT: usize = 7;

/// One applier's last applied revision.
#[derive(Clone, Debug, PartialEq)]
pub struct Revision {
    pub applier: Object,
    pub source: Option<Object>,
    /// The URL it fetches from.
    pub url: Option<String>,
    pub revision: String,
    /// The formats that split `revision` into a ref and a commit.
    pub formats: Vec<String>,
    pub at: Timestamp,
}

impl Revision {
    /// The ref and the commit that the revision's formats find in it.
    pub fn parts(&self) -> (&str, Option<&str>) {
        parts(&self.revision, &self.formats)
    }
}

impl GitOps {
    /// The last applied revision of every applier that applies a
    /// directory, sorted by applier.
    pub fn revisions(&self) -> Vec<Revision> {
        let found = self.found.values().filter_map(|found| {
            let fields = self.fields_of(&found.object)?;
            fields.directory.as_ref()?;
            let revision = found.revision.clone()?;
            let source = self.source_of(found);
            let fetched = source.as_ref().and_then(|source| {
                let source = self.found.get(source)?;
                let same = source.revision.as_ref() == Some(&revision);
                same.then_some(source.revision_at)?
            });
            let formats = fields.revision.as_ref().map(|r| r.formats.clone());
            Some(Revision {
                applier: found.object.clone(),
                url: self.url_of(found),
                source,
                revision,
                formats: formats.unwrap_or_default(),
                at: found.revision_at.or(fetched).or(found.ready_since)?,
            })
        });
        let mut found: Vec<Revision> = found.collect();
        found.sort_by(|a, b| a.applier.cmp(&b.applier));
        found
    }
}

/// Splits `revision` into its ref, such as `main` in `main@sha1:0a1b2c3`,
/// and its commit, using the first of `formats` that matches.  A
/// `{commit}` must be at least `SHORTEST_COMMIT` hex digits, so a chart
/// version is never read as a commit.  When no format matches, the whole
/// revision is the ref and there is no commit.
pub fn parts<'a>(revision: &'a str, formats: &[String]) -> (&'a str, Option<&'a str>) {
    let commit = |found: &&'a str| {
        found.len() >= SHORTEST_COMMIT && found.chars().all(|c| c.is_ascii_hexdigit())
    };
    let matched = formats.iter().find_map(|format| {
        let found = captures(format, revision)?;
        let sha = found.get("commit").copied();
        if sha.is_some_and(|sha| !commit(&sha)) {
            return None;
        }
        Some((found.get("ref").copied().unwrap_or_default(), sha))
    });
    matched.unwrap_or((revision, None))
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn formats() -> Vec<String> {
        ["{ref}@sha1:{commit}", "sha1:{commit}", "{ref}@*"]
            .map(String::from)
            .to_vec()
    }

    #[rstest]
    #[case::a_branch("main@sha1:0a1b2c3", ("main", Some("0a1b2c3")))]
    #[case::an_oci_tag("2026.01.01-001@sha256:ffee", ("2026.01.01-001", None))]
    #[case::a_bare_commit("sha1:0a1b2c3", ("", Some("0a1b2c3")))]
    #[case::a_bare_digest("sha256:ffee", ("sha256:ffee", None))]
    #[case::a_chart_version("1.4.0", ("1.4.0", None))]
    #[case::a_commit_that_is_no_hex("main@sha1:not-a-sha", ("main", None))]
    fn reads_a_revisions_ref_and_commit(
        #[case] revision: &str,
        #[case] expected: (&str, Option<&str>),
    ) {
        assert_eq!(parts(revision, &formats()), expected);
    }

    fn tree(name: &str, revision: &str) -> String {
        format!(
            "spec: {{path: ./{name}, sourceRef: {{kind: Repo, name: fleet}}}}\nstatus: {{lastAppliedRevision: '{revision}', conditions: [{{type: Ready, status: 'True', lastTransitionTime: '2026-01-01T11:30:00Z'}}]}}"
        )
    }

    /// A tree whose source still has the applied revision gets the time
    /// that the source fetched it.  A tree whose source has fetched a
    /// newer revision gets the time that its condition last changed.
    #[rstest]
    fn dates_each_revision_by_the_fetch_or_else_by_its_condition() {
        let repo = "spec: {url: 'https://forge.example.test/fleet.git'}\nstatus: {artifact: {revision: 'main@sha1:bbbbbbb', lastUpdateTime: '2026-01-01T10:00:00Z'}}";
        let (shop, blog) = (
            tree("shop", "main@sha1:bbbbbbb"),
            tree("blog", "main@sha1:aaaaaaa"),
        );
        let gitops = crate::gitops::fixtures::gitops(&[
            ("Repo", "ops", "fleet", "", repo),
            ("Tree", "ops", "shop", "", &shop),
            ("Tree", "ops", "blog", "", &blog),
        ]);
        let found: Vec<(String, String)> = gitops
            .revisions()
            .into_iter()
            .map(|r| (r.applier.name, r.at.to_string()))
            .collect();
        let row = |name: &str, at: &str| (name.to_string(), at.to_string());
        assert_eq!(
            found,
            vec![
                row("blog", "2026-01-01T11:30:00Z"),
                row("shop", "2026-01-01T10:00:00Z")
            ]
        );
    }

    #[rstest]
    #[case::a_commit("0a1b2c3d4e5f", ("", Some("0a1b2c3d4e5f")))]
    #[case::a_chart_version("1.4.0", ("1.4.0", None))]
    fn reads_a_bare_commit_by_its_digits(
        #[case] revision: &str,
        #[case] expected: (&str, Option<&str>),
    ) {
        assert_eq!(parts(revision, &["{commit}".to_string()]), expected);
    }
}
