//! A link to the commit an image was built from, read from its source
//! and revision labels.

use super::{Forges, safe, web};

/// What to show for a commit: a link when one can be built, otherwise
/// plain text.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Commit {
    /// The URL of the commit's page, and link text made of the
    /// repository's name and the short revision, as "app@0a1b2c3".
    Link { href: String, text: String },
    /// The source and revision unchanged, for when the source is not a
    /// web address, no `Forge` matches its host, or the revision is empty
    /// or cannot go into a URL unescaped.
    Text { source: String, revision: String },
}

/// How many characters of a revision a link shows, as git abbreviates
/// one.
const SHORT: usize = 7;

/// The link to `revision` in the repository at `source`, or the text of
/// both when no link can be built.
pub fn commit(source: &str, revision: &str, forges: &Forges) -> Commit {
    let link = || {
        let found = web(source)?;
        let forge = forges.of(found.host)?;
        let revision_ok = !revision.is_empty() && !revision.contains('/') && safe(revision);
        if !revision_ok {
            return None;
        }
        let short: String = revision.chars().take(SHORT).collect();
        Some(Commit::Link {
            href: forge.commit(&found.base(), revision),
            text: format!("{}@{short}", found.name()),
        })
    };
    link().unwrap_or_else(|| Commit::Text {
        source: source.into(),
        revision: revision.into(),
    })
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    const SHA: &str = "0a1b2c3d4e5f60718293a4b5c6d7e8f901234567";

    fn link(href: &str, text: &str) -> Commit {
        Commit::Link {
            href: href.into(),
            text: text.into(),
        }
    }

    fn text(source: &str, revision: &str) -> Commit {
        Commit::Text {
            source: source.into(),
            revision: revision.into(),
        }
    }

    /// Forges that include a Gitea at `forge.example.com`, which
    /// `Forges::build` recognizes from the image that runs there.
    fn served() -> Forges {
        Forges::build(
            &super::super::fixtures::layouts(),
            [(
                "forge.example.com".into(),
                vec!["gitea/gitea:1.22.0".into()],
            )],
        )
    }

    #[rstest]
    #[case::github(
        "https://github.com/acme/app",
        SHA,
        link(&format!("https://github.com/acme/app/commit/{SHA}"), "app@0a1b2c3")
    )]
    #[case::github_with_dot_git(
        "https://github.com/acme/app.git",
        "abc1234",
        link("https://github.com/acme/app/commit/abc1234", "app@abc1234")
    )]
    #[case::gitlab_in_a_subgroup(
        "https://gitlab.com/acme/platform/app",
        "abc1234",
        link("https://gitlab.com/acme/platform/app/-/commit/abc1234", "app@abc1234")
    )]
    #[case::codeberg(
        "https://codeberg.org/acme/app",
        "abc1234",
        link("https://codeberg.org/acme/app/commit/abc1234", "app@abc1234")
    )]
    #[case::a_forge_served_by_the_cluster(
        "https://forge.example.com/team/app",
        "abc1234",
        link("https://forge.example.com/team/app/commit/abc1234", "app@abc1234")
    )]
    #[case::an_unknown_host(
        "https://code.example.com/team/app",
        "abc1234",
        text("https://code.example.com/team/app", "abc1234")
    )]
    #[case::a_revision_that_needs_url_escaping(
        "https://github.com/acme/app",
        "v1 \"x\"",
        text("https://github.com/acme/app", "v1 \"x\"")
    )]
    #[case::a_revision_with_a_path_separator(
        "https://github.com/acme/app",
        "../x",
        text("https://github.com/acme/app", "../x")
    )]
    #[case::no_revision(
        "https://github.com/acme/app",
        "",
        text("https://github.com/acme/app", "")
    )]
    fn links_a_commit_only_on_a_known_forge(
        #[case] source: &str,
        #[case] revision: &str,
        #[case] expected: Commit,
    ) {
        assert_eq!(commit(source, revision, &served()), expected);
    }
}
