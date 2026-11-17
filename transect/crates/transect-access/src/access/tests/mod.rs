//! The reduction, over one small cluster written for it: what each
//! account is granted, and what follows, for every form of rule and of
//! step.

use std::sync::Arc;

use k8s_openapi::api::core::v1::Pod;
use rstest::rstest;
use serde::Deserialize;

use super::*;

mod subjects;

/// The cluster: an admin, a deployer who may change its namespace's
/// workloads, a GitOps tool's controller and an account that may edit
/// what it applies, accounts limited by name, by exec, and by binding,
/// a group, a user bound through an aggregated role, and two accounts
/// that may each run pods as the other.
const CLUSTER: &str = include_str!("cluster.yaml");

const ACCOUNTS: [(&str, &str); 31] = [
    ("ops", "admin"),
    ("ops", "applier"),
    ("ops", "deployer"),
    ("ops", "default"),
    ("tools", "editor"),
    ("tools", "reader"),
    ("tools", "binder"),
    ("tools", "wild"),
    ("shop", "viewer"),
    ("shop", "web"),
    ("shop", "default"),
    ("ledger", "db"),
    ("ledger", "locked"),
    ("ledger", "default"),
    ("watch", "scraper"),
    ("tools", "node-reader"),
    ("shop", "labeller"),
    ("shop", "minter"),
    ("shop", "one-token"),
    ("granting", "granter"),
    ("rewriting", "cluster-binder"),
    ("rewriting", "escalator"),
    ("rewriting", "escalate-only"),
    ("shop", "peek"),
    ("shop", "peek-elsewhere"),
    ("sources", "pusher"),
    ("pinned", "pinner"),
    ("pinned", "misser"),
    ("naming", "runner"),
    ("naming", "other"),
    ("naming", "writer"),
];

fn typed<K: serde::de::DeserializeOwned>(value: &serde_yaml::Value) -> Arc<K> {
    Arc::new(serde_yaml::from_value(value.clone()).unwrap())
}

fn access() -> Access {
    let accounts = ACCOUNTS.iter().map(|(n, a)| (n.to_string(), a.to_string()));
    reduce(CLUSTER, accounts.collect(), appliers())
}

/// The reduction of `cluster`, whose ServiceAccounts are `accounts`
/// and the ones its documents declare.
fn reduce(cluster: &str, mut accounts: Vec<(String, String)>, appliers: Vec<Applier>) -> Access {
    let mut rbac = RbacObjects::default();
    let mut pods: Vec<Running> = Vec::new();
    for document in serde_yaml::Deserializer::from_str(cluster) {
        let value = serde_yaml::Value::deserialize(document).unwrap();
        let kind = value["kind"].as_str().unwrap_or_default().to_string();
        match kind.as_str() {
            "ClusterRole" => rbac.cluster_roles.push(typed(&value)),
            "Role" => rbac.roles.push(typed(&value)),
            "ClusterRoleBinding" => rbac.cluster_role_bindings.push(typed(&value)),
            "RoleBinding" => rbac.role_bindings.push(typed(&value)),
            "Pod" => {
                let pod: Arc<Pod> = typed(&value);
                pods.extend(Running::read(&pod));
            }
            "ServiceAccount" => {
                let meta = &value["metadata"];
                let text = |key: &str| meta[key].as_str().unwrap().to_string();
                accounts.push((text("namespace"), text("name")));
            }
            other => panic!("no {other} in the reduction's cluster"),
        }
    }
    let inputs = Inputs {
        rbac: &rbac,
        service_accounts: accounts,
        pods,
        gitops: vec![
            ("apply.example.test".into(), "repos".into()),
            ("apply.example.test".into(), "trees".into()),
        ],
        appliers,
    };
    Access::build(&inputs)
}

