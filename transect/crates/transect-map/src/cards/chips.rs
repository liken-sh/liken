//! The namespace card's grid of chips, one per workload or group of
//! workloads, in the order a reader scans them.

use std::collections::HashMap;

use transect_cluster::ownership;
use transect_graph::edges::Object;
use transect_graph::links::Part;
use transect_observe::health::Level;

/// One chip: what it names, and the worst health among the workloads
/// that it names.
#[derive(Debug, PartialEq)]
pub struct Chip {
    pub label: Label,
    pub level: Level,
    /// What the workloads it names are, from their `why`.
    pub title: Option<String>,
    /// The page of each workload it names, in the order it names them.
    pub hrefs: Vec<Option<String>>,
}

#[derive(Debug, PartialEq)]
pub enum Label {
    /// A workload that runs some number of pods other than one.
    Replicated { name: String, replicas: i32 },
    /// One or two workloads of one pod each.  Pairing them keeps a
    /// namespace of many small helpers from filling the card.
    Single(Vec<String>),
    /// Every CronJob as one count, because a namespace's scheduled jobs
    /// are many small names, and each name tells the reader little.
    CronJobs(usize),
    /// Every Job that no CronJob or other owner started, as one count,
    /// for the same reason.
    Jobs(usize),
    /// A workload of any other kind, named by its kind, such as a cache
    /// an operator runs.  `custom` marks a custom kind.
    Owned {
        kind: String,
        name: String,
        custom: bool,
    },
}

/// The key that looks up the `why` of a chip whose kind is not one of
/// the built-in workloads: its kind and name, as "Player lobby".
pub fn owned_key(kind: &str, name: &str) -> String {
    format!("{kind} {name}")
}

impl Chip {
    /// The chip with its title: the `why` of each workload it names,
    /// as `whys` gives them by name, prefixed by the name when the chip
    /// names two.  An operator often gives its objects of several kinds
    /// the same name, so a chip of another kind looks its workload up by
    /// kind and name, as "Player lobby".  A count of CronJobs or Jobs
    /// names no workload.
    pub fn titled(self, whys: &HashMap<String, String>) -> Self {
        let names: Vec<String> = match &self.label {
            Label::Replicated { name, .. } => vec![name.clone()],
            Label::Owned { kind, name, .. } => vec![owned_key(kind, name)],
            Label::Single(names) => names.clone(),
            Label::CronJobs(_) | Label::Jobs(_) => Vec::new(),
        };
        let said: Vec<String> = match &names[..] {
            [name] => whys.get(name).cloned().into_iter().collect(),
            _ => names
                .iter()
                .filter_map(|name| Some(format!("{name}: {}", whys.get(name)?)))
                .collect(),
        };
        Self {
            title: (!said.is_empty()).then(|| said.join("\n")),
            ..self
        }
    }

    /// What the chip shows, one name at a time.  Each workload of a pair
    /// opens its own page, and a count opens no page.
    pub fn parts(&self) -> Vec<Part> {
        let href = |at: usize| self.hrefs.get(at).cloned().flatten();
        match &self.label {
            Label::Single(names) => names
                .iter()
                .enumerate()
                .map(|(at, name)| Part {
                    text: name.clone(),
                    href: href(at),
                })
                .collect(),
            _ => vec![Part {
                text: self.to_string(),
                href: href(0),
            }],
        }
    }

    /// The chip's classes.  A chip whose words run past half the card's
    /// width spans the card's width, and a chip whose workload needs
    /// attention takes --warm, as its parcel would.
    pub fn class(&self) -> String {
        let mut class = String::from("chip");
        if self.to_string().chars().count() > WIDE {
            class.push_str(" chip-wide");
        }
        match self.level {
            Level::Healthy | Level::Notice => {}
            Level::Warning => class.push_str(" chip-warning"),
            Level::Failing => class.push_str(" chip-failing"),
        }
        class
    }
}

/// The most characters that fit in a chip half the card's width.
const WIDE: usize = 18;

/// A namespace's workloads, sorted into the chips they draw as.
#[derive(Default)]
pub(super) struct Sorted {
    /// The built-in workloads that run pods, each a name, its replicas,
    /// its health, and its page.
    replicated: Vec<(String, i32, Level, Option<String>)>,
    cron_jobs: Vec<Level>,
    jobs: Vec<Level>,
    owned: Vec<Chip>,
}

