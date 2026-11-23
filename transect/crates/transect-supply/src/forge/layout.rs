//! The `Forge` resource: the URLs of one forge's pages, and the ways to
//! recognize a repository that the forge hosts.
//!
//! `tree` and `commit` are URL templates for the page of a directory
//! and the page of a commit.  A template can use these placeholders:
//!
//! - `{repository}` is the repository's web address, such as
//!   `https://forge.example/owner/app`.
//! - `{ref}` is the branch, tag, or commit.
//! - `{refKind}` is `branch`, `tag`, or `commit`, for a forge whose
//!   directory URLs include the kind of the ref.
//! - `{path}` is the directory.  At the repository's root it is
//!   removed, with the slash before it.
//! - `{sha}` is the commit.

use serde::Deserialize;

use transect_resources::resources::DocId;

/// One `Forge` resource, validated.
#[derive(Clone, Debug, PartialEq)]
pub struct Forge {
    pub doc: DocId,
    pub name: String,
    /// The hosts that serve this forge, in lower case.
    pub hosts: Vec<String>,
    /// The first labels of the host names that serve this forge, such as
    /// `gitea` for `gitea.example.com`.
    pub host_prefixes: Vec<String>,
    /// The last parts of the repository names of the images that run
    /// this forge.  They let transect match a host that the cluster
    /// serves through an `Ingress` to this forge, by the images behind
    /// it.
    pub images: Vec<String>,
    pub tree: String,
    pub commit: String,
}

/// The ref that a directory page shows.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Ref {
    Branch(String),
    Tag(String),
    Commit(String),
    /// A branch, a tag, or a commit, when the source does not say which.
    Named(String),
}

impl Ref {
    pub fn name(&self) -> &str {
        match self {
            Self::Branch(name) | Self::Tag(name) | Self::Commit(name) | Self::Named(name) => name,
        }
    }

    /// `branch`, `tag`, or `commit`.  It is `None` for a `Named` ref that
    /// is not a full commit hash, because its kind is not known.
    fn kind(&self) -> Option<&'static str> {
        match self {
            Self::Branch(_) => Some("branch"),
            Self::Tag(_) => Some("tag"),
            Self::Commit(_) => Some("commit"),
            Self::Named(name) if full_commit(name) => Some("commit"),
            Self::Named(_) => None,
        }
    }
}

/// Whether `name` is a full commit hash, 40 hex digits for SHA-1 or 64
/// for SHA-256.  It is the only kind of ref that the name alone
/// identifies.
fn full_commit(name: &str) -> bool {
    matches!(name.len(), 40 | 64) && name.chars().all(|c| c.is_ascii_hexdigit())
}

impl Forge {
    /// The page of the directory `path` at `reference` in the repository
    /// whose web address is `repository`.  It is `None` when the
    /// template uses `{refKind}` and the kind of `reference` is not
    /// known.
    pub fn tree(&self, repository: &str, reference: &Ref, path: &str) -> Option<String> {
        let template = match path {
            "" => self.tree.replace("/{path}", "").replace("{path}", ""),
            _ => self.tree.clone(),
        };
        let kind = match template.contains("{refKind}") {
            true => reference.kind()?,
            false => "",
        };
        Some(
            template
                .replace("{repository}", repository)
                .replace("{refKind}", kind)
                .replace("{ref}", reference.name())
                .replace("{path}", path),
        )
    }

