use rstest::rstest;

use super::fixtures::{Row, build};
use super::*;

fn pod(name: &'static str, owners: Vec<(&'static str, &'static str, &'static str)>) -> Row {
    ("", "Pod", Some("shop"), name, owners)
}

fn object(group: &str, kind: &str, name: &str) -> Object {
    Object::new(group, kind, Some("shop"), name)
}

#[rstest]
#[case::a_deployments_pod(
    vec![
        pod("web-5d8f-x2x7q", vec![("apps/v1", "ReplicaSet", "web-5d8f")]),
        ("apps", "ReplicaSet", Some("shop"), "web-5d8f", vec![("apps/v1", "Deployment", "web")]),
        ("apps", "Deployment", Some("shop"), "web", vec![]),
    ],
    object("apps", "Deployment", "web"),
)]
#[case::a_cron_jobs_pod(
    vec![
        pod("report-29-abcde", vec![("batch/v1", "Job", "report-29")]),
        ("batch", "Job", Some("shop"), "report-29", vec![("batch/v1", "CronJob", "report")]),
        ("batch", "CronJob", Some("shop"), "report", vec![]),
    ],
    object("batch", "CronJob", "report"),
)]
#[case::a_custom_kind_missing_from_the_index(
    vec![
        pod("cache-0", vec![("apps/v1", "StatefulSet", "cache")]),
        ("apps", "StatefulSet", Some("shop"), "cache", vec![("cache.example.io/v1", "Cache", "sessions")]),
    ],
    object("cache.example.io", "Cache", "sessions"),
)]
#[case::up_through_two_custom_kinds(
    vec![
        pod("run-0", vec![("apps/v1", "StatefulSet", "run")]),
        ("apps", "StatefulSet", Some("shop"), "run", vec![("agents.example.io/v1", "Session", "run")]),
        ("agents.example.io", "Session", Some("shop"), "run", vec![("agents.example.io/v1", "Mandate", "nightly")]),
        ("agents.example.io", "Mandate", Some("shop"), "nightly", vec![]),
    ],
    object("agents.example.io", "Mandate", "nightly"),
)]
#[case::a_cluster_scoped_owner(
    vec![
        pod("fetch-1", vec![("catalog.example.io/v1", "Catalog", "upstream")]),
        ("catalog.example.io", "Catalog", None, "upstream", vec![]),
    ],
    Object::new("catalog.example.io", "Catalog", None, "upstream"),
)]
#[case::a_pod_with_no_owner(vec![pod("debug", vec![])], object("", "Pod", "debug"))]
#[case::the_controller_not_the_first_owner(
    vec![pod("web-0", vec![("apps/v1", "StatefulSet", "web"), ("v1", "ConfigMap", "settings")])],
    object("apps", "StatefulSet", "web"),
)]
#[case::a_loop_ends(
    vec![
        pod("web-0", vec![("apps/v1", "StatefulSet", "web")]),
        ("apps", "StatefulSet", Some("shop"), "web", vec![("example.io/v1", "Thing", "a")]),
        ("example.io", "Thing", Some("shop"), "a", vec![("example.io/v1", "Thing", "b")]),
        ("example.io", "Thing", Some("shop"), "b", vec![("example.io/v1", "Thing", "a")]),
    ],
    object("example.io", "Thing", "a"),
)]
fn walks_a_pod_to_its_top_owner(#[case] rows: Vec<Row>, #[case] top: Object) {
    let ownership = build(&rows);
    let pod = object("", "Pod", rows[0].3);
    assert_eq!(ownership.top_owner(&pod), top);
}

/// The pod `db-0` of the `StatefulSet` `db`, which the `Database`
/// `orders` owns.
fn database() -> Ownership {
    build(&[
        pod("db-0", vec![("apps/v1", "StatefulSet", "db")]),
        (
            "apps",
            "StatefulSet",
            Some("shop"),
            "db",
            vec![("db.example.io/v1", "Database", "orders")],
        ),
        ("db.example.io", "Database", Some("shop"), "orders", vec![]),
    ])
}