impl Sorted {
    /// Adds `workload`, with its replicas when it is a built-in kind that
    /// has them, and its page.
    pub(super) fn add(
        &mut self,
        workload: &Object,
        replicas: Option<i32>,
        level: Level,
        href: Option<String>,
    ) {
        let built_in = !ownership::custom(workload);
        match (workload.kind.as_str(), replicas) {
            ("CronJob", _) if built_in => self.cron_jobs.push(level),
            ("Job", _) if built_in => self.jobs.push(level),
            (_, Some(replicas)) if built_in => {
                self.replicated
                    .push((workload.name.clone(), replicas, level, href));
            }
            (kind, _) => self.owned.push(Chip {
                label: Label::Owned {
                    kind: kind.into(),
                    name: workload.name.clone(),
                    custom: !built_in,
                },
                level,
                title: None,
                hrefs: vec![href],
            }),
        }
    }

    /// The replicated workloads come first, most replicas first, because
    /// they run most of the namespace's pods.  The single-pod workloads
    /// follow by name, two to a chip, then the counts of CronJobs and
    /// Jobs, and then every other kind, by kind and name.
    pub(super) fn chips(mut self) -> Vec<Chip> {
        self.replicated
            .sort_by(|(a, a_replicas, ..), (b, b_replicas, ..)| {
                b_replicas.cmp(a_replicas).then_with(|| a.cmp(b))
            });
        let (singles, replicated): (Vec<_>, Vec<_>) = self
            .replicated
            .into_iter()
            .partition(|(_, replicas, ..)| *replicas == 1);
        let mut chips: Vec<Chip> = replicated
            .into_iter()
            .map(|(name, replicas, level, href)| Chip {
                label: Label::Replicated { name, replicas },
                level,
                title: None,
                hrefs: vec![href],
            })
            .collect();
        chips.extend(singles.chunks(2).map(|pair| {
            Chip {
                label: Label::Single(pair.iter().map(|(name, ..)| name.clone()).collect()),
                level: pair
                    .iter()
                    .map(|(_, _, level, _)| *level)
                    .max()
                    .unwrap_or_default(),
                title: None,
                hrefs: pair.iter().map(|(.., href)| href.clone()).collect(),
            }
        }));
        for (levels, label) in [
            (&self.cron_jobs, Label::CronJobs(self.cron_jobs.len())),
            (&self.jobs, Label::Jobs(self.jobs.len())),
        ] {
            if let Some(&level) = levels.iter().max() {
                chips.push(Chip {
                    label,
                    level,
                    title: None,
                    hrefs: Vec::new(),
                });
            }
        }
        self.owned.sort_by_key(|chip| chip.to_string());
        chips.extend(self.owned);
        chips
    }
}

#[cfg(test)]
pub mod fixtures {
    use super::*;

    /// A chip of `label` whose names have no page.
    pub fn chip(label: Label) -> Chip {
        let hrefs = match &label {
            Label::Single(names) => vec![None; names.len()],
            Label::CronJobs(_) | Label::Jobs(_) => Vec::new(),
            Label::Replicated { .. } | Label::Owned { .. } => vec![None],
        };
        Chip {
            label,
            level: Level::Healthy,
            title: None,
            hrefs,
        }
    }

    pub fn replicated(name: &str, replicas: i32) -> Chip {
        chip(Label::Replicated {
            name: name.into(),
            replicas,
        })
    }

    pub fn single(names: &[&str]) -> Chip {
        chip(Label::Single(
            names.iter().map(|&name| name.into()).collect(),
        ))
    }

