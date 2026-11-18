//! The workload card: the workload's kind and name, how many of its
//! pods are ready, what it runs and when it last rolled, its health, and
//! the findings about it.

use k8s_openapi::jiff::Timestamp;

use crate::map::known::Chip;
use transect_cluster::ownership::Managed;
use transect_cluster::workloads::Workload;
use transect_graph::edges::{Provenance, provenance};
use transect_ground::loss::IfDown;
use transect_network::addresses::Depends;
use transect_network::policy::Calls;
use transect_observe::health::{Health, Level, Reason, minutes_between};
use transect_resources::intent::{Docs, Line, ObjectIntent};
use transect_resources::knowledge::KnownThrough;
use transect_supply::findings::WorkloadFinding;
use transect_supply::gitops::CodeSection;
use transect_supply::images::{Origin, Runs};
use transect_supply::vulnerabilities::Counts;

#[derive(Debug, PartialEq)]
pub struct WorkloadCard {
    pub kind: String,
    pub name: String,
    /// The workload's page, which the card's title links to, or none on
    /// that page itself.
    pub href: Option<String>,
    /// What the workload is, as the card's first line: the `why` of its
    /// intent, else its kind's knowledge summary, else the description
    /// from its CRD, else the description of its first image.
    pub why: Option<Line>,
    /// What commonly goes wrong with it, from its intent.
    pub gotcha: Option<Line>,
    /// What the workload provides to the cluster, from the `provides`
    /// of its intent.  The "If it goes down" lines name this in place of
    /// the workload.
    pub provides: Option<Line>,
    pub docs: Option<Docs>,
    /// The workload's health, the same as its box on the namespace page
    /// shows.
    pub health: Health,
    /// For each of the health's reasons in order, the page it links to,
    /// or `None` for a reason about the workload itself.
    pub reason_pages: Vec<Option<String>>,
    /// How many of its pods are ready, and how many it should run.
    pub ready: Option<(i32, i32)>,
    /// Set for a custom workload with no controller under it, when none
    /// of its pods is running.
    pub idle: Option<Idle>,
    pub images: Vec<String>,
    /// Where each image that the pods run came from, paired with the
    /// image name in the pod spec.  An image has one entry for each
    /// digest that its pods run.
    pub origins: Vec<(String, Origin)>,
    /// Minutes since its latest rollout.
    pub rolled: Option<i64>,
    pub findings: Vec<WorkloadFinding>,
    /// What its configured addresses point at, and what points at it.
    pub depends: Depends,
    /// Where it is declared, or the reason the card does not show it.
    pub code: CodeSection,
    /// The controllers of its custom resources, and what it controls.
    pub managed: Managed,
    /// Who its policies let call it, and on which ports.
    pub calls: Calls,
    /// What the workload's dependents lose if it goes down.
    pub if_down: IfDown,
    /// Its kind's knowledge, shown at the end of the card.
    pub known: Option<KnownThrough>,
    /// The conditions reported by a custom workload whose kind has no
    /// knowledge.  A kind with knowledge shows them on its board.
    pub conditions: Vec<Chip>,
    /// A link to its access tab, for a workload that serves a port.
    pub access: Option<CardAccess>,
    /// The count line of its current vulnerability reports, and the link
    /// to its vulnerabilities tab.
    pub vulnerabilities: Option<(Option<Counts>, String)>,
}

/// A card's link to the access lens: the number of ways in to the
/// workload, and the link to its access tab.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct CardAccess {
    pub ways: usize,
    pub href: String,
}

impl CardAccess {
    pub fn ways_in(&self) -> String {
        match self.ways {
            0 => "no way in".into(),
            1 => "1 way in".into(),
            ways => format!("{ways} ways in"),
        }
    }
}

/// How a custom workload with no controller and no running pod last
/// ran, such as a workload that runs `Job`s, between runs.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Idle {
    /// No pod of the workload has finished.
    Unrun,
    /// Its newest pod finished or failed this many minutes ago.
    Ended { failed: bool, minutes: i64 },
}