    /// The page of the commit `sha` in `repository`.
    pub fn commit(&self, repository: &str, sha: &str) -> String {
        self.commit
            .replace("{repository}", repository)
            .replace("{sha}", sha)
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
#[serde(deny_unknown_fields, rename_all = "camelCase")]
struct Spec {
    #[serde(default)]
    hosts: Vec<String>,
    #[serde(default)]
    host_prefixes: Vec<String>,
    #[serde(default)]
    images: Vec<String>,
    tree: String,
    commit: String,
}

const TREE: [&str; 4] = ["{repository}", "{refKind}", "{ref}", "{path}"];
const COMMIT: [&str; 2] = ["{repository}", "{sha}"];

/// The `Forge` that `value` declares, or why `value` is not valid.
pub fn parse(doc: DocId, value: serde_yaml_ng::Value) -> Result<Forge, String> {
    let document: Document = serde_yaml_ng::from_value(value).map_err(|error| error.to_string())?;
    let spec = document.spec;
    check("tree", &spec.tree, &TREE, &["{repository}", "{ref}"])?;
    check("commit", &spec.commit, &COMMIT, &COMMIT)?;
    let lower = |names: Vec<String>| names.iter().map(|n| n.to_ascii_lowercase()).collect();
    Ok(Forge {
        doc,
        name: document.metadata.name,
        hosts: lower(spec.hosts),
        host_prefixes: lower(spec.host_prefixes),
        images: spec.images,
        tree: spec.tree,
        commit: spec.commit,
    })
}

/// Checks that `template` starts with `{repository}`, contains every
/// placeholder in `needed`, and contains no placeholder outside `known`.
/// The error names the first check that failed.
fn check(field: &str, template: &str, known: &[&str], needed: &[&str]) -> Result<(), String> {
    // Text before `{repository}` could make the link point at a
    // different host from the repository's.
    if !template.starts_with("{repository}") {
        return Err(format!(
            "{field} {template:?} does not start with {{repository}}"
        ));
    }
    if let Some(missing) = needed.iter().find(|p| !template.contains(**p)) {
        return Err(format!("{field} {template:?} has no {missing}"));
    }
    let mut rest = template.to_string();
    for placeholder in known {
        rest = rest.replace(placeholder, "");
    }
    if rest.contains('{') {
        return Err(format!(
            "{field} {template:?} contains a placeholder other than {}",
            known.join(", ")
        ));
    }
    Ok(())
}

#[cfg(any(test, feature = "fixtures"))]
pub mod fixtures {
    use super::*;

    /// `Forge` documents for GitHub, GitLab, and Gitea, written the way
    /// a cluster declares them.
    pub const LAYOUTS: &str = r#"
metadata: {name: github}
spec: {hosts: [github.com], tree: '{repository}/tree/{ref}/{path}', commit: '{repository}/commit/{sha}'}
---
metadata: {name: gitlab}
spec: {hosts: [gitlab.com], hostPrefixes: [gitlab], images: [gitlab-ce, gitlab-ee], tree: '{repository}/-/tree/{ref}/{path}', commit: '{repository}/-/commit/{sha}'}
---
metadata: {name: gitea}
spec: {hosts: [codeberg.org], hostPrefixes: [gitea, forgejo], images: [gitea, forgejo], tree: '{repository}/src/{refKind}/{ref}/{path}', commit: '{repository}/commit/{sha}'}
"#;

    pub fn layouts() -> Vec<Forge> {
        serde_yaml_ng::Deserializer::from_str(LAYOUTS)
            .map(|document| {
                let value = serde_yaml_ng::Value::deserialize(document).unwrap();
                parse(DocId::default(), value).unwrap()
            })
            .collect()
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn gitea() -> Forge {
        fixtures::layouts().remove(2)
    }

    #[rstest]
    #[case::a_branch(Ref::Branch("main".into()), "apps", Some("https://f.example/a/b/src/branch/main/apps"))]
    #[case::the_root(Ref::Tag("v1".into()), "", Some("https://f.example/a/b/src/tag/v1"))]
    #[case::a_full_commit_hash_of_unknown_kind(
        Ref::Named("0a1b2c3d4e5f60718293a4b5c6d7e8f901234567".into()),
        "x",
        Some("https://f.example/a/b/src/commit/0a1b2c3d4e5f60718293a4b5c6d7e8f901234567/x")
    )]
    #[case::a_name_of_unknown_kind(Ref::Named("main".into()), "x", None)]
    fn fills_the_tree_template(
        #[case] reference: Ref,
        #[case] path: &str,
        #[case] expected: Option<&str>,
    ) {
        let found = gitea().tree("https://f.example/a/b", &reference, path);
        assert_eq!(found.as_deref(), expected);
    }

    fn read(spec: &str) -> Result<Forge, String> {
        let text = format!("metadata: {{name: forge}}\nspec: {spec}\n");
        parse(DocId::default(), serde_yaml_ng::from_str(&text).unwrap())
    }

    #[rstest]
    #[case::a_tree_with_no_ref(
        "{tree: '{repository}/tree', commit: '{repository}/c/{sha}'}",
        "has no {ref}"
    )]
    #[case::a_commit_with_no_sha(
        "{tree: '{repository}/{ref}', commit: '{repository}/c'}",
        "has no {sha}"
    )]
    #[case::an_unknown_placeholder(
        "{tree: '{repository}/{ref}/{dir}', commit: '{repository}/{sha}'}",
        "other than"
    )]
    #[case::text_before_the_repository(
        "{tree: 'x{repository}/{ref}', commit: '{repository}/{sha}'}",
        "does not start with {repository}"
    )]
    #[case::an_unknown_field(
        "{tree: '{repository}/{ref}', commit: '{repository}/{sha}', ssh: true}",
        "unknown field"
    )]
    fn reports_why_a_forge_is_not_valid(#[case] spec: &str, #[case] expected: &str) {
        let error = read(spec).unwrap_err();
        assert!(error.contains(expected), "{error:?}, want {expected:?}");
    }
}
