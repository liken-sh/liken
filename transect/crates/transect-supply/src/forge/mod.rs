//! Links from a card to a repository's pages on its forge: the directory
//! that a GitOps tool applies an object from, and the commit that an
//! image was built from.  Both start from the repository's clone URL.
//!
//! A clone URL does not say which forge serves it, and each forge lays
//! out its pages differently.  A guessed link that opens a 404 is worse
//! than no link, so transect links only when a `Forge` resource matches
//! the host, and shows any other repository as text.

mod commit;
mod layout;

use std::collections::HashMap;

pub use commit::{Commit, commit};
pub use layout::{Forge, Ref, parse};

#[cfg(any(test, feature = "fixtures"))]
pub use layout::fixtures;

/// The forges declared, and the hosts each serves.
#[derive(Debug, Default)]
pub struct Forges {
    layouts: Vec<Forge>,
    /// Lowercased `Ingress` hosts that route to a forge's image, mapped
    /// to that forge's index in `layouts`.
    served: HashMap<String, usize>,
}

impl Forges {
    /// Each entry of `hosts` is an `Ingress` host with the images of the
    /// workloads it routes to.  The host serves the forge of the first of
    /// those images that a layout lists.
    pub fn build(
        layouts: &[Forge],
        hosts: impl IntoIterator<Item = (String, Vec<String>)>,
    ) -> Self {
        let mut served = HashMap::new();
        for (host, images) in hosts {
            let found = images.iter().find_map(|image| of_image(layouts, image));
            if let Some(at) = found {
                served.insert(host.to_ascii_lowercase(), at);
            }
        }
        Self {
            layouts: layouts.to_vec(),
            served,
        }
    }

    /// The forge for `host`, trying in order a layout that lists the
    /// host, a layout that lists the host's first label, and a forge
    /// that the cluster runs at the host.
    pub fn of(&self, host: &str) -> Option<&Forge> {
        let host = host.to_ascii_lowercase();
        let named = self.layouts.iter().find(|f| f.hosts.contains(&host));
        let first = host.split('.').next().unwrap_or_default();
        let prefixed = || {
            let prefixed = self.layouts.iter();
            prefixed
                .into_iter()
                .find(|f| f.host_prefixes.iter().any(|p| p == first))
        };
        let served = || self.served.get(&host).and_then(|at| self.layouts.get(*at));
        named.or_else(prefixed).or_else(served)
    }
}

/// The index in `layouts` of the forge that an image runs, matched by
/// the last path segment of its repository, such as `gitea` in
/// `gitea/gitea:1.22`.  Cutting that segment at `:` removes only the
/// tag, because a registry's port is in the first segment.
fn of_image(layouts: &[Forge], image: &str) -> Option<usize> {
    let repository = image.split('@').next()?;
    let last = repository.rsplit('/').next()?.split(':').next()?;
    layouts
        .iter()
        .position(|forge| forge.images.iter().any(|name| name == last))
}

/// Whether `text` can go into a URL path unescaped: it has only
/// letters, digits, and `-._~/@`.  A forge may put `~` or `@` before an
/// owner, as in `/~owner/repo` or `/@owner/repo`.
pub fn safe(text: &str) -> bool {
    let allowed = |c: char| c.is_ascii_alphanumeric() || "-._~/@".contains(c);
    text.chars().all(allowed)
}

/// Whether `text` has a `..` path segment, which would make a link
/// leave the repository whose path it is part of.
pub fn steps_up(text: &str) -> bool {
    text.split('/').any(|part| part == "..")
}

/// The location of a repository's web pages: scheme, host, and the
/// repository's path on that host.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Web<'a> {
    pub scheme: &'static str,
    pub host: &'a str,
    pub repository: &'a str,
}

impl Web<'_> {
    /// `<scheme>://<host>/<repository>`.
    pub fn base(&self) -> String {
        format!("{}://{}/{}", self.scheme, self.host, self.repository)
    }

    /// The last segment of the repository path, which a card shows as
    /// its name.
    pub fn name(&self) -> &str {
        self.repository
            .rsplit('/')
            .next()
            .unwrap_or(self.repository)
    }

    /// Whether `other` is the same repository.  Host and repository
    /// names compare without regard to case.
    pub fn same(&self, other: &Web) -> bool {
        self.host.eq_ignore_ascii_case(other.host)
            && self.repository.eq_ignore_ascii_case(other.repository)
    }
}