impl WorkloadCard {
    pub fn build(
        workload: &Workload,
        health: Health,
        findings: Vec<WorkloadFinding>,
        depends: Depends,
        now: Timestamp,
    ) -> Self {
        Self {
            kind: workload.object.kind.clone(),
            name: workload.object.name.clone(),
            href: None,
            why: None,
            gotcha: None,
            provides: None,
            docs: None,
            health,
            reason_pages: Vec::new(),
            ready: workload.ready(),
            idle: workload.idle().map(|run| match run {
                Some(run) => Idle::Ended {
                    failed: run.failed,
                    minutes: minutes_between(run.at, now),
                },
                None => Idle::Unrun,
            }),
            images: workload.images(),
            origins: Vec::new(),
            rolled: workload.rolled().map(|rolled| minutes_between(rolled, now)),
            findings,
            depends,
            code: CodeSection::default(),
            managed: Default::default(),
            calls: Calls::default(),
            if_down: IfDown::default(),
            known: None,
            conditions: Vec::new(),
            access: None,
            vulnerabilities: None,
        }
    }

    /// Links each reason to the page that `page` returns for the reason's
    /// subject.
    pub fn with_reason_pages(self, page: impl Fn(&Reason) -> Option<String>) -> Self {
        let reason_pages = self.health.reasons.iter().map(page).collect();
        Self {
            reason_pages,
            ..self
        }
    }

    /// Each of the health's reasons with the page its line links to.
    pub fn reason_lines(&self) -> Vec<(&Reason, Option<&str>)> {
        let pages = self.reason_pages.iter().map(Option::as_deref);
        self.health
            .reasons
            .iter()
            .zip(pages.chain(std::iter::repeat(None)))
            .collect()
    }

    pub fn with_conditions(self, conditions: Vec<Chip>) -> Self {
        Self { conditions, ..self }
    }

    /// Sets the `KindKnowledge` of the workload's kind. Its summary opens
    /// the card when the intent has no `why`, so call this before
    /// `with_intent`.
    pub fn with_known(self, known: Option<KnownThrough>) -> Self {
        Self { known, ..self }
    }

    /// Sets the access tab from `ways`, the number of ways in to the
    /// workload, which is `None` for a workload that serves no port.
    /// The tab needs the card's `href`, so call it after `with_href`.
    pub fn with_access(self, ways: Option<usize>) -> Self {
        let access = ways.zip(self.href.clone()).map(|(ways, page)| CardAccess {
            ways,
            href: format!("{page}/access"),
        });
        Self { access, ..self }
    }

    /// Sets the vulnerabilities tab and the current count line.
    /// The tab is set only when `named` is true and the card has an
    /// `href`, so call it after `with_href`.
    pub fn with_vulnerabilities(self, named: bool, counts: Option<Counts>) -> Self {
        let tab = self.href.clone().filter(|_| named);
        let vulnerabilities = tab.map(|page| (counts, format!("{page}/vulnerabilities")));
        Self {
            vulnerabilities,
            ..self
        }
    }

    pub fn with_href(self, href: Option<String>) -> Self {
        Self { href, ..self }
    }

    pub fn with_if_down(self, if_down: IfDown) -> Self {
        Self { if_down, ..self }
    }

    pub fn with_calls(self, calls: Calls) -> Self {
        Self { calls, ..self }
    }

    pub fn with_managed(self, managed: Managed) -> Self {
        Self { managed, ..self }
    }

    /// Marks each directory of `code` whose repository is the source
    /// label of one of the card's images, so call it after
    /// `with_origins`.
    pub fn with_code(self, code: CodeSection) -> Self {
        let sources = self.origins.iter().filter_map(|(_, origin)| match origin {
            Origin::Read(built) => built.source.as_deref(),
            _ => None,
        });
        let code = code.with_image_sources(sources);
        Self { code, ..self }
    }

    pub fn with_origins(self, origins: Vec<(String, Origin)>) -> Self {
        Self { origins, ..self }
    }

    /// Sets the intent's lines, and sets `why` from the first source
    /// that has one, in the order that `why` lists them.  `defined` is
    /// the description from the kind's CRD.  Call it after `with_known`
    /// and `with_origins`.  A knowledge summary that opens the card is
    /// left out of the knowledge section at the end of the card.
    pub fn with_intent(self, intent: Option<&ObjectIntent>, defined: Option<Line>) -> Self {
        let intent = intent.cloned().unwrap_or_default();
        let summary = self.known.as_ref().and_then(KnownThrough::opening);
        let (why, known) = match (intent.why, summary) {
            (Some(why), _) => (Some(why), self.known.clone()),
            (None, Some(summary)) => (
                Some(summary),
                self.known.clone().map(KnownThrough::without_summary),
            ),
            (None, None) => (
                defined.or_else(|| self.described_by_image()),
                self.known.clone(),
            ),
        };
        Self {
            why,
            known,
            gotcha: intent.gotcha,
            provides: intent.provides,
            docs: intent.docs,
            ..self
        }
    }

