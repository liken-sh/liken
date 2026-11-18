//! Sets of ports that a `NetworkPolicy` rule's `ports` admit: a port or
//! a range from `port` to `endPort` of one protocol, or every port.
//!
//! A connection is allowed only on a port that the policies of both
//! ends admit. So the rules of one end combine by union, and the two
//! ends combine by intersection. Each set is kept sorted, with
//! overlapping and adjacent spans merged, so two sets that admit the same ports are
//! equal, and a card can group callers by the ports they reach.

use k8s_openapi::api::networking::v1::NetworkPolicyPort;
use k8s_openapi::apimachinery::pkg::util::intstr::IntOrString;

/// The protocol of a rule's port that sets none, as the API defines.
pub const DEFAULT_PROTOCOL: &str = "TCP";

const HIGHEST: u16 = u16::MAX;

#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub enum Ports {
    /// Every port of every protocol, which is what a rule with no
    /// `ports` admits.
    Any,
    /// The listed spans, sorted and merged. An empty list admits no
    /// port.
    Listed(Vec<Span>),
}

/// Ports `first` to `last` of one protocol.
#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub struct Span {
    pub protocol: String,
    pub first: u16,
    pub last: u16,
}

/// A named container port of a pod. A rule's named port resolves to
/// its number on the pod that the connection reaches.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct NamedPort {
    pub name: String,
    pub protocol: String,
    pub number: u16,
}

impl Default for Ports {
    fn default() -> Self {
        Self::none()
    }
}

impl Ports {
    /// The empty set, which is where a union starts.
    pub fn none() -> Self {
        Self::Listed(Vec::new())
    }

    pub fn is_empty(&self) -> bool {
        matches!(self, Self::Listed(spans) if spans.is_empty())
    }

    pub fn union(&self, other: &Self) -> Self {
        match (self, other) {
            (Self::Any, _) | (_, Self::Any) => Self::Any,
            (Self::Listed(a), Self::Listed(b)) => {
                Self::merged(a.iter().chain(b).cloned().collect())
            }
        }
    }

    pub fn intersect(&self, other: &Self) -> Self {
        match (self, other) {
            (Self::Any, ports) | (ports, Self::Any) => ports.clone(),
            (Self::Listed(a), Self::Listed(b)) => {
                let mut spans = Vec::new();
                for x in a {
                    for y in b.iter().filter(|y| y.protocol == x.protocol) {
                        let (first, last) = (x.first.max(y.first), x.last.min(y.last));
                        if first <= last {
                            spans.push(Span::new(&x.protocol, first, last));
                        }
                    }
                }
                Self::merged(spans)
            }
        }
    }

    /// Whether it admits `port` in any protocol.
    pub fn covers(&self, port: u16) -> bool {
        match self {
            Self::Any => true,
            Self::Listed(spans) => spans.iter().any(|s| s.first <= port && port <= s.last),
        }
    }

    /// The ports that a rule's `ports` admit on a pod with the `named`
    /// container ports. An empty list admits every port. A named port
    /// admits the number that the pod gives that name with the same
    /// protocol, and admits nothing on a pod that does not define it.
    /// A port with no number admits its whole protocol, and an
    /// `endPort` below the port is ignored.
    pub fn of_rule(ports: &[NetworkPolicyPort], named: &[NamedPort]) -> Self {
        if ports.is_empty() {
            return Self::Any;
        }
        let mut spans = Vec::new();
        for port in ports {
            let protocol = port.protocol.as_deref().unwrap_or(DEFAULT_PROTOCOL);
            match &port.port {
                None => spans.push(Span::new(protocol, 1, HIGHEST)),
                Some(IntOrString::Int(number)) => {
                    let Ok(first) = u16::try_from(*number) else {
                        continue;
                    };
                    let last = port.end_port.and_then(|end| u16::try_from(end).ok());
                    spans.push(Span::new(protocol, first, last.unwrap_or(first).max(first)));
                }
                Some(IntOrString::String(name)) => {
                    let found = named
                        .iter()
                        .filter(|named| &named.name == name && named.protocol == protocol);
                    spans
                        .extend(found.map(|named| Span::new(protocol, named.number, named.number)));
                }
            }
        }
        Self::merged(spans)
    }

