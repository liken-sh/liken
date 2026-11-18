//! How an account gets capabilities that no rule grants it directly:
//! acting as another account by minting its token or impersonating it,
//! binding a role to itself, rewriting a role it is bound to, and
//! running exec in pods that a grant names.

use super::super::account::{Account, Hop, Where};
use super::super::capability::{ALL, Scope};
use super::super::grants::RoleGrants;
use super::super::routes::{Names, Route, admits};
use super::{Follow, Places, Steps};
use transect_graph::edges::Object;

/// The group that the API server's authorizer allows every request,
/// with or without a binding.
const MASTERS: &str = "system:masters";

/// Records the accounts that `routes` let an account act as, by minting
/// a token for a ServiceAccount or by impersonating an account.  When
/// the routes come from another step, such as a role that the account
/// may bind, `through` is that step and is each new step's hop.
/// Otherwise the hop is the route itself.
pub(super) fn acts_through(
    routes: &[(Route, Where, Names)],
    through: Option<&Hop>,
    places: &Places,
    steps: &mut Steps,
) {
    let hop = |own: Hop| through.cloned().unwrap_or(own);
    let mut impersonates_a_user = false;
    for (route, at, names) in routes {
        match route {
            Route::MintsTokens | Route::ImpersonatesServiceAccounts => {
                impersonates_a_user |= *route == Route::ImpersonatesServiceAccounts;
                for namespace in places.within(at) {
                    let here = places.accounts.get(namespace).into_iter().flatten();
                    for account in here {
                        let Account::ServiceAccount { name, .. } = account else {
                            continue;
                        };
                        if !admits(names, name) {
                            continue;
                        }
                        let own = match route {
                            Route::MintsTokens => Hop::MintsToken {
                                account: account.clone(),
                                at: Where::In(namespace.clone()),
                            },
                            _ => Hop::Impersonates {
                                account: account.clone(),
                            },
                        };
                        steps.acts_as.push((account.clone(), hop(own)));
                    }
                }
            }
            Route::ImpersonatesUsers => {
                impersonates_a_user = true;
                for user in places.users.iter().filter(|u| admits(names, &u.short())) {
                    let own = Hop::Impersonates {
                        account: user.clone(),
                    };
                    steps.acts_as.push((user.clone(), hop(own)));
                }
            }
            _ => {}
        }
    }
    // The API server accepts an `Impersonate-Group` header only beside an
    // `Impersonate-User` header, and a ServiceAccount counts as a user.
    // So a grant on groups alone lets the account impersonate no group.
    if !impersonates_a_user {
        return;
    }
    let groups = routes
        .iter()
        .filter(|(route, ..)| *route == Route::ImpersonatesGroups);
    for (_, _, names) in groups {
        for group in places.groups.iter().filter(|g| admits(names, &g.short())) {
            let own = Hop::Impersonates {
                account: group.clone(),
            };
            steps.acts_as.push((group.clone(), hop(own)));
        }
        if admits(names, MASTERS) {
            let own = Hop::Impersonates {
                account: Account::Group(MASTERS.into()),
            };
            every_capability(Where::Everywhere, hop(own), steps);
        }
    }
}

/// Records each role that `held` may bind to itself, and so what it may
/// do through that role.  Binding a role takes `bind` on the role's
/// kind, covering its name, and `create` on the binding's kind, in a
/// place where both grants apply.  A RoleBinding can bind a Role of its
/// own namespace or any ClusterRole.  A ClusterRoleBinding can bind
/// only a ClusterRole, and creating one takes a grant in every
/// namespace, because the kind is cluster-scoped.
pub(super) fn binds(held: &super::Held, places: &Places, steps: &mut Steps) {
    let at = |wanted: Route| {
        let routes = held.routes.iter();
        let routes = routes.filter(move |((route, _), _)| *route == wanted);
        routes.map(|((_, at), names)| (at, names))
    };
    let creates_cluster =
        at(Route::CreatesClusterRoleBindings).any(|(at, _)| *at == Where::Everywhere);
    for (binds, names) in at(Route::BindsRoles) {
        for (creates, _) in at(Route::CreatesRoleBindings) {
            let Some(both) = meet(binds, creates) else {
                continue;
            };
            for namespace in places.within(&both) {
                let roles = places.roles.iter().filter(|(role, _)| {
                    role.kind == "Role"
                        && role.namespace.as_deref() == Some(namespace)
                        && admits(names, &role.name)
                });
                for (role, grants) in roles {
                    let here = Where::In(namespace.clone());
                    bound(role, grants, Scope::Namespace, here, places, steps);
                }
            }
        }
    }
    for (binds, names) in at(Route::BindsClusterRoles) {
        let roles = places
            .roles
            .iter()
            .filter(|(role, _)| role.kind == "ClusterRole" && admits(names, &role.name));
        let roles: Vec<&(Object, RoleGrants)> = roles.collect();
        for (creates, _) in at(Route::CreatesRoleBindings) {
            let Some(both) = meet(binds, creates) else {
                continue;
            };
            // A RoleBinding grants a ClusterRole's rules only in its
            // own namespace. When both grants apply everywhere, the
            // account can create one in each namespace, so the rules
            // apply in every namespace.
            let places_bound: Vec<Where> = match &both {
                Where::Everywhere => vec![Where::Everywhere],
                Where::In(_) => places.within(&both).map(|n| Where::In(n.clone())).collect(),
            };
            for here in places_bound {
                for (role, grants) in &roles {
                    bound(role, grants, Scope::Namespace, here.clone(), places, steps);
                }
            }
        }
        if *binds == Where::Everywhere && creates_cluster {
            for (role, grants) in &roles {
                bound(
                    role,
                    grants,
                    Scope::Cluster,
                    Where::Everywhere,
                    places,
                    steps,
                );
            }
        }
    }
}