    /// The description of the first image, as the card's opening line.
    fn described_by_image(&self) -> Option<Line> {
        let image = self.images.first()?;
        let origins = self.origins_of(image).into_iter();
        let text = origins.filter_map(Origin::description).next()?;
        Some(Line {
            text: text.into(),
            provenance: Provenance::Image(Some(image.clone())),
        })
    }

    /// Whether the origin of `image` shows the image's description.  It
    /// does not when the card's opening line is that description.
    pub fn describes(&self, image: &str) -> bool {
        let opening = self.why.as_ref().map(|why| &why.provenance);
        opening != Some(&Provenance::Image(Some(image.into())))
    }

    /// The Runs box its page draws in the main column.
    pub fn runs(&self) -> Runs {
        Runs::of(&self.images, &self.origins, self.rolled_ago(), |image| {
            self.describes(image)
        })
    }

    /// Where `image` came from, once per digest its pods run.
    pub fn origins_of(&self, image: &str) -> Vec<&Origin> {
        let of_image = self.origins.iter().filter(|(name, _)| name == image);
        of_image.map(|(_, origin)| origin).collect()
    }

    /// What the card's marked lines mean, when any line is marked.
    pub fn legend(&self) -> Option<String> {
        let lines = [
            self.why.as_ref().map(|line| &line.provenance),
            self.gotcha.as_ref().map(|line| &line.provenance),
            self.provides.as_ref().map(|line| &line.provenance),
            self.docs.as_ref().map(|docs| &docs.provenance),
        ];
        let lines = lines.into_iter().flatten().cloned();
        let if_down = self.if_down.hops.iter().flat_map(|hop| hop.provenances());
        let lines = lines.chain(if_down.cloned());
        let lines = lines.chain(self.depends.provenances().cloned());
        let lines = lines.chain(self.managed.provenances().cloned());
        let known = self.known.iter().flat_map(|known| known.provenances());
        let lines = lines.chain(known.cloned());
        let provenances: Vec<Provenance> = lines.collect();
        provenance::legend(&provenances)
    }

    /// The CSS class of the health dot, the same class that the
    /// namespace card uses.
    pub fn dot_class(&self) -> &'static str {
        match self.health.level {
            Level::Healthy => "dot-clear",
            Level::Notice => "concern-notice",
            Level::Warning => "concern-warning",
            Level::Failing => "concern-failing",
        }
    }
}

#[cfg(test)]
pub mod fixtures {
    use super::*;