/// The main cluster's appliers: a Tree its controller applies, which
/// fetches through a Repo in another namespace; one that names the
/// account it applies as, in a field; and one a grant names.
fn appliers() -> Vec<Applier> {
    let controller = vec![Account::service_account("ops", "applier")];
    let tree = |namespace: &str, name: &str| {
        Object::new("apply.example.test", "Tree", Some(namespace), name)
    };
    let repo = Object::new("apply.example.test", "Repo", Some("sources"), "main");
    vec![
        Applier {
            object: tree("tools", "site"),
            plural: "trees".into(),
            accounts: controller.clone(),
            named: false,
            field: None,
            fallback: Vec::new(),
            source: Some((repo, "repos".into())),
        },
        Applier {
            object: tree("ledger", "books"),
            plural: "trees".into(),
            accounts: vec![Account::service_account("ledger", "db")],
            named: true,
            field: Some("spec.serviceAccountName".into()),
            fallback: controller.clone(),
            source: None,
        },
        Applier {
            object: tree("naming", "plain"),
            plural: "trees".into(),
            accounts: vec![Account::service_account("naming", "runner")],
            named: true,
            field: Some("spec.serviceAccountName".into()),
            fallback: vec![Account::service_account("naming", "runner")],
            source: None,
        },
        Applier {
            object: tree("pinned", "pinned"),
            plural: "trees".into(),
            accounts: controller,
            named: false,
            field: None,
            fallback: Vec::new(),
            source: None,
        },
    ]
}

/// The account a test names: `ns/name`, `Group name`, or `User name`.
fn named(text: &str) -> Account {
    match text.split_once(' ') {
        Some(("Group", name)) => Account::Group(name.into()),
        Some(("User", name)) => Account::User(name.into()),
        _ => {
            let (namespace, name) = text.split_once('/').unwrap();
            Account::service_account(namespace, name)
        }
    }
}

fn place(at: &Where) -> &str {
    match at {
        Where::Everywhere => "*",
        Where::In(namespace) => namespace,
    }
}

fn hop(hop: &Hop) -> String {
    match hop {
        Hop::RunsAs { account, .. } => format!("runs as {}", account.short()),
        Hop::ExecToken { account, .. } => format!("exec token {}", account.short()),
        Hop::StartsPod { .. } => "starts pod".into(),
        Hop::ExecMounts { .. } => "exec mounts".into(),
        Hop::Applies { account, how, .. } => match how {
            AppliesAs::MayName { .. } => format!("may name {}", account.short()),
            AppliesAs::Unset { .. } => format!("empties to {}", account.short()),
            AppliesAs::Through { source } => {
                format!("through {} applies as {}", source.name, account.short())
            }
            _ => format!("applies as {}", account.short()),
        },
        Hop::Binds { role, .. } => format!("binds {}", role.name),
        Hop::Rewrites { role, .. } => format!("rewrites {}", role.name),
        Hop::MintsToken { account, .. } => format!("mints token {}", account.short()),
        Hop::Impersonates { account } => format!("impersonates {}", account.short()),
    }
}

/// Each grant and follow of `capability` to `account`, in a line.
fn said(access: &Access, account: &str, capability: Capability) -> Vec<String> {
    let held = access
        .of(&named(account))
        .unwrap_or_else(|| panic!("no {account}"));
    let Some(cell) = held.cells.get(&capability) else {
        return Vec::new();
    };
    let names = |names: &[String]| match names {
        [] => String::new(),
        names => format!(" by name {}", names.join(",")),
    };
    let granted = cell.granted.iter().flat_map(|(at, vias)| {
        vias.iter().map(move |via| {
            let group = via
                .group
                .as_ref()
                .map(|g| format!(" as Group {g}"))
                .unwrap_or_default();
            format!("granted {}{}{group}", place(at), names(&via.names))
        })
    });
    let follows = cell.follows.iter().map(|(at, follow)| {
        format!(
            "follows {} {}{}",
            place(at),
            hop(&follow.hop),
            names(&follow.names)
        )
    });
    granted.chain(follows).collect()
}

