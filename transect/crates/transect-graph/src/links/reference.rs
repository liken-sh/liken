//! How a card names one end of an edge: its words, and the page it
//! opens when it has one.  Every section that lists ends, whatever its
//! domain, names them the same way.

use std::fmt;

use crate::edges::{Endpoint, Object, Plurals};

/// One end a card names, with the page it opens when it has one.
#[derive(Clone, Debug, PartialEq)]
pub struct Reference {
    pub text: String,
    pub href: Option<String>,
}

/// What a reference names.
#[derive(Debug, PartialEq)]
pub enum Named<'a> {
    /// An object, and whether it is in the namespace of the card that
    /// names it, in which case the card leaves out its namespace.
    Object {
        object: &'a Object,
        here: bool,
    },
    Machine(&'a str),
    Namespace(&'a str),
    /// Every pod in a namespace, which a policy rule admits whole.
    EveryPod(&'a str),
    /// Every pod in the cluster.
    AnyPod,
    /// Any namespace, for a rule that admits every one, except the
    /// namespaces in `but`; with `pods`, any pod in them.
    AnyNamespace {
        pods: bool,
        but: &'a [String],
    },
    /// Anything that can reach the pod, inside the cluster or out.
    Anything,
    /// The addresses in a block, but those in its exceptions.
    Addresses {
        cidr: &'a str,
        except: &'a [String],
    },
}

impl Reference {
    pub fn to_end(end: &Endpoint, namespace: Option<&str>, plurals: &Plurals) -> Self {
        match end {
            Endpoint::Object(object) => Self {
                text: Named::Object {
                    object,
                    here: object.namespace.as_deref() == namespace,
                }
                .to_string(),
                href: super::page(object, plurals),
            },
            Endpoint::Machine(machine) => Self {
                text: Named::Machine(machine).to_string(),
                href: Some(super::machine(machine)),
            },
            // A ground box's page is under its `Outside`'s name, which
            // only the caller's `Outsides` holds.
            Endpoint::Outside(name) => Self {
                text: name.clone(),
                href: None,
            },
            Endpoint::Device(device) => Self {
                text: device.short(),
                href: super::device(device),
            },
        }
    }

    pub fn to_namespace(namespace: &str) -> Self {
        Self {
            text: Named::Namespace(namespace).to_string(),
            href: Some(format!("/namespaces/{namespace}")),
        }
    }
}

/// "Cluster postgres", "Deployment auth/login", "machine alpha", or
/// a namespace's name.
impl fmt::Display for Named<'_> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Object { object, here } => {
                let here = here.then_some(object.namespace.as_deref()).flatten();
                f.write_str(&super::kind_and_name_in(object, here))
            }
            Self::Machine(machine) => write!(f, "machine {machine}"),
            Self::Namespace(namespace) => write!(f, "{namespace}"),
            Self::EveryPod(namespace) => write!(f, "every pod in {namespace}"),
            Self::AnyPod => f.write_str("any pod"),
            Self::AnyNamespace { pods, but } => {
                let lead = match pods {
                    true => "any pod in any namespace",
                    false => "any namespace",
                };
                match but {
                    [] => f.write_str(lead),
                    but => write!(f, "{lead} but {}", listed(but)),
                }
            }
            Self::Anything => f.write_str("anything"),
            Self::Addresses { cidr, except: [] } => write!(f, "addresses in {cidr}"),
            Self::Addresses { cidr, except } => {
                write!(f, "addresses in {cidr} except {}", except.join(", "))
            }
        }
    }
}

/// The most namespaces "any namespace but" names before it counts the
/// rest.
const MOST_NAMED: usize = 4;

/// "a", "a and b", "a, b, and c", or "a, b, c, d, and 3 more".
fn listed(names: &[String]) -> String {
    match names {
        [one] => one.clone(),
        [one, two] => format!("{one} and {two}"),
        names if names.len() > MOST_NAMED => format!(
            "{}, and {} more",
            names[..MOST_NAMED].join(", "),
            names.len() - MOST_NAMED
        ),
        [rest @ .., last] => format!("{}, and {last}", rest.join(", ")),
        [] => String::new(),
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::every_pod(Named::EveryPod("shop"), "every pod in shop")]
    #[case::any_namespace(Named::AnyNamespace { pods: false, but: &[] }, "any namespace")]
    #[case::any_namespace_but_two(
        Named::AnyNamespace { pods: true, but: &["blog".into(), "watch".into()] },
        "any pod in any namespace but blog and watch"
    )]
    #[case::any_namespace_but_many(
        Named::AnyNamespace { pods: false, but: &["a".into(), "b".into(), "c".into(), "d".into(), "e".into(), "f".into()] },
        "any namespace but a, b, c, d, and 2 more"
    )]
    #[case::a_block(Named::Addresses { cidr: "192.0.2.0/24", except: &[] }, "addresses in 192.0.2.0/24")]
    #[case::a_block_with_exceptions(
        Named::Addresses { cidr: "10.0.0.0/8", except: &["10.1.0.0/16".into(), "10.2.0.0/16".into()] },
        "addresses in 10.0.0.0/8 except 10.1.0.0/16, 10.2.0.0/16"
    )]
    fn names_a_caller(#[case] named: Named, #[case] text: &str) {
        assert_eq!(named.to_string(), text);
    }
}