    pub fn workload_card(ready: Option<(i32, i32)>, rolled: Option<i64>) -> WorkloadCard {
        WorkloadCard {
            kind: "Deployment".into(),
            name: "web".into(),
            href: None,
            why: None,
            gotcha: None,
            provides: None,
            docs: None,
            health: Health::default(),
            reason_pages: Vec::new(),
            ready,
            idle: None,
            images: Vec::new(),
            origins: Vec::new(),
            rolled,
            findings: Vec::new(),
            depends: Default::default(),
            code: Default::default(),
            managed: Default::default(),
            calls: Default::default(),
            if_down: Default::default(),
            known: None,
            conditions: Vec::new(),
            access: None,
            vulnerabilities: None,
        }
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::fixtures::workload_card;
    use super::*;
    use transect_supply::images::Built;

    /// The card of a workload running `images`, each described as
    /// "<image> does things".
    fn running(images: &[&str]) -> WorkloadCard {
        let origins = images.iter().map(|image| {
            let built = Built {
                commit: None,
                source: None,
                version: None,
                description: Some(format!("{image} does things")),
                documentation: None,
            };
            (image.to_string(), Origin::Read(built))
        });
        WorkloadCard {
            images: images.iter().map(|image| image.to_string()).collect(),
            origins: origins.collect(),
            ..workload_card(None, None)
        }
    }

    fn intent_with_why(why: &str) -> ObjectIntent {
        ObjectIntent {
            why: Some(Line {
                text: why.into(),
                provenance: Provenance::Intent(transect_graph::edges::Stated::Annotation {
                    field: "why".into(),
                    on: "Deployment web".into(),
                }),
            }),
            ..ObjectIntent::default()
        }
    }

    #[rstest]
    #[case::its_why(&["web:1", "proxy:2"], Some(intent_with_why("Sells things.")), Some("Sells things."), true)]
    #[case::its_first_images_description(&["web:1", "proxy:2"], None, Some("web:1 does things"), false)]
    #[case::nothing(&[], None, None, true)]
    fn opens_with_its_why_or_its_first_images_description(
        #[case] images: &[&str],
        #[case] intent: Option<ObjectIntent>,
        #[case] opening: Option<&str>,
        #[case] first_image_describes: bool,
    ) {
        let card = running(images).with_intent(intent.as_ref(), None);
        let why = card.why.as_ref().map(|line| line.text.as_str());
        assert_eq!(
            (why, card.describes("web:1"), card.describes("proxy:2")),
            (opening, first_image_describes, true)
        );
    }

    fn widget() -> KnownThrough {
        let knowledge = transect_resources::knowledge::fixtures::knowledge(
            "widgets.widgets.example.com",
            "{summary: Turns things.}",
        );
        KnownThrough::of(&knowledge, None)
    }

    #[rstest]
    #[case::its_why_over_the_summary(
        Some(intent_with_why("Sells things.")),
        "Sells things.",
        Some("Turns things.")
    )]
    #[case::the_summary_over_the_image(None, "Turns things.", None)]
    fn opens_with_its_kinds_summary_when_it_has_no_why(
        #[case] intent: Option<ObjectIntent>,
        #[case] opening: &str,
        #[case] summary_at_the_end: Option<&str>,
    ) {
        let card = running(&["web:1"])
            .with_known(Some(widget()))
            .with_intent(intent.as_ref(), None);
        let why = card.why.map(|line| line.text);
        let summary = card.known.and_then(|known| known.summary);
        assert_eq!(
            (why.as_deref(), summary.as_deref()),
            (Some(opening), summary_at_the_end)
        );
    }

    fn defined() -> Line {
        Line {
            text: "Keeps widgets.".into(),
            provenance: Provenance::Definition {
                crd: "widgets.widgets.example.com".into(),
            },
        }
    }

    #[rstest]
    #[case::the_summary_over_its_crd(Some(widget()), Some(defined()), "Turns things.")]
    #[case::its_crd_over_the_image(None, Some(defined()), "Keeps widgets.")]
    #[case::the_image_without_a_crd(None, None, "web:1 does things")]
    fn opens_with_what_its_crd_says_when_knowledge_says_nothing(
        #[case] known: Option<KnownThrough>,
        #[case] defined: Option<Line>,
        #[case] opening: &str,
    ) {
        let card = running(&["web:1"])
            .with_known(known)
            .with_intent(None, defined);
        assert_eq!(card.why.map(|line| line.text).as_deref(), Some(opening));
    }

    #[rstest]
    #[case::an_image_only(None, "image: from an image's labels.")]
    #[case::intent_and_not_the_runs_boxs_image(
        Some(intent_with_why("Sells things.")),
        "intent: written by the people who run the cluster. Hover"
    )]
    fn its_legend_names_the_marks_it_shows(
        #[case] intent: Option<ObjectIntent>,
        #[case] meanings: &str,
    ) {
        let card = running(&["web:1"]).with_intent(intent.as_ref(), None);
        let legend = card.legend().unwrap();
        assert!(legend.contains(meanings), "{legend}");
    }

    #[rstest]
    #[case::none_reaches_it(Some(0), Some("no way in"))]
    #[case::one(Some(1), Some("1 way in"))]
    #[case::several(Some(3), Some("3 ways in"))]
    #[case::it_serves_no_port(None, None)]
    fn leads_to_its_access_tab_with_its_ways_in(
        #[case] ways: Option<usize>,
        #[case] words: Option<&str>,
    ) {
        let page = Some("/namespaces/shop/deployments/web".to_string());
        let card = workload_card(None, None).with_href(page).with_access(ways);
        let shown = card.access.map(|a| (a.ways_in(), a.href));
        let href = "/namespaces/shop/deployments/web/access".to_string();
        assert_eq!(shown, words.map(|w| (w.to_string(), href)));
    }
}
