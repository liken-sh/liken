//! The hover text of a matrix cell: where a binding grants the
//! capability and through which binding and role, or how the capability
//! follows from other capabilities.

use super::super::account::{Account, AppliesAs, Hop, Via, Where};
use super::super::{Access, Capability, Follow};
use transect_graph::edges::Object;

/// The clause that says which binding and role give a grant, such as
/// ", through ClusterRoleBinding admins, to ClusterRole cluster-admin".
/// When the grant comes through a group, the clause starts with ", as a
/// member of Group ...".
fn through(via: &Via) -> String {
    let group = match &via.group {
        Some(group) => format!(", as a member of Group {group}"),
        None => String::new(),
    };
    format!(
        "{group}, through {}, to {}",
        named(&via.binding),
        role(&via.role)
    )
}

/// An object as `Kind namespace/name`, or as `Kind name` when it has no
/// namespace.
pub fn named(object: &Object) -> String {
    match &object.namespace {
        Some(namespace) => format!("{} {namespace}/{}", object.kind, object.name),
        None => format!("{} {}", object.kind, object.name),
    }
}

/// A role as `Kind name`, without a namespace, because a binding can
/// reference a Role only in its own namespace.
fn role(object: &Object) -> String {
    format!("{} {}", object.kind, object.name)
}

/// The sentence for a grant, such as "May read Secrets in shop, for the
/// object named api-token only, through …".
pub fn granted(capability: Capability, at: &Where, via: &Via) -> String {
    let names = match via.names.as_slice() {
        [] => String::new(),
        [name] => format!(", for the object named {name} only"),
        names => format!(", for the objects named {} only", names.join(", ")),
    };
    format!(
        "May {} {}{names}{}.",
        capability.words(),
        at.words(),
        through(via)
    )
}

/// The clause after an account's name that says how `account` has
/// `capability` at `at`: ", through …" for a grant that is not limited
/// to named objects, or ", which follows from its own grants" otherwise.
fn source(access: &Access, account: &Account, capability: Capability, at: &Where) -> String {
    let cell = access
        .of(account)
        .and_then(|held| held.cells.get(&capability));
    let places = [at, &Where::Everywhere];
    let via = places.iter().find_map(|place| {
        let vias = cell?.granted.get(place)?;
        vias.iter().find(|via| via.names.is_empty())
    });
    match via {
        Some(via) => through(via),
        None => ", which follows from its own grants".into(),
    }
}