    pub fn owned(kind: &str, name: &str, custom: bool) -> Chip {
        chip(Label::Owned {
            kind: kind.into(),
            name: name.into(),
            custom,
        })
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::fixtures::*;
    use super::*;

    /// A workload to add: its group, kind, name, replicas, and level.
    type Row = (&'static str, &'static str, &'static str, Option<i32>, Level);

    fn deployment(name: &'static str, replicas: i32) -> Row {
        ("apps", "Deployment", name, Some(replicas), Level::Healthy)
    }

    fn sorted(rows: &[Row]) -> Vec<Chip> {
        let mut sorted = Sorted::default();
        for &(group, kind, name, replicas, level) in rows {
            sorted.add(
                &Object::new(group, kind, Some("shop"), name),
                replicas,
                level,
                None,
            );
        }
        sorted.chips()
    }

    fn at(level: Level, chip: Chip) -> Chip {
        Chip { level, ..chip }
    }

    #[rstest]
    #[case::nothing(&[], vec![])]
    #[case::replicated_by_replicas_then_name(
        &[deployment("worker", 2), deployment("web", 3), deployment("api", 2)],
        vec![replicated("web", 3), replicated("api", 2), replicated("worker", 2)],
    )]
    #[case::singles_pair_by_name(
        &[deployment("cache", 1), deployment("auth", 1), deployment("mail", 1)],
        vec![single(&["auth", "cache"]), single(&["mail"])],
    )]
    #[case::scaled_to_zero_is_not_single(&[deployment("batch", 0)], vec![replicated("batch", 0)])]
    #[case::cron_jobs_and_jobs_as_counts(
        &[
            ("batch", "Job", "migrate", None, Level::Healthy),
            ("batch", "CronJob", "report", None, Level::Healthy),
            deployment("web", 1),
            ("batch", "CronJob", "cleanup", None, Level::Healthy),
        ],
        vec![single(&["web"]), chip(Label::CronJobs(2)), chip(Label::Jobs(1))],
    )]
    #[case::other_kinds_last_by_kind_and_name(
        &[
            ("redis.example.io", "Redis", "redis", None, Level::Healthy),
            ("", "Pod", "debug", None, Level::Healthy),
            ("postgres.example.io", "Cluster", "postgres", None, Level::Healthy),
            deployment("web", 1),
        ],
        vec![
            single(&["web"]),
            owned("Cluster", "postgres", true),
            owned("Pod", "debug", false),
            owned("Redis", "redis", true),
        ],
    )]
    #[case::a_custom_kind_named_like_a_built_in_one(
        &[("jobs.example.io", "Job", "nightly", None, Level::Healthy)],
        vec![owned("Job", "nightly", true)],
    )]
    #[case::a_pair_takes_its_worst_level(
        &[deployment("auth", 1), ("apps", "Deployment", "cache", Some(1), Level::Warning)],
        vec![at(Level::Warning, single(&["auth", "cache"]))],
    )]
    #[case::a_count_takes_its_worst_level(
        &[
            ("batch", "CronJob", "report", None, Level::Failing),
            ("batch", "CronJob", "cleanup", None, Level::Healthy),
        ],
        vec![at(Level::Failing, chip(Label::CronJobs(2)))],
    )]
    fn orders_the_chips(#[case] rows: &[Row], #[case] expected: Vec<Chip>) {
        assert_eq!(sorted(rows), expected);
    }

    #[rstest]
    #[case::custom(owned("Cache", "sessions", true), "chip")]
    #[case::built_in(owned("Pod", "debug", false), "chip")]
    #[case::warning(at(Level::Warning, replicated("web", 3)), "chip chip-warning")]
    #[case::failing_custom(
        at(Level::Failing, owned("Cache", "sessions", true)),
        "chip chip-failing"
    )]
    #[case::wide(owned("Player", "lobby-speakers", true), "chip chip-wide")]
    fn a_chip_takes_its_width_and_health(#[case] chip: Chip, #[case] class: &str) {
        assert_eq!(chip.class(), class);
    }

    #[rstest]
    #[case::one_workload(replicated("web", 3), Some("Sells things."))]
    #[case::one_of_a_pair(single(&["auth", "web"]), Some("web: Sells things."))]
    #[case::both_of_a_pair(single(&["mail", "web"]), Some("mail: Sends mail.\nweb: Sells things."))]
    #[case::none_said(owned("Cache", "sessions", true), None)]
    #[case::another_kind_by_kind_and_name(owned("Cache", "web", true), Some("Keeps sessions."))]
    #[case::not_by_name_alone(owned("Queue", "web", true), None)]
    #[case::a_count(chip(Label::CronJobs(2)), None)]
    fn a_chip_is_titled_with_what_its_workloads_are(
        #[case] chip: Chip,
        #[case] title: Option<&str>,
    ) {
        let whys = HashMap::from([
            ("web".to_string(), "Sells things.".to_string()),
            ("mail".to_string(), "Sends mail.".to_string()),
            (owned_key("Cache", "web"), "Keeps sessions.".to_string()),
        ]);
        assert_eq!(chip.titled(&whys).title.as_deref(), title);
    }
}