/// The web pages of a clone URL.  An `http` or `https` URL keeps its
/// host and any port.  An `ssh://git@host:port/...` or
/// `git@host:owner/repo` URL becomes an HTTPS URL at the same host
/// without the port, because a forge's SSH port is not its web port.
/// A user before the host is dropped.  An `@` after the host belongs to
/// the repository path, as in `https://host/@owner/repo`.  It returns
/// `None` for a URL without both an owner and a repository, for a
/// scheme other than `http`, `https`, and `ssh`, such as `oci`, and for
/// a host or path with characters outside the allowed set.
pub fn web(url: &str) -> Option<Web<'_>> {
    let (scheme, rest, port) = match url.split_once("://") {
        Some(("https", rest)) => ("https", rest, true),
        Some(("http", rest)) => ("http", rest, true),
        Some(("ssh", rest)) => ("https", rest, false),
        Some(_) => return None,
        None => ("https", url, false),
    };
    let (authority, path) = match url.contains("://") {
        true => rest.split_once('/')?,
        false => rest.split_once(':')?,
    };
    let authority = authority
        .rsplit_once('@')
        .map_or(authority, |(_, host)| host);
    let host = match port {
        true => authority,
        false => authority.split(':').next()?,
    };
    let repository = path.trim_matches('/');
    let repository = repository.strip_suffix(".git").unwrap_or(repository);
    let parts = repository
        .split('/')
        .filter(|part| !part.is_empty())
        .count();
    let host_ok = !host.is_empty()
        && host
            .chars()
            .all(|c| c.is_ascii_alphanumeric() || "-.:".contains(c));
    (parts >= 2 && host_ok && safe(repository) && !steps_up(repository)).then_some(Web {
        scheme,
        host,
        repository,
    })
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::gitea("gitea/gitea:1.22.0", Some("gitea"))]
    #[case::forgejo_from_its_registry("codeberg.org/forgejo/forgejo:9", Some("gitea"))]
    #[case::a_registry_with_a_port("registry.example.com:5000/forgejo/forgejo:9", Some("gitea"))]
    #[case::gitlab("gitlab/gitlab-ce:17.0.0-ce.0", Some("gitlab"))]
    #[case::a_digest("gitea/gitea@sha256:0a1b", Some("gitea"))]
    #[case::a_forges_helper("gitea/act_runner:0.2", None)]
    #[case::something_else("postgres:16", None)]
    fn reads_a_forge_from_an_image(#[case] image: &str, #[case] forge: Option<&str>) {
        let layouts = fixtures::layouts();
        let found = of_image(&layouts, image).map(|at| layouts[at].name.as_str());
        assert_eq!(found, forge);
    }

    #[rstest]
    #[case::named("GitHub.com", Some("github"))]
    #[case::by_its_first_label("gitea.example.com", Some("gitea"))]
    #[case::served("code.example.com", Some("gitlab"))]
    #[case::unknown("wiki.example.com", None)]
    fn finds_the_forge_that_serves_a_host(#[case] host: &str, #[case] forge: Option<&str>) {
        let hosts = [(
            "code.example.com".to_string(),
            vec!["gitlab/gitlab-ce:17".to_string()],
        )];
        let forges = Forges::build(&fixtures::layouts(), hosts);
        assert_eq!(forges.of(host).map(|f| f.name.as_str()), forge);
    }

    #[rstest]
    #[case::https("https://github.com/acme/app", Some(("https", "github.com", "acme/app")))]
    #[case::a_port_kept("http://code.example.com:3000/acme/app.git", Some(("http", "code.example.com:3000", "acme/app")))]
    #[case::ssh_drops_its_port("ssh://git@code.example.com:2222/acme/app.git", Some(("https", "code.example.com", "acme/app")))]
    #[case::scp_style("git@github.com:acme/app.git", Some(("https", "github.com", "acme/app")))]
    #[case::no_owner("https://github.com/app", None)]
    #[case::oci("oci://registry.example/acme/app", None)]
    #[case::a_quote_in_the_host("https://exa\"mple.com/acme/app", None)]
    #[case::an_owner_after_an_at("https://forge.example/@owner.example/app", Some(("https", "forge.example", "@owner.example/app")))]
    #[case::an_owner_after_a_tilde("https://forge.example/~owner/app", Some(("https", "forge.example", "~owner/app")))]
    #[case::a_user_and_an_at_owner("https://git@forge.example/@owner/app", Some(("https", "forge.example", "@owner/app")))]
    #[case::scp_style_with_an_at_owner("git@forge.example:@owner/app", Some(("https", "forge.example", "@owner/app")))]
    #[case::a_path_that_steps_up("https://forge.example/acme/../app", None)]
    fn reads_a_repositorys_web_pages(
        #[case] url: &str,
        #[case] expected: Option<(&str, &str, &str)>,
    ) {
        let found = web(url).map(|web| (web.scheme, web.host, web.repository));
        assert_eq!(found, expected);
    }

    #[rstest]
    #[case::the_same_in_another_case(
        "https://GitHub.com/Acme/App",
        "git@github.com:acme/app.git",
        true
    )]
    #[case::another_repository(
        "https://github.com/acme/app",
        "https://github.com/acme/fleet",
        false
    )]
    fn compares_repositories(#[case] a: &str, #[case] b: &str, #[case] same: bool) {
        assert_eq!(web(a).unwrap().same(&web(b).unwrap()), same);
    }
}
