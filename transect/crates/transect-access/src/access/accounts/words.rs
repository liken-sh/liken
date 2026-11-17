//! The sentences in a cell's title: where a binding grants the
//! capability and through which binding and role, or the step that the
//! capability follows by.

use super::super::account::{Account, AppliesAs, Hop, Via, Where};
use super::super::{Access, Capability, Follow};
use transect_graph::edges::Object;

/// `binding` and `role` as a clause names them: ", through
/// ClusterRoleBinding admins, to ClusterRole cluster-admin", after the
/// group it names when it names one.
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

/// A binding or a role as `Kind namespace/name`, or as `Kind name` when
/// it is in no namespace.
pub fn named(object: &Object) -> String {
    match &object.namespace {
        Some(namespace) => format!("{} {namespace}/{}", object.kind, object.name),
        None => format!("{} {}", object.kind, object.name),
    }
}

/// A role as its binding names it: a Role by name alone, since it is in
/// its binding's namespace.
fn role(object: &Object) -> String {
    format!("{} {}", object.kind, object.name)
}

/// "May read Secrets in shop, for the object named api-token only,
/// through …".
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

/// How `account` has `capability` at `at`, as a clause after its
/// name: ", through …" for a grant, or ", which follows from its own
/// grants" for a step.
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

/// "It may run as ServiceAccount ops/admin: it may run pods in ops, …
/// ServiceAccount ops/admin may grant roles in every namespace, …".
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

/// The step in a few words, for the line under a row's roles: the
/// words before the account it goes through, the account, and the
/// words after it.
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
    fn says_a_grant_limited_by_name_as_a_limit(#[case] names: &[&str], #[case] said: &str) {
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
