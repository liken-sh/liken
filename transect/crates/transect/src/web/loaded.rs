//! The resources read from the directory that `--resources` names,
//! sorted by kind to the domain crate that parses each kind. The sort
//! must know every domain crate, so it is in the binary. The
//! `resources` crate reads the directory and applies the overlays.

#[cfg(test)]
mod examples;

use transect_access::access::LoadBalancerController;
use transect_ground::devices::knowledge::DeviceKnowledge;
use transect_ground::outside::{self, Outside};
use transect_network::entry::IngressController;
use transect_observe::logs::LogSource;
use transect_observe::logs::patterns::{self, LogPattern};
use transect_observe::metrics::MetricsSource;
use transect_resources::intent::Intent;
use transect_resources::knowledge::{self, KindKnowledge};
use transect_resources::layers::{self, Layers};
use transect_resources::resources::{Condition, Directory, DocId, Sorts};
use transect_supply::forge::Forge;
use transect_supply::images::mirror::{self, RegistryMirror};

/// The parsed resources. `directory` has the condition of each file.
#[derive(Debug, Default)]
pub struct Loaded {
    pub directory: Directory,
    pub layers: Layers,
    pub intents: Vec<Intent>,
    pub kinds: Vec<KindKnowledge>,
    pub devices: Vec<DeviceKnowledge>,
    pub outsides: Vec<Outside>,
    pub mirrors: Vec<RegistryMirror>,
    pub log_source: Option<LogSource>,
    pub log_patterns: Vec<LogPattern>,
    pub metrics_source: Option<MetricsSource>,
    /// Each matches an `IngressClass` by its `spec.controller`.
    pub ingress_controllers: Vec<IngressController>,
    /// The URL layout of each forge, used to build links to code.
    pub forges: Vec<Forge>,
    /// How each load balancer's listeners identify the `Service` they
    /// serve.
    pub load_balancers: Vec<LoadBalancerController>,
}

impl Sorts for Loaded {
    fn directory(&mut self) -> &mut Directory {
        &mut self.directory
    }

    fn take(&mut self, doc: DocId, kind: &str, value: serde_yaml::Value) -> Option<Condition> {
        let condition = match kind {
            "Layers" => match layers::parse(value) {
                Ok(_) if self.layers.doc.is_some() => Condition::left_out(
                    "Duplicate",
                    "a cluster declares one Layers resource, and the one read first is used",
                ),
                Ok(list) => {
                    self.layers = Layers::new(list, doc);
                    Condition::loaded()
                }
                Err(message) => Condition::left_out("Invalid", message),
            },
            "Intent" => match Intent::parse(doc, value) {
                Ok(intent) => {
                    self.intents.push(intent);
                    Condition::loaded()
                }
                Err(message) => Condition::left_out("Invalid", message),
            },
            "KindKnowledge" => first(
                &mut self.kinds,
                knowledge::parse(doc, value),
                |a, b| a.name == b.name,
                |one| format!("{} is described twice", one.name),
            ),
            "DeviceKnowledge" => first(
                &mut self.devices,
                transect_ground::devices::knowledge::parse(doc, value),
                |a, b| a.driver == b.driver,
                |one| format!("the driver {} is described twice", one.driver),
            ),
            "Outside" => first(
                &mut self.outsides,
                outside::parse(doc, value),
                |a, b| a.name == b.name,
                |one| format!("{} is declared twice", one.name),
            ),
            "RegistryMirror" => first(
                &mut self.mirrors,
                mirror::parse(doc, value),
                |a, b| a.host == b.host,
                |one| format!("{} has a mirror already", one.host),
            ),
            "LogSource" => only(
                &mut self.log_source,
                transect_observe::logs::source::parse(doc, value),
                "log source",
            ),
            "MetricsSource" => only(
                &mut self.metrics_source,
                transect_observe::metrics::source::parse(doc, value),
                "metrics source",
            ),
            "IngressController" => first(
                &mut self.ingress_controllers,
                transect_network::entry::controller::parse(doc, value),
                |a, b| a.controller == b.controller,
                |one| format!("{} is declared twice", one.controller),
            ),
            "LoadBalancerController" => first(
                &mut self.load_balancers,
                transect_access::access::parse_load_balancer(doc, value),
                |a, b| a.name == b.name,
                |one| format!("{} is declared twice", one.name),
            ),
            "Forge" => first(
                &mut self.forges,
                transect_supply::forge::parse(doc, value),
                |a, b| a.name == b.name,
                |one| format!("{} is declared twice", one.name),
            ),
            "LogPattern" => first(
                &mut self.log_patterns,
                patterns::parse(doc, value),
                |a, b| a.name == b.name,
                |one| format!("{} is declared twice", one.name),
            ),
            _ => return None,
        };
        Some(condition)
    }
}

/// Adds `parsed` to `kept`. When `same` matches an entry already in
/// `kept`, the entry read first stays, and the condition has the
/// message from `twice`.
fn first<T>(
    kept: &mut Vec<T>,
    parsed: Result<T, String>,
    same: impl Fn(&T, &T) -> bool,
    twice: impl Fn(&T) -> String,
) -> Condition {
    match parsed {
        Ok(one) if kept.iter().any(|known| same(known, &one)) => Condition::left_out(
            "Duplicate",
            format!("{}, and the one read first is used", twice(&one)),
        ),
        Ok(one) => {
            kept.push(one);
            Condition::loaded()
        }
        Err(message) => Condition::left_out("Invalid", message),
    }
}

/// Stores `parsed` in `kept` for a kind that a cluster declares at most
/// once. When `kept` is already set, the first stays.
fn only<T>(kept: &mut Option<T>, parsed: Result<T, String>, what: &str) -> Condition {
    match parsed {
        Ok(_) if kept.is_some() => Condition::left_out(
            "Duplicate",
            format!("a cluster declares one {what}, and the one read first is used"),
        ),
        Ok(one) => {
            *kept = Some(one);
            Condition::loaded()
        }
        Err(message) => Condition::left_out("Invalid", message),
    }
}

#[cfg(test)]
mod tests;