#[rstest]
#[case::every_verb_everywhere("ops/admin", Capability::ReadSecrets, &["granted *"])]
#[case::a_group_bound_to_everything("Group system:masters", Capability::GrantRoles, &["granted *"])]
#[case::a_role_in_one_namespace("ops/deployer", Capability::ChangeWorkloads, &["granted ops", "follows * runs as ops/admin"])]
#[case::running_as_the_admin_beside_it("ops/deployer", Capability::GrantRoles, &["follows * runs as ops/admin"])]
#[case::everywhere_through_the_admin_beside_it("ops/deployer", Capability::ReadSecrets, &["follows * runs as ops/admin"])]
#[case::a_rolebinding_to_a_clusterrole("tools/editor", Capability::ChangeApplied, &["granted tools", "follows * applies as ops/applier"])]
#[case::what_the_applier_applies_as("tools/editor", Capability::Exec, &["follows * applies as ops/applier"])]
#[case::what_an_applier_names("User bob", Capability::ReadSecrets, &["follows * applies as ledger/db"])]
#[case::emptying_the_account_an_applier_names("User bob", Capability::GrantRoles, &["follows * empties to ops/applier"])]
#[case::naming_any_account_in_the_appliers_namespace("naming/writer", Capability::Exec, &["follows naming may name naming/other"])]
#[case::a_source_in_another_namespace("sources/pusher", Capability::Exec, &["follows * through main applies as ops/applier"])]
#[case::an_applier_by_name("pinned/pinner", Capability::Exec, &["follows * applies as ops/applier"])]
#[case::another_applier_by_name("pinned/misser", Capability::Exec, &[])]
#[case::secrets_by_name("tools/reader", Capability::ReadSecrets, &["granted tools by name api-token"])]
#[case::nothing_follows_from_names("tools/reader", Capability::RunPods, &[])]
#[case::bind_and_create_bindings("tools/binder", Capability::GrantRoles, &["granted tools"])]
#[case::binding_its_namespaces_roles_to_itself("tools/binder", Capability::ReadSecrets, &["follows tools binds secrets"])]
#[case::no_role_in_its_namespace_runs_pods("tools/binder", Capability::RunPods, &[])]
#[case::binding_a_clusterrole_in_its_namespace("granting/granter", Capability::RunPods, &["follows granting binds cluster-admin"])]
#[case::creating_clusterrolebindings_through_a_rolebinding("rewriting/cluster-binder", Capability::RunPods, &[])]
#[case::binding_a_clusterrole_everywhere("User frank", Capability::Exec, &["follows * binds cluster-admin"])]
#[case::rewriting_the_role_it_is_bound_to("rewriting/escalator", Capability::RunPods, &["follows rewriting rewrites self-editor"])]
#[case::escalate_without_bind_or_update("rewriting/escalate-only", Capability::RunPods, &[])]
#[case::a_star_after_the_kind_grants_no_exec("tools/wild", Capability::Exec, &[])]
#[case::a_user_named_as_a_service_account("tools/wild", Capability::ReadSecrets, &["granted tools"])]
#[case::exec_and_the_mounted_token("shop/viewer", Capability::ChangeWorkloads, &["follows ledger exec token shop/web"])]
#[case::exec_and_the_secrets_a_pod_mounts("shop/viewer", Capability::ReadSecrets, &["follows * exec token shop/web", "follows shop exec mounts by name web-tls"])]
#[case::no_token_mounted_no_chain("ledger/locked", Capability::RunPods, &[])]
#[case::a_group_of_a_namespaces_accounts("ledger/locked", Capability::ReadSecrets, &["granted ledger as Group system:serviceaccounts:ledger"])]
#[case::the_group_itself("Group system:serviceaccounts:ledger", Capability::ReadSecrets, &["granted ledger"])]
#[case::an_aggregated_role_to_a_user("User alice", Capability::RunPods, &["granted shop"])]
#[case::one_hop_from_a_user("User alice", Capability::ChangeWorkloads, &["follows ledger runs as shop/web"])]
#[case::two_hops_name_the_first("User alice", Capability::ReadSecrets, &["follows * runs as shop/web", "follows shop starts pod"])]
#[case::a_cycle_one_way("shop/web", Capability::RunPods, &["follows shop runs as ledger/db"])]
#[case::the_kubelets_api_in_every_namespace("watch/scraper", Capability::Exec, &["granted *"])]
#[case::the_kubelets_api_reaches_a_mounted_token("watch/scraper", Capability::ChangeWorkloads, &["follows ledger exec token shop/web"])]
#[case::the_kubelets_api_through_a_rolebinding("tools/node-reader", Capability::Exec, &[])]
#[case::patching_a_pod_runs_as_its_account("shop/labeller", Capability::ChangeWorkloads, &["follows ledger exec token shop/web"])]
#[case::a_token_it_may_mint("shop/minter", Capability::ChangeWorkloads, &["follows ledger mints token shop/web"])]
#[case::a_named_token_it_may_mint("shop/one-token", Capability::ReadSecrets, &["follows * mints token shop/viewer"])]
#[case::impersonating_every_account("User carol", Capability::Exec, &["follows * impersonates ops/admin"])]
#[case::impersonating_accounts_through_a_rolebinding("User dave", Capability::ReadSecrets, &["follows * impersonates ledger/db"])]
#[case::impersonating_users_through_a_rolebinding("User dave", Capability::GrantRoles, &[])]
#[case::a_group_with_no_user_to_impersonate("User erin", Capability::Exec, &[])]
#[case::exec_into_a_pod_by_name("shop/peek", Capability::ReadSecrets, &["follows * exec token shop/web", "follows shop exec mounts by name web-tls"])]
#[case::exec_into_another_pod_by_name("shop/peek-elsewhere", Capability::ReadSecrets, &[])]
#[case::the_kubelets_api_of_one_machine("User gina", Capability::ChangeWorkloads, &["follows ledger exec token shop/web"])]
#[case::a_cycle_the_other_way("ledger/db", Capability::ChangeWorkloads, &["follows ledger runs as shop/web"])]
fn reduces_rbac_to_what_each_account_may_do(
    #[case] account: &str,
    #[case] capability: Capability,
    #[case] expected: &[&str],
) {
    assert_eq!(said(&access(), account, capability), expected);
}