/// Records what follows when `held` binds `role`, whose rules are
/// `grants`, to itself at `at` with a binding of `scope`: the
/// capabilities the role grants through such a binding, the accounts it
/// may act as, and the appliers it may change.
fn bound(
    role: &Object,
    grants: &RoleGrants,
    scope: Scope,
    at: Where,
    places: &Places,
    steps: &mut Steps,
) {
    let hop = Hop::Binds {
        role: role.clone(),
        at: at.clone(),
    };
    // `grants` picks its rules by the kind of binding only: `Everywhere`
    // for a ClusterRoleBinding and `In(_)` for a RoleBinding. It never
    // reads the namespace, so an empty namespace is enough.
    let probe = match scope {
        Scope::Cluster => Where::Everywhere,
        Scope::Namespace => Where::In(String::new()),
    };
    for (capability, names) in grants.at(&probe) {
        let follow = Follow {
            hop: hop.clone(),
            names: names.clone(),
        };
        steps.own.push((*capability, at.clone(), follow));
    }
    let routes = grants.routes_at(&probe).iter();
    let routes: Vec<(Route, Where, Names)> = routes
        .map(|(route, names)| (*route, at.clone(), names.clone()))
        .collect();
    acts_through(&routes, Some(&hop), places, steps);
    let writes = grants.writes.iter();
    let writes: Vec<_> = writes
        .map(|(kind, names)| (kind.clone(), at.clone(), names.clone()))
        .collect();
    super::appliers::applies_through(&writes, Some(&hop), places, steps);
}

/// Records every capability, wherever the binding applies, for each
/// role that `held` is bound to and may rewrite, because the account can
/// add any rule to that role. Rewriting a role to grant more than the
/// account already has takes `escalate`, and `update` or `patch`, on the
/// role's kind, covering its name. A ClusterRole has no namespace, so
/// only a grant in every namespace covers one.
pub(super) fn rewrites(held: &super::Held, steps: &mut Steps) {
    let at = |wanted: Route| {
        let routes = held.routes.iter();
        let routes = routes.filter(move |((route, _), _)| *route == wanted);
        routes.map(|((_, at), names)| (at, names))
    };
    let pairs = [
        (Route::EscalatesRoles, Route::RewritesRoles, "Role"),
        (
            Route::EscalatesClusterRoles,
            Route::RewritesClusterRoles,
            "ClusterRole",
        ),
    ];
    for (escalates, rewrites, kind) in pairs {
        for (escalate, escalate_names) in at(escalates) {
            for (rewrite, rewrite_names) in at(rewrites) {
                let Some(both) = meet(escalate, rewrite) else {
                    continue;
                };
                let roles = held.roles.iter().filter(|(_, role)| {
                    role.kind == kind
                        && admits(escalate_names, &role.name)
                        && admits(rewrite_names, &role.name)
                        && both.covers(role.namespace.as_deref().unwrap_or_default())
                });
                for (bound_at, role) in roles {
                    let hop = Hop::Rewrites {
                        role: role.clone(),
                        at: bound_at.clone(),
                    };
                    every_capability(bound_at.clone(), hop, steps);
                }
            }
        }
    }
}

fn every_capability(at: Where, hop: Hop, steps: &mut Steps) {
    for capability in ALL {
        let follow = Follow {
            hop: hop.clone(),
            names: Vec::new(),
        };
        steps.own.push((capability, at.clone(), follow));
    }
}

/// The place where both `a` and `b` apply, when they share one.
fn meet(a: &Where, b: &Where) -> Option<Where> {
    match (a, b) {
        (Where::Everywhere, other) | (other, Where::Everywhere) => Some(other.clone()),
        (a, b) if a == b => Some(a.clone()),
        _ => None,
    }
}

/// Records what follows when a grant limits exec to named targets: pod
/// names for `pods/exec`, or machine names for `nodes/proxy`. An
/// account that can exec into such a pod can act as the pod's
/// ServiceAccount through the token that the pod mounts, and can read
/// the Secrets that the pod mounts.
pub(super) fn execs_named(held: &super::Held, places: &Places, steps: &mut Steps) {
    for ((route, at), names) in &held.routes {
        // A grant with no names is the `Exec` capability, which covers
        // every pod, so this reads only the grants limited to names.
        if names.is_empty() {
            continue;
        }
        let named = |pod: &&super::Running| match route {
            Route::ExecsPods => admits(names, &pod.name),
            _ => pod.node.as_deref().is_some_and(|node| admits(names, node)),
        };
        if !matches!(route, Route::ExecsPods | Route::ProxiesNodes) {
            continue;
        }
        for namespace in places.within(at) {
            let pods = places.pods.get(namespace).into_iter().flatten();
            let pods: Vec<&super::Running> = pods.filter(named).collect();
            let here = Where::In(namespace.clone());
            let mut secrets: Vec<String> = Vec::new();
            for pod in &pods {
                if pod.token {
                    let account = Account::service_account(namespace, &pod.account);
                    let hop = Hop::ExecToken {
                        account: account.clone(),
                        at: here.clone(),
                    };
                    steps.acts_as.push((account, hop));
                }
                secrets.extend(pod.secrets.iter().cloned());
            }
            secrets.sort();
            secrets.dedup();
            if !secrets.is_empty() {
                let follow = Follow {
                    hop: Hop::ExecMounts { at: here.clone() },
                    names: secrets,
                };
                steps
                    .own
                    .push((super::Capability::ReadSecrets, here, follow));
            }
        }
    }
}
