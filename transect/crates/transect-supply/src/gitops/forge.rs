//! Builds the link to an applier's directory on the forge that hosts the
//! repository, at the ref that the applier's source follows.

use super::GitRef;
use crate::forge::{Forges, Ref, safe, steps_up, web};

/// Where a card's code link goes.
#[derive(Clone, Debug, PartialEq)]
pub enum Place {
    /// The URL of the directory's page, and the repository name and path
    /// to show as the link text.
    Link { href: String, text: String },
    /// The repository's URL and the path, shown as text for a directory
    /// that transect cannot link to.
    Text { repository: String, path: String },
}

/// Where `path` in the repository at `url` is shown.  It is a `Link`
/// when a `Forge` layout or a forge that the cluster runs matches the
/// repository's host, `reference` gives a ref that is safe to put in a
/// URL, and `path` is too.  Otherwise it is `Text`.
pub fn place(url: &str, reference: &GitRef, path: &str, forges: &Forges) -> Place {
    let link = || {
        let found = web(url)?;
        let forge = forges.of(found.host)?;
        let reference = of(reference)?;
        let path = clean(path)?;
        let path = path.as_str();
        let href = forge.tree(&found.base(), &reference, path)?;
        let name = found.name();
        let text = match path {
            "" => name.to_string(),
            path => format!("{name}/{path}"),
        };
        Some(Place::Link { href, text })
    };
    link().unwrap_or_else(|| Place::Text {
        repository: url.to_string(),
        path: if path.is_empty() {
            "./".into()
        } else {
            path.into()
        },
    })
}

/// The ref to put in the directory's URL: the commit, else the tag, else
/// the branch, else a ref of unknown type, because a commit fixes the
/// exact content that the source applies.  It returns `None` when the
/// chosen ref is not safe in a URL, and when the source has no ref, such
/// as a source that follows a semver range, because no single directory
/// page shows what that source applies.
fn of(reference: &GitRef) -> Option<Ref> {
    let found = [
        reference.commit.clone().map(Ref::Commit),
        reference.tag.clone().map(Ref::Tag),
        reference.branch.clone().map(Ref::Branch),
        reference.name.clone().map(Ref::Named),
    ];
    let found = found.into_iter().flatten().next()?;
    (safe(found.name()) && !steps_up(found.name())).then_some(found)
}