    /// The set of `spans`, sorted, with overlapping or adjacent spans
    /// of one protocol merged.
    fn merged(mut spans: Vec<Span>) -> Self {
        spans.sort();
        let mut merged: Vec<Span> = Vec::new();
        for span in spans {
            match merged.last_mut() {
                Some(last)
                    if last.protocol == span.protocol
                        && u32::from(span.first) <= u32::from(last.last) + 1 =>
                {
                    last.last = last.last.max(span.last);
                }
                _ => merged.push(span),
            }
        }
        Self::Listed(merged)
    }
}

impl Span {
    pub fn new(protocol: &str, first: u16, last: u16) -> Self {
        Self {
            protocol: protocol.into(),
            first,
            last,
        }
    }
}

#[cfg(test)]
pub mod fixtures {
    use super::*;

    pub fn tcp(first: u16, last: u16) -> Span {
        Span::new("TCP", first, last)
    }

    pub fn listed(spans: &[Span]) -> Ports {
        Ports::Listed(spans.to_vec())
    }

    pub fn port(
        protocol: Option<&str>,
        port: Option<IntOrString>,
        end: Option<i32>,
    ) -> NetworkPolicyPort {
        NetworkPolicyPort {
            protocol: protocol.map(Into::into),
            port,
            end_port: end,
        }
    }

    pub fn number(number: i32) -> NetworkPolicyPort {
        port(None, Some(IntOrString::Int(number)), None)
    }

    pub fn named(name: &str) -> NetworkPolicyPort {
        port(None, Some(IntOrString::String(name.into())), None)
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::fixtures::*;
    use super::*;

    fn pod_ports() -> Vec<NamedPort> {
        vec![
            NamedPort {
                name: "http".into(),
                protocol: "TCP".into(),
                number: 8080,
            },
            NamedPort {
                name: "dns".into(),
                protocol: "UDP".into(),
                number: 53,
            },
        ]
    }

    #[rstest]
    #[case::no_ports_is_every_port(vec![], Ports::Any)]
    #[case::a_number(vec![number(5432)], listed(&[tcp(5432, 5432)]))]
    #[case::a_range(vec![port(None, Some(IntOrString::Int(8000)), Some(8010))], listed(&[tcp(8000, 8010)]))]
    #[case::a_protocol_alone(vec![port(Some("UDP"), None, None)], listed(&[Span::new("UDP", 1, 65535)]))]
    #[case::a_named_port(vec![named("http")], listed(&[tcp(8080, 8080)]))]
    #[case::a_named_port_of_another_protocol(vec![named("dns")], Ports::none())]
    #[case::a_named_port_the_pod_lacks(vec![named("metrics")], Ports::none())]
    #[case::adjacent_numbers_merge(vec![number(80), number(81)], listed(&[tcp(80, 81)]))]
    #[case::sorted(vec![number(443), number(80)], listed(&[tcp(80, 80), tcp(443, 443)]))]
    fn reads_a_rules_ports(#[case] ports: Vec<NetworkPolicyPort>, #[case] expected: Ports) {
        assert_eq!(Ports::of_rule(&ports, &pod_ports()), expected);
    }

    #[rstest]
    #[case::any_and_some(Ports::Any, listed(&[tcp(80, 80)]), listed(&[tcp(80, 80)]))]
    #[case::overlap(listed(&[tcp(80, 90)]), listed(&[tcp(85, 100)]), listed(&[tcp(85, 90)]))]
    #[case::disjoint(listed(&[tcp(80, 80)]), listed(&[tcp(443, 443)]), Ports::none())]
    #[case::other_protocols(listed(&[tcp(53, 53)]), listed(&[Span::new("UDP", 53, 53)]), Ports::none())]
    fn intersects(#[case] a: Ports, #[case] b: Ports, #[case] expected: Ports) {
        assert_eq!(a.intersect(&b), expected);
    }

    #[rstest]
    #[case::any_wins(Ports::Any, listed(&[tcp(80, 80)]), Ports::Any)]
    #[case::overlapping(listed(&[tcp(80, 90)]), listed(&[tcp(85, 100)]), listed(&[tcp(80, 100)]))]
    #[case::apart(listed(&[tcp(443, 443)]), listed(&[tcp(80, 80)]), listed(&[tcp(80, 80), tcp(443, 443)]))]
    fn unites(#[case] a: Ports, #[case] b: Ports, #[case] expected: Ports) {
        assert_eq!(a.union(&b), expected);
    }
}