#[rstest]
#[case::bound_to_it("ops/admin", true)]
#[case::bound_to_something_less("ops/deployer", false)]
fn knows_who_is_bound_to_every_verb_everywhere(#[case] account: &str, #[case] bound: bool) {
    let access = access();
    let held = access.of(&named(account)).unwrap();
    assert_eq!(
        held.everything
            .iter()
            .any(|(at, _)| *at == Where::Everywhere),
        bound
    );
}

#[rstest]
#[case::a_role_in_its_namespace("apps", "deployments", "ops", "web", &["ServiceAccount ops/admin", "ServiceAccount ops/applier", "ServiceAccount ops/deployer", "Group system:masters"])]
#[case::not_in_another_namespace("apps", "deployments", "shop", "web", &["ServiceAccount ops/admin", "ServiceAccount ops/applier", "Group system:masters"])]
#[case::a_custom_kind("apply.example.test", "trees", "tools", "site", &["ServiceAccount ops/admin", "ServiceAccount ops/applier", "ServiceAccount tools/editor", "Group system:masters"])]
fn knows_who_may_change_each_kind(
    #[case] group: &str,
    #[case] plural: &str,
    #[case] namespace: &str,
    #[case] name: &str,
    #[case] expected: &[&str],
) {
    let access = access();
    let found: Vec<String> = access
        .changers
        .of(group, plural, namespace, name)
        .iter()
        .map(Account::text)
        .collect();
    assert_eq!(found, expected);
}

fn accounts_page() -> super::accounts::AccountsPage {
    let look = super::accounts::Look {
        href: &|_| None,
        why: &|_| None,
    };
    super::accounts::AccountsPage::build(&access(), &look)
}

#[test]
fn heads_a_run_through_an_admin_by_the_account_it_may_run_as() {
    let headings: Vec<String> = accounts_page()
        .runs
        .iter()
        .map(|run| run.heading.iter().map(|part| part.text.as_str()).collect())
        .collect();
    let through = "May run pods or change workloads in Namespace ops, and so may run as ServiceAccount ops/admin, which is bound to ClusterRole cluster-admin";
    assert!(headings.iter().any(|h| h == through), "{headings:#?}");
}

#[test]
fn leaves_the_step_a_run_heading_states_out_of_its_rows() {
    let page = accounts_page();
    let through = &page.runs[1..page.runs.len() - 1];
    let steps: Vec<(&str, Option<String>)> = through
        .iter()
        .flat_map(|run| &run.rows)
        .filter(|row| row.step.is_some())
        .map(|row| (row.name.as_str(), row.line()))
        .collect();
    assert_eq!(steps, vec![]);
}

#[test]
fn says_a_row_s_role_and_step_in_one_line() {
    let page = accounts_page();
    let lines: Vec<Option<String>> = page
        .runs
        .iter()
        .flat_map(|run| &run.rows)
        .map(|row| row.line())
        .collect();
    assert!(
        lines
            .iter()
            .any(|line| line.as_deref() == Some("ClusterRole changer → may run as ledger/db")),
        "{lines:#?}"
    );
}