/// `path` without a leading `./` and without slashes at either end, or
/// `None` when it contains `..` or a character that a URL would need to
/// escape.
fn clean(path: &str) -> Option<String> {
    let path = path.trim_start_matches("./").trim_matches('/');
    let path = if path == "." { "" } else { path };
    (safe(path) && !steps_up(path)).then(|| path.to_string())
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    /// A repository at `url` and the ref its source follows.
    type Source = (String, GitRef);

    fn git(url: &str, branch: Option<&str>, tag: Option<&str>, commit: Option<&str>) -> Source {
        let reference = GitRef {
            branch: branch.map(Into::into),
            tag: tag.map(Into::into),
            commit: commit.map(Into::into),
            ..Default::default()
        };
        (url.into(), reference)
    }

    fn named(url: &str, name: &str) -> Source {
        let reference = GitRef {
            name: Some(name.into()),
            ..Default::default()
        };
        (url.into(), reference)
    }

    fn on_main(url: &str) -> Source {
        git(url, Some("main"), None, None)
    }

    fn link(href: &str, text: &str) -> Place {
        Place::Link {
            href: href.into(),
            text: text.into(),
        }
    }

    fn text(repository: &str, path: &str) -> Place {
        Place::Text {
            repository: repository.into(),
            path: path.into(),
        }
    }

    /// A forge the cluster serves at `forge.example.com`, behind an
    /// `Ingress` to a Gitea image.
    fn served() -> Forges {
        Forges::build(
            &crate::forge::fixtures::layouts(),
            [
                (
                    "forge.example.com".into(),
                    vec!["gitea/gitea:1.22.0".into()],
                ),
                ("wiki.example.com".into(), vec!["wiki/wiki:3".into()]),
            ],
        )
    }

    #[rstest]
    #[case::github(
        on_main("https://github.com/acme/fleet"),
        "./apps/shop",
        link("https://github.com/acme/fleet/tree/main/apps/shop", "fleet/apps/shop")
    )]
    #[case::github_with_dot_git_and_a_slash(
        on_main("https://github.com/acme/fleet.git/"),
        "apps/shop/",
        link("https://github.com/acme/fleet/tree/main/apps/shop", "fleet/apps/shop")
    )]
    #[case::github_over_ssh(
        on_main("ssh://git@github.com/acme/fleet.git"),
        "./shop",
        link("https://github.com/acme/fleet/tree/main/shop", "fleet/shop")
    )]
    #[case::github_scp_style(
        on_main("git@github.com:acme/fleet.git"),
        "./shop",
        link("https://github.com/acme/fleet/tree/main/shop", "fleet/shop")
    )]
    #[case::the_repository_root(
        on_main("https://github.com/acme/fleet"),
        "./",
        link("https://github.com/acme/fleet/tree/main", "fleet")
    )]
    #[case::a_tag_on_github(
        git("https://github.com/acme/fleet", None, Some("v1.2.0"), None),
        "./deploy",
        link("https://github.com/acme/fleet/tree/v1.2.0/deploy", "fleet/deploy")
    )]
    #[case::gitlab_in_a_subgroup(
        on_main("https://gitlab.com/acme/platform/fleet.git"),
        "./shop",
        link(
            "https://gitlab.com/acme/platform/fleet/-/tree/main/shop",
            "fleet/shop"
        )
    )]
    #[case::codeberg(
        on_main("https://codeberg.org/acme/fleet.git"),
        "./shop",
        link("https://codeberg.org/acme/fleet/src/branch/main/shop", "fleet/shop")
    )]
    #[case::a_gitea_host_by_name(
        git("https://gitea.example.com/acme/fleet", None, Some("v2"), None),
        "./shop",
        link("https://gitea.example.com/acme/fleet/src/tag/v2/shop", "fleet/shop")
    )]
    #[case::a_commit_on_gitea(
        git(
            "https://gitea.example.com/acme/fleet",
            Some("main"),
            None,
            Some("0a1b2c")
        ),
        "./shop",
        link(
            "https://gitea.example.com/acme/fleet/src/commit/0a1b2c/shop",
            "fleet/shop"
        )
    )]
    #[case::a_forge_the_cluster_serves_over_ssh_on_its_own_port(
        on_main("ssh://git@forge.example.com:2222/acme/fleet.git"),
        "./shop",
        link(
            "https://forge.example.com/acme/fleet/src/branch/main/shop",
            "fleet/shop"
        )
    )]
    #[case::any_ref_on_github(
        named("https://github.com/acme/fleet", "v1.2.0"),
        "./deploy",
        link("https://github.com/acme/fleet/tree/v1.2.0/deploy", "fleet/deploy")
    )]
    #[case::a_full_commit_of_unknown_type_on_gitea(
        named(
            "https://gitea.example.com/acme/fleet",
            "0a1b2c3d4e5f60718293a4b5c6d7e8f901234567"
        ),
        "./shop",
        link(
            "https://gitea.example.com/acme/fleet/src/commit/0a1b2c3d4e5f60718293a4b5c6d7e8f901234567/shop",
            "fleet/shop"
        )
    )]
    #[case::a_branch_of_unknown_type_on_gitea(
        named("https://gitea.example.com/acme/fleet", "main"),
        "./shop",
        text("https://gitea.example.com/acme/fleet", "./shop")
    )]
    #[case::an_unknown_host(
        on_main("https://code.example.com/acme/fleet"),
        "./shop",
        text("https://code.example.com/acme/fleet", "./shop")
    )]
    #[case::a_host_the_cluster_serves_that_is_no_forge(
        on_main("https://wiki.example.com/acme/fleet"),
        "./shop",
        text("https://wiki.example.com/acme/fleet", "./shop")
    )]
    #[case::an_oci_repository(
        git("oci://registry.example.com/acme/fleet", None, Some("v1"), None),
        "./",
        text("oci://registry.example.com/acme/fleet", "./")
    )]
    #[case::no_branch_tag_or_commit(
        git("https://github.com/acme/fleet", None, None, None),
        "./shop",
        text("https://github.com/acme/fleet", "./shop")
    )]
    #[case::a_repository_without_an_owner(
        on_main("https://github.com/fleet"),
        "./shop",
        text("https://github.com/fleet", "./shop")
    )]
    #[case::a_path_that_steps_up(
        on_main("https://github.com/acme/fleet"),
        "./shop/../secrets",
        text("https://github.com/acme/fleet", "./shop/../secrets")
    )]
    #[case::a_ref_that_steps_up(
        git("https://github.com/acme/fleet", Some("../../other/repo"), None, None),
        "./shop",
        text("https://github.com/acme/fleet", "./shop")
    )]
    #[case::a_path_a_url_would_escape(
        on_main("https://github.com/acme/fleet"),
        "./my shop",
        text("https://github.com/acme/fleet", "./my shop")
    )]
    fn links_a_directory_only_on_a_forge_it_knows(
        #[case] source: Source,
        #[case] path: &str,
        #[case] expected: Place,
    ) {
        let (url, reference) = source;
        assert_eq!(place(&url, &reference, path, &served()), expected);
    }

    /// Two forges whose repository URLs put a mark before the owner: a
    /// tilde at one host, as SourceHut does, and an at sign at another,
    /// as Tangled does.
    fn owner_marked() -> Forges {
        let layouts = r#"
metadata: {name: tilde}
spec: {hosts: [git.tilde.example], tree: '{repository}/tree/{ref}/item/{path}', commit: '{repository}/commit/{sha}'}
---
metadata: {name: at}
spec: {hosts: [at.example], tree: '{repository}/tree/{ref}/{path}', commit: '{repository}/commit/{sha}'}
"#;
        let layouts: Vec<_> = serde_yaml::Deserializer::from_str(layouts)
            .map(|document| {
                let value = <serde_yaml::Value as serde::Deserialize>::deserialize(document);
                crate::forge::parse(
                    transect_resources::resources::DocId::default(),
                    value.unwrap(),
                )
                .unwrap()
            })
            .collect();
        Forges::build(&layouts, [])
    }

    #[rstest]
    #[case::a_tilde_owner(
        "https://git.tilde.example/~acme/fleet",
        link(
            "https://git.tilde.example/~acme/fleet/tree/main/item/shop",
            "fleet/shop"
        )
    )]
    #[case::an_at_owner(
        "https://at.example/@acme.example/fleet",
        link("https://at.example/@acme.example/fleet/tree/main/shop", "fleet/shop")
    )]
    #[case::an_at_owner_over_ssh(
        "git@at.example:@acme.example/fleet",
        link("https://at.example/@acme.example/fleet/tree/main/shop", "fleet/shop")
    )]
    fn links_a_directory_under_a_marked_owner(#[case] url: &str, #[case] expected: Place) {
        let (url, reference) = on_main(url);
        assert_eq!(place(&url, &reference, "./shop", &owner_marked()), expected);
    }
}