/// The sentence for a capability that follows by `follow.hop`, such as
/// "It may run as ServiceAccount ops/admin: it may run pods in ops, …
/// ServiceAccount ops/admin may grant roles in every namespace, …".  It
/// gives the capability that allows the step, and then what the target
/// account may do.
pub fn follows(
    access: &Access,
    account: &Account,
    capability: Capability,
    at: &Where,
    follow: &Follow,
) -> String {
    let target = |other: &Account| {
        format!(
            " {} may {} {}{}.",
            other.text(),
            capability.words(),
            at.words(),
            source(access, other, capability, at)
        )
    };
    let basis = |basis: Capability, there: &Where| {
        format!(
            "it may {} {}{}",
            basis.words(),
            there.words(),
            source(access, account, basis, there)
        )
    };
    match &follow.hop {
        Hop::RunsAs {
            account: other,
            basis: by,
            at: there,
        } => {
            format!(
                "It may run as {}: {}.{}",
                other.text(),
                basis(*by, there),
                target(other)
            )
        }
        Hop::ExecToken {
            account: other,
            at: there,
        } => format!(
            "It may exec into pods {} that run as {} and mount its token: {}.{}",
            there.words(),
            other.text(),
            basis(Capability::Exec, there),
            target(other)
        ),
        Hop::StartsPod {
            basis: by,
            at: there,
        } => format!(
            "A pod it starts {} may mount any Secret there: {}.",
            there.words(),
            basis(*by, there)
        ),
        Hop::ExecMounts { at: there } => format!(
            "It may exec into pods {} that mount {}: {}.",
            at.words(),
            follow.names.join(", "),
            basis(Capability::Exec, there)
        ),
        Hop::Applies {
            account: other,
            applier,
            how,
        } => {
            let object = self::named(applier);
            let mut changed = applier.namespace.clone().unwrap_or_default();
            let how = match how {
                AppliesAs::Names => format!("as {object} names"),
                AppliesAs::Controller => {
                    format!("as the controller of {} objects", applier.kind)
                }
                AppliesAs::MayName { field } => {
                    format!("since it may set {field} of {object} to it")
                }
                AppliesAs::Unset { field } => {
                    format!("since it may empty {field} of {object}, which then applies as it")
                }
                AppliesAs::Through { source } => {
                    changed = source.namespace.clone().unwrap_or_default();
                    format!(
                        "since it may change {}, which {object} fetches through",
                        self::named(source)
                    )
                }
            };
            format!(
                "It may change what {} applies, {how}: {}.{}",
                other.text(),
                basis(Capability::ChangeApplied, &Where::In(changed)),
                target(other)
            )
        }
        Hop::Binds {
            role: bound,
            at: there,
        } => format!(
            "It may bind {} to itself {}: a binding grants it bind on the role and create on the binding's kind there.",
            named(bound),
            there.words()
        ),
        Hop::Rewrites {
            role: bound,
            at: there,
        } => format!(
            "It may rewrite {}, which binds it {}, to grant anything: a binding grants it escalate and update or patch on the role.",
            named(bound),
            there.words()
        ),
        Hop::MintsToken {
            account: other,
            at: there,
        } => format!(
            "It may create a token for {} and sign in as it: a binding grants it create on serviceaccounts/token {}.{}",
            other.text(),
            there.words(),
            target(other)
        ),
        Hop::Impersonates { account: other } => {
            let superuser = match other {
                Account::Group(name) if name == "system:masters" => {
                    " The API server lets that group do anything, bound or not."
                }
                _ => "",
            };
            format!(
                "It may impersonate {}: a binding grants it impersonate.{superuser}{}",
                other.text(),
                target(other)
            )
        }
    }
}

/// A short description of `hop` for the line under a row's roles: the
/// text before the account, the account the step goes through, if any,
/// and the text after it.
pub fn step(hop: &Hop) -> (&'static str, Option<&Account>, &'static str) {
    match hop {
        Hop::RunsAs { account, .. } => ("may run as ", Some(account), ""),
        Hop::ExecToken { account, .. } => ("may exec into pods that run as ", Some(account), ""),
        Hop::Applies { account, .. } => ("may change what ", Some(account), " applies"),
        Hop::StartsPod { .. } => ("a pod it starts may mount any Secret", None, ""),
        Hop::ExecMounts { .. } => ("may exec into pods that mount Secrets", None, ""),
        Hop::Binds { .. } => ("may bind a role to itself", None, ""),
        Hop::Rewrites { .. } => ("may rewrite a role it is bound to", None, ""),
        Hop::MintsToken { account, .. } => ("may create a token for ", Some(account), ""),
        Hop::Impersonates { account } => ("may impersonate ", Some(account), ""),
    }
}

#[cfg(test)]
mod granted_tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::one_name(&["sources"], "May read Secrets in every namespace, for the object named sources only, through ClusterRoleBinding readers, to ClusterRole reader.")]
    #[case::two_names(&["a", "b"], "May read Secrets in every namespace, for the objects named a, b only, through ClusterRoleBinding readers, to ClusterRole reader.")]
    fn writes_the_names_a_grant_is_limited_to(#[case] names: &[&str], #[case] said: &str) {
        let via = Via {
            binding: Object::new(
                "rbac.authorization.k8s.io",
                "ClusterRoleBinding",
                None,
                "readers",
            ),
            role: Object::new("rbac.authorization.k8s.io", "ClusterRole", None, "reader"),
            names: names.iter().map(|name| name.to_string()).collect(),
            group: None,
        };
        assert_eq!(
            granted(Capability::ReadSecrets, &Where::Everywhere, &via),
            said
        );
    }
}
