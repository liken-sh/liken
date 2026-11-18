//! The namespace page draws each band of the cluster map folded to one
//! line, with the band's first few namespace names, and draws the
//! machines as one row of chips.

use std::collections::BTreeSet;

use super::super::ClusterMap;
use super::sentence;
use transect_ground::machines::Machine;

/// How many of a folded band's namespaces it names before "N more".
const FIRST_NAMES: usize = 3;

/// One band folded to a line.  The open namespace's own band lists its
/// other namespaces on that line, above the open box.
#[derive(Debug, PartialEq)]
pub struct Folded {
    pub name: String,
    pub open: bool,
    /// The band's first namespaces by name, then any other namespace
    /// that is in a loop with the open one.  Never the open namespace.
    pub names: Vec<Named>,
    pub more: usize,
}

/// One namespace a folded band names.
#[derive(Debug, PartialEq)]
pub struct Named {
    pub name: String,
    /// Whether it is in a loop with the open namespace, which the page
    /// marks with a tag.
    pub looped: bool,
}

impl Folded {
    /// The text after the band's names: "12 more", or a note that the
    /// band is empty when it lists no names.
    pub fn tail(&self) -> Option<String> {
        match (self.names.is_empty(), self.more) {
            (true, 0) if !self.open => Some(sentence::EMPTY_BAND.into()),
            (_, 0) => None,
            (_, more) => Some(sentence::more(more)),
        }
    }
}

/// Folds each band of `map`.  The map already sorts each band's
/// namespaces by name.  `looped` holds the namespaces in a loop with the
/// open one, and each of them is listed wherever it falls in its band,
/// so the page shows every other member of the loop.
pub fn fold(map: &ClusterMap, open: &str, looped: &BTreeSet<String>) -> Vec<Folded> {
    map.bands
        .iter()
        .map(|band| {
            let others: Vec<&str> = band
                .tiles
                .iter()
                .map(|tile| tile.namespace.as_str())
                .filter(|&namespace| namespace != open)
                .collect();
            let (first, rest) = others.split_at(others.len().min(FIRST_NAMES));
            let looped_rest = rest.iter().filter(|&&name| looped.contains(name));
            let names: Vec<Named> = first
                .iter()
                .chain(looped_rest)
                .map(|&name| Named {
                    name: name.into(),
                    looped: looped.contains(name),
                })
                .collect();
            Folded {
                name: band.name.clone(),
                open: others.len() < band.tiles.len(),
                more: others.len() - names.len(),
                names,
            }
        })
        .collect()
}

/// One machine in the folded machines band.
#[derive(Debug, PartialEq)]
pub struct MachineChip {
    pub name: String,
    /// How many of the open namespace's pods run there.
    pub pods: usize,
    pub control_plane: bool,
}

/// A chip for each machine.  The machines that run the namespace's
/// pods come first.  Within each group, control-plane machines come
/// first, then the rest by name.  `Machine::here` gives the count of the
/// namespace's pods on each machine.
pub fn machine_chips(machines: &[Machine]) -> Vec<MachineChip> {
    let mut chips: Vec<(bool, MachineChip)> = machines
        .iter()
        .map(|machine| {
            let chip = MachineChip {
                name: machine.name.clone(),
                pods: machine.here.unwrap_or(0),
                control_plane: machine.control_plane,
            };
            (!machine.control_plane, chip)
        })
        .collect();
    chips.sort_by(|(a_worker, a), (b_worker, b)| {
        (a.pods == 0)
            .cmp(&(b.pods == 0))
            .then(a_worker.cmp(b_worker))
            .then_with(|| a.name.cmp(&b.name))
    });
    chips.into_iter().map(|(_, chip)| chip).collect()
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::super::cluster::{Band, NamespaceTile};
    use super::*;
    use transect_ground::machines::Resources;
    use transect_observe::health::Health;
    use transect_observe::usage::Used;

    fn band(name: &str, namespaces: &[&str]) -> Band {
        Band {
            name: name.into(),
            tiles: namespaces
                .iter()
                .map(|&namespace| NamespaceTile {
                    namespace: namespace.into(),
                    workloads: 1,
                    health: Health::default(),
                    selected: false,
                    relations: Vec::new(),
                    looped: false,
                    loss: None,
                })
                .collect(),
        }
    }

    /// A folded band whose names are tagged when they start with "⟲".
    fn folded(name: &str, open: bool, names: &[&str], more: usize) -> Folded {
        let named = |name: &str| match name.strip_prefix('⟲') {
            Some(name) => Named {
                name: name.into(),
                looped: true,
            },
            None => Named {
                name: name.into(),
                looped: false,
            },
        };
        Folded {
            name: name.into(),
            open,
            names: names.iter().map(|&name| named(name)).collect(),
            more,
        }
    }

    #[rstest]
    fn names_the_first_of_each_band_and_counts_the_rest() {
        let map = ClusterMap {
            bands: vec![
                band("apps", &["shop", "blog", "wiki", "chat", "mail"]),
                band("platform", &["dns", "proxy"]),
                band("empty", &[]),
            ],
        };
        assert_eq!(
            fold(&map, "blog", &BTreeSet::new()),
            vec![
                folded("apps", true, &["shop", "wiki", "chat"], 1),
                folded("platform", false, &["dns", "proxy"], 0),
                folded("empty", false, &[], 0),
            ]
        );
    }

    #[rstest]
    fn names_each_namespace_in_a_loop_with_the_open_one() {
        let map = ClusterMap {
            bands: vec![
                band("apps", &["shop", "blog", "wiki", "chat", "forge"]),
                band("platform", &["dns", "proxy", "cache", "mail", "deploy"]),
            ],
        };
        let looped = BTreeSet::from(["deploy".to_string(), "proxy".to_string()]);
        assert_eq!(
            fold(&map, "forge", &looped),
            vec![
                folded("apps", true, &["shop", "blog", "wiki"], 1),
                folded("platform", false, &["dns", "⟲proxy", "cache", "⟲deploy"], 1),
            ]
        );
    }

    #[rstest]
    #[case::more(folded("apps", false, &["shop"], 4), Some("4 more"))]
    #[case::all_named(folded("apps", false, &["shop"], 0), None)]
    #[case::empty(folded("apps", false, &[], 0), Some("no namespaces"))]
    #[case::open_and_alone(folded("apps", true, &[], 0), None)]
    fn the_tail_of_a_folded_line(#[case] band: Folded, #[case] tail: Option<&str>) {
        assert_eq!(band.tail().as_deref(), tail);
    }

    fn machine(name: &str, control_plane: bool, here: usize) -> Machine {
        Machine {
            name: name.into(),
            control_plane,
            etcd: false,
            ready: true,
            allocatable: Resources::default(),
            requested: Resources::default(),
            used: Used::default(),
            pods: 0,
            here: Some(here),
            lost: false,
            really_lost: false,
            lost_minutes: None,
            held: vec![],
            pod_memory: vec![],
            alerted: None,
        }
    }

    #[rstest]
    fn machines_running_the_namespace_come_first() {
        let machines = [
            machine("alpha", true, 0),
            machine("beta", true, 2),
            machine("delta", false, 0),
            machine("gamma", false, 1),
        ];
        let chips = machine_chips(&machines);
        let order: Vec<(&str, usize)> = chips
            .iter()
            .map(|chip| (chip.name.as_str(), chip.pods))
            .collect();
        assert_eq!(
            order,
            vec![("beta", 2), ("gamma", 1), ("alpha", 0), ("delta", 0)]
        );
    }
}
