//! The "Code" section of a card: where the card's object is declared,
//! found through the graph's reconciles-from edges, or the reason that
//! the card cannot show it.

use super::{Code, GitOps, Off};
use crate::forge::{self, Forges};
use transect_graph::edges::{Endpoint, Graph, Object};

#[derive(Debug, Default, PartialEq)]
pub struct CodeSection {
    pub entries: Vec<Code>,
    /// Why `entries` is empty: the reason that reconciles-from is off, or
    /// that no GitOps applier's label or annotation is on the object.
    /// It is `None` whenever `entries` has an entry.
    pub note: Option<transect_resources::resources::OffLine>,
    /// The card's namespace.  The card does not print the namespace of an
    /// applier in this namespace.
    pub here: Option<String>,
}

impl CodeSection {
    /// Where `workload` is declared, with the release that installed it
    /// when a chart did.
    pub fn of_workload(
        graph: &Graph,
        gitops: &GitOps,
        forges: &Forges,
        workload: &Object,
        here: &str,
    ) -> Self {
        let section = Self::collect(graph, gitops, forges, [workload], here, |code| code);
        section.or_unmarked("no GitOps applier's label or annotation is on it")
    }

    /// The directories where `workloads` are declared, each listed once,
    /// in the order of the first workload that has it.  A namespace's
    /// card leaves out the releases, because each workload's card shows
    /// its own chart.
    pub fn of_namespace<'a>(
        graph: &Graph,
        gitops: &GitOps,
        forges: &Forges,
        workloads: impl IntoIterator<Item = &'a Object>,
        here: &str,
    ) -> Self {
        let without_release = |code: Code| Code {
            release: None,
            ..code
        };
        let section = Self::collect(graph, gitops, forges, workloads, here, without_release);
        section.or_unmarked("no GitOps applier's label or annotation is on its workloads")
    }

    /// Sets the note `unmarked` when reconciles-from is on and found no
    /// entries, so the card never shows an empty section.
    fn or_unmarked(self, unmarked: &str) -> Self {
        match (&self.note, self.entries.is_empty()) {
            (None, true) => Self {
                note: Some(transect_resources::resources::OffLine::plain(unmarked)),
                ..self
            },
            _ => self,
        }
    }

    fn collect<'a>(
        graph: &Graph,
        gitops: &GitOps,
        forges: &Forges,
        objects: impl IntoIterator<Item = &'a Object>,
        here: &str,
        shown: impl Fn(Code) -> Code,
    ) -> Self {
        let mut entries: Vec<Code> = Vec::new();
        if gitops.off.is_none() {
            for object in objects {
                let end = Endpoint::Object(object.clone());
                let found = Code::of(graph, gitops, forges, &end).map(&shown);
                if let Some(code) = found.filter(|code| !entries.contains(code)) {
                    entries.push(code);
                }
            }
        }
        Self {
            entries,
            note: gitops.off.as_ref().map(Off::note),
            here: Some(here.into()),
        }
    }

    pub fn here(&self) -> Option<&str> {
        self.here.as_deref()
    }

    /// Links each applier to the URL that `href` returns for it.
    pub fn linked(mut self, href: impl Fn(&Object) -> Option<String>) -> Self {
        for code in &mut self.entries {
            if let Some(release) = &mut code.release {
                release.href = href(&release.object);
            }
            if let Some(declared) = &mut code.declared {
                declared.href = href(&declared.applier);
            }
        }
        self
    }

    /// The section with each directory marked `built_here` when one of
    /// `sources` is the directory's repository.  `sources` are the
    /// source labels of the images that the workload runs.
    pub fn with_image_sources<'a>(mut self, sources: impl IntoIterator<Item = &'a str>) -> Self {
        let sources: Vec<forge::Web> = sources.into_iter().filter_map(forge::web).collect();
        for code in &mut self.entries {
            let Some(declared) = &mut code.declared else {
                continue;
            };
            let repository = declared.repository.as_deref().and_then(forge::web);
            declared.built_here = repository
                .is_some_and(|repository| sources.iter().any(|source| source.same(&repository)));
        }
        self
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::no_knowledge(GitOps::off(Off::NoKnowledge), Some(Off::NoKnowledge.note()))]
    #[case::on_and_unmarked(
        crate::gitops::fixtures::gitops(&[]),
        Some(transect_resources::resources::OffLine::plain(
            "no GitOps applier's label or annotation is on it"
        ))
    )]
    fn says_why_there_is_no_code(
        #[case] gitops: GitOps,
        #[case] note: Option<transect_resources::resources::OffLine>,
    ) {
        let web = Object::new("apps", "Deployment", Some("shop"), "web");
        let graph = Graph::default();
        let section = CodeSection::of_workload(
            &graph,
            &gitops,
            &Forges::build(&crate::forge::fixtures::layouts(), []),
            &web,
            "shop",
        );
        assert_eq!((section.entries.len(), section.note), (0, note));
    }

    fn declared_in(repository: &str) -> CodeSection {
        CodeSection {
            entries: vec![Code {
                release: None,
                declared: Some(super::super::code::Declared {
                    applier: super::super::fixtures::object("Tree", "ops", "shop"),
                    href: None,
                    ready: super::super::Ready::True,
                    place: None,
                    repository: Some(repository.into()),
                    built_here: false,
                }),
            }],
            ..CodeSection::default()
        }
    }

    #[rstest]
    #[case::the_same_repository("https://github.com/acme/app.git", &["https://github.com/acme/app"], true)]
    #[case::over_ssh("ssh://git@github.com/acme/app", &["https://github.com/Acme/App"], true)]
    #[case::another_repository("https://github.com/acme/fleet", &["https://github.com/acme/app"], false)]
    #[case::no_image_source("https://github.com/acme/app", &[], false)]
    fn marks_a_directory_in_the_repository_an_image_is_built_from(
        #[case] repository: &str,
        #[case] sources: &[&str],
        #[case] built_here: bool,
    ) {
        let section = declared_in(repository).with_image_sources(sources.iter().copied());
        let declared = section.entries[0].declared.as_ref().unwrap();
        assert_eq!(declared.built_here, built_here);
    }
}