#[rstest]
#[case::its_controller(object("apps", "StatefulSet", "db"), true)]
#[case::its_top_owner(object("db.example.io", "Database", "orders"), true)]
#[case::itself(object("", "Pod", "db-0"), false)]
#[case::a_controller_of_another_name(object("apps", "StatefulSet", "web"), false)]
#[case::a_controller_of_another_kind(object("apps", "DaemonSet", "db"), false)]
fn finds_an_owner_anywhere_on_the_chain(#[case] owner: Object, #[case] owned: bool) {
    let pod = object("", "Pod", "db-0");
    assert_eq!(database().is_owned_by(&pod, &owner), owned);
}

#[rstest]
fn counts_each_top_owner_once_per_namespace() {
    let ownership = build(&[
        pod("web-1", vec![("apps/v1", "ReplicaSet", "web-5d8f")]),
        pod("web-2", vec![("apps/v1", "ReplicaSet", "web-5d8f")]),
        (
            "apps",
            "ReplicaSet",
            Some("shop"),
            "web-5d8f",
            vec![("apps/v1", "Deployment", "web")],
        ),
        ("apps", "Deployment", Some("shop"), "web", vec![]),
        ("apps", "Deployment", Some("shop"), "idle", vec![]),
        (
            "apps",
            "StatefulSet",
            Some("shop"),
            "cache",
            vec![("cache.example.io/v1", "Cache", "sessions")],
        ),
        ("batch", "CronJob", Some("blog"), "digest", vec![]),
    ]);
    let shop: Vec<&Object> = ownership.workloads("shop").collect();
    assert_eq!(
        shop,
        vec![
            &object("apps", "Deployment", "idle"),
            &object("apps", "Deployment", "web"),
            &object("cache.example.io", "Cache", "sessions"),
        ]
    );
    assert_eq!(
        ownership.workloads_per_namespace(),
        HashMap::from([("shop".into(), 3), ("blog".into(), 1)])
    );
}

#[rstest]
fn an_old_replica_set_is_not_a_workload() {
    let ownership = build(&[(
        "apps",
        "ReplicaSet",
        Some("shop"),
        "web-old",
        vec![("apps/v1", "Deployment", "web")],
    )]);
    assert_eq!(ownership.workloads("shop").count(), 0);
}

#[rstest]
fn an_owned_service_reaches_its_top_owner_and_is_no_workload() {
    let ownership = build(&[
        (
            "",
            "Service",
            Some("shop"),
            "sessions-primary",
            vec![("cache.example.io/v1", "Cache", "sessions")],
        ),
        (
            "cache.example.io",
            "Cache",
            Some("shop"),
            "sessions",
            vec![],
        ),
    ]);
    let service = object("", "Service", "sessions-primary");
    assert_eq!(
        ownership.top_owner(&service),
        object("cache.example.io", "Cache", "sessions")
    );
    assert_eq!(ownership.workloads("shop").count(), 0);
}

#[rstest]
fn an_owned_object_is_managed_by_its_top_owner() {
    let ownership = build(&[
        pod("cache-0", vec![("apps/v1", "StatefulSet", "cache")]),
        (
            "apps",
            "StatefulSet",
            Some("shop"),
            "cache",
            vec![("cache.example.io/v1", "Cache", "sessions")],
        ),
        ("apps", "Deployment", Some("shop"), "web", vec![]),
    ]);
    let edges: Vec<(Endpoint, Endpoint)> = ownership
        .edges()
        .into_iter()
        .map(|edge| (edge.from, edge.to))
        .collect();
    let sessions = Endpoint::Object(object("cache.example.io", "Cache", "sessions"));
    assert_eq!(
        edges,
        vec![
            (
                Endpoint::Object(object("", "Pod", "cache-0")),
                sessions.clone()
            ),
            (
                Endpoint::Object(object("apps", "StatefulSet", "cache")),
                sessions
            ),
        ]
    );
}

/// The edges come out in the order of the objects they leave, so a page
/// that lists what an owner manages lists it in the same order on every
/// load.
#[rstest]
fn lists_the_managed_objects_in_order() {
    let names = ["web-e", "web-b", "web-d", "web-a", "web-c"];
    let mut rows: Vec<Row> = names
        .iter()
        .map(|name| pod(name, vec![("apps/v1", "Deployment", "web")]))
        .collect();
    rows.push(("apps", "Deployment", Some("shop"), "web", vec![]));
    let ownership = build(&rows);
    let from: Vec<Endpoint> = ownership
        .edges()
        .into_iter()
        .map(|edge| edge.from)
        .collect();
    let mut sorted = from.clone();
    sorted.sort();
    assert_eq!(from, sorted);
}
