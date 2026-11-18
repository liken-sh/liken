use std::sync::Arc;

use k8s_openapi::api::batch::v1::CronJob;
use k8s_openapi::api::core::v1::{Node, Pod};
use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;
use k8s_openapi::jiff::Timestamp;
use rstest::rstest;

use super::read::{ran, restarts, rollout, scheduled};
use super::*;
use transect_cluster::ownership::fixtures::build;
use transect_graph::edges::Plurals;

const NOW: &str = "2026-01-02T12:00:00Z";

fn at(time: &str) -> Timestamp {
    time.parse().unwrap()
}

fn web() -> Object {
    Object::new("apps", "Deployment", Some("shop"), "web")
}

fn rolled(time: &str, revision: &str) -> Change {
    Change {
        at: at(time),
        what: What::Rolled {
            workload: web(),
            revision: Some(revision.into()),
        },
        href: None,
        after: Vec::new(),
    }
}

fn restart(pod: &str, time: &str) -> (Object, Restart, Option<String>) {
    let restart = Restart {
        pod: pod.into(),
        container: "web".into(),
        reason: Some("Error".into()),
        exit_code: Some(1),
        at: at(time),
    };
    (web(), restart, None)
}

/// Each entry's time and text, and the text of each line under it.
fn listed(changes: &[Change]) -> Vec<(String, String, Vec<String>)> {
    let now = at(NOW);
    let each = changes.iter().map(|change| {
        let after = change.after.iter().map(Change::after_text).collect();
        (change.time(now), change.text(), after)
    });
    each.collect()
}

fn owned(expected: Vec<(&str, &str, Vec<&str>)>) -> Vec<(String, String, Vec<String>)> {
    let each = expected.into_iter().map(|(time, text, after)| {
        let after = after.into_iter().map(Into::into).collect();
        (time.into(), text.into(), after)
    });
    each.collect()
}

#[rstest]
#[case::newest_first(
    vec![rolled("2026-01-02T08:00:00Z", "4"), rolled("2026-01-02T10:00:00Z", "5")],
    vec![],
    vec![
        ("10:00", "shop/web rolled out revision 5", vec![]),
        ("08:00", "shop/web rolled out revision 4", vec![]),
    ]
)]
#[case::older_than_a_day_is_left_out(vec![rolled("2026-01-01T11:59:00Z", "4")], vec![], vec![])]
#[case::yesterday_names_its_day(
    vec![rolled("2026-01-01T22:13:00Z", "4")],
    vec![],
    vec![("Thu 22:13", "shop/web rolled out revision 4", vec![])]
)]
#[case::a_restart_soon_after_lists_under_the_rollout(
    vec![rolled("2026-01-02T10:00:00Z", "5")],
    vec![restart("web-1", "2026-01-02T10:04:00Z")],
    vec![(
        "10:00",
        "shop/web rolled out revision 5",
        vec!["then web in web-1 restarted (Error, exit 1)"]
    )]
)]
#[case::a_restart_long_after_stands_alone(
    vec![rolled("2026-01-02T10:00:00Z", "5")],
    vec![restart("web-1", "2026-01-02T11:00:00Z")],
    vec![
        ("11:00", "shop/web: web in web-1 restarted (Error, exit 1)", vec![]),
        ("10:00", "shop/web rolled out revision 5", vec![]),
    ]
)]
#[case::restarts_of_one_workload_are_one_entry(
    vec![],
    vec![restart("web-1", "2026-01-02T09:00:00Z"), restart("web-2", "2026-01-02T09:02:00Z")],
    vec![(
        "09:02",
        "shop/web: 2 containers restarted in 2 pods, the latest web in web-2 restarted (Error, exit 1)",
        vec![]
    )]
)]
fn arranges_changes_newest_first_with_restarts_under_their_rollout(
    #[case] changes: Vec<Change>,
    #[case] restarts: Vec<(Object, Restart, Option<String>)>,
    #[case] expected: Vec<(&str, &str, Vec<&str>)>,
) {
    let arranged = arrange(changes, restarts, at(NOW));
    assert_eq!(listed(&arranged), owned(expected));
}

fn meta(yaml: &str) -> ObjectMeta {
    serde_yaml::from_str(yaml).unwrap()
}

fn link(object: &Object) -> Option<String> {
    Some(format!("/{}", object.name))
}

#[rstest]
#[case::a_replica_set(
    "{namespace: shop, name: web-7d9f, creationTimestamp: \"2026-01-02T10:00:00Z\", annotations: {deployment.kubernetes.io/revision: \"5\"}, ownerReferences: [{apiVersion: apps/v1, kind: Deployment, name: web, uid: u, controller: true}]}",
    Some("shop/web rolled out revision 5")
)]
#[case::a_controller_revision(
    "{namespace: shop, name: db-5c8b, creationTimestamp: \"2026-01-02T10:00:00Z\", ownerReferences: [{apiVersion: apps/v1, kind: StatefulSet, name: db, uid: u, controller: true}]}",
    Some("shop/db rolled out a new revision")
)]
#[case::no_owner(
    "{namespace: shop, name: web-7d9f, creationTimestamp: \"2026-01-02T10:00:00Z\"}",
    None
)]
fn reads_a_rollout_from_its_revisions_creation(#[case] yaml: &str, #[case] expected: Option<&str>) {
    let found = rollout(&meta(yaml), &link);
    assert_eq!(found.map(|change| change.text()).as_deref(), expected);
}

#[rstest]
#[case::a_job_alone(
    "{namespace: shop, name: migrate, creationTimestamp: \"2026-01-02T10:00:00Z\"}",
    Some("Job shop/migrate ran")
)]
#[case::a_cron_jobs_run(
    "{namespace: shop, name: report-1, creationTimestamp: \"2026-01-02T10:00:00Z\", ownerReferences: [{apiVersion: batch/v1, kind: CronJob, name: report, uid: u}]}",
    None
)]
fn reads_a_run_from_a_job_no_cron_job_started(#[case] yaml: &str, #[case] expected: Option<&str>) {
    let found = ran(&meta(yaml), &build(&[]), &link);
    assert_eq!(found.map(|change| change.text()).as_deref(), expected);
}

#[rstest]
#[case::succeeded(
    "{lastScheduleTime: \"2026-01-02T10:00:00Z\", lastSuccessfulTime: \"2026-01-02T10:01:00Z\"}",
    "shop/report ran on its schedule and succeeded"
)]
#[case::failed(
    "{lastScheduleTime: \"2026-01-02T10:00:00Z\", lastSuccessfulTime: \"2026-01-02T09:01:00Z\"}",
    "shop/report ran on its schedule and did not succeed"
)]
#[case::running(
    "{lastScheduleTime: \"2026-01-02T10:00:00Z\", active: [{name: report-1}]}",
    "shop/report ran on its schedule and is running"
)]
fn reads_a_cron_jobs_last_run(#[case] status: &str, #[case] expected: &str) {
    let yaml = format!(
        "{{metadata: {{namespace: shop, name: report}}, spec: {{schedule: '* * * * *', jobTemplate: {{}}}}, status: {status}}}"
    );
    let cron_job: CronJob = serde_yaml::from_str(&yaml).unwrap();
    let found = scheduled(&cron_job, &link).unwrap();
    assert_eq!(found.text(), expected);
}

fn node(conditions: &str) -> Node {
    let yaml = format!("{{metadata: {{name: machine-a}}, status: {{conditions: [{conditions}]}}}}");
    serde_yaml::from_str(&yaml).unwrap()
}

const READY: &str = "{type: Ready, status: \"True\", lastTransitionTime: \"2026-01-02T10:00:20Z\"}";
const DISK: &str = "{type: DiskPressure, status: \"False\", reason: KubeletHasNoDiskPressure, lastTransitionTime: \"2026-01-02T10:00:00Z\"}";
const MEMORY: &str =
    "{type: MemoryPressure, status: \"False\", lastTransitionTime: \"2026-01-02T10:00:00Z\"}";
const HOT: &str = "{type: MemoryPressure, status: \"True\", reason: KubeletHasInsufficientMemory, lastTransitionTime: \"2026-01-02T09:00:00Z\"}";
const NOT_READY: &str = "{type: Ready, status: \"Unknown\", reason: NodeStatusUnknown, lastTransitionTime: \"2026-01-02T08:00:00Z\"}";

/// Conditions that changed within a minute of each other, as after a
/// reboot, make one entry.  A condition that changed alone has its own
/// entry.
#[rstest]
#[case::a_reboot(vec![READY, DISK, MEMORY], vec![("10:00", "machine-a became ready; DiskPressure and MemoryPressure became False")])]
#[case::apart(vec![READY, HOT], vec![("09:00", "machine-a: MemoryPressure became True: KubeletHasInsufficientMemory"), ("10:00", "machine-a became ready")])]
#[case::not_ready(vec![NOT_READY], vec![("08:00", "machine-a stopped being ready: NodeStatusUnknown")])]
#[case::never_moved(vec!["{type: PIDPressure, status: \"False\"}"], vec![])]
fn reads_a_nodes_conditions_at_their_last_transitions(
    #[case] conditions: Vec<&str>,
    #[case] expected: Vec<(&str, &str)>,
) {
    let found = super::read::conditions(&node(&conditions.join(", ")));
    let now = at(NOW);
    let found: Vec<(String, String)> = found.iter().map(|c| (c.time(now), c.text())).collect();
    let expected: Vec<(String, String)> = expected
        .into_iter()
        .map(|(a, b)| (a.into(), b.into()))
        .collect();
    assert_eq!(found, expected);
}

#[rstest]
fn reads_a_containers_last_termination_as_a_restart() {
    let pod: Pod = serde_yaml::from_str(
        "{metadata: {namespace: shop, name: web-1}, status: {containerStatuses: [{name: web, image: web, imageID: '', ready: true, restartCount: 1, lastState: {terminated: {exitCode: 137, reason: OOMKilled, finishedAt: \"2026-01-02T10:00:00Z\"}}}, {name: sidecar, image: s, imageID: '', ready: true, restartCount: 0}]}}",
    )
    .unwrap();
    let ownership = build(&[
        (
            "",
            "Pod",
            Some("shop"),
            "web-1",
            vec![("apps/v1", "Deployment", "web")],
        ),
        ("apps", "Deployment", Some("shop"), "web", vec![]),
    ]);
    let found = restarts(&Arc::new(pod), &ownership, &Plurals::default());
    let arranged = arrange(vec![], found, at(NOW));
    assert_eq!(
        listed(&arranged),
        owned(vec![(
            "10:00",
            "shop/web: web in web-1 restarted (OOMKilled, exit 137)",
            vec![]
        )])
    );
}

#[rstest]
fn the_sidebar_leaves_out_scheduled_runs() {
    let report = Object::new("batch", "CronJob", Some("shop"), "report");
    let run = Change {
        at: at("2026-01-02T11:00:00Z"),
        what: What::Scheduled {
            cron_job: report,
            succeeded: Some(true),
        },
        href: None,
        after: Vec::new(),
    };
    let changes = vec![run, rolled("2026-01-02T10:00:00Z", "5")];
    let shown: Vec<String> = for_sidebar(&changes).iter().map(Change::text).collect();
    assert_eq!(shown, vec!["shop/web rolled out revision 5"]);
}

fn revision(name: &str, revision: &str, time: &str) -> crate::gitops::Revision {
    use crate::gitops::fixtures::object;
    crate::gitops::Revision {
        applier: object("Tree", "ops", name),
        source: Some(object("Repo", "ops", "fleet")),
        url: Some("https://github.com/example/fleet".into()),
        revision: revision.into(),
        formats: ["{ref}@sha1:{commit}", "{ref}@*"]
            .map(String::from)
            .to_vec(),
        at: at(time),
    }
}

/// Appliers that applied one revision of one source share one entry,
/// with the commit that the revision names.
#[rstest]
#[case::two_of_one_revision(
    vec![revision("shop", "main@sha1:0a1b2c3d4e", "2026-01-02T10:00:00Z"), revision("blog", "main@sha1:0a1b2c3d4e", "2026-01-02T10:00:05Z")],
    vec![("10:00", "2 Trees applied main from ops/fleet".to_string(), Some("fleet@0a1b2c3"))]
)]
#[case::an_oci_tag(
    vec![revision("shop", "2026.01.01-001@sha256:ffee", "2026-01-02T10:00:00Z")],
    vec![("10:00", "ops/shop applied 2026.01.01-001".to_string(), None)]
)]
fn reads_one_entry_per_revision_applied(
    #[case] revisions: Vec<crate::gitops::Revision>,
    #[case] expected: Vec<(&str, String, Option<&str>)>,
) {
    let found = super::read::applied(
        &revisions,
        &crate::forge::Forges::build(&crate::forge::fixtures::layouts(), []),
        &link,
    );
    let now = at(NOW);
    let found: Vec<(String, String, Option<&str>)> = found
        .iter()
        .map(|c| (c.time(now), c.text(), c.commit().map(|(_, text)| text)))
        .collect();
    let expected: Vec<(String, String, Option<&str>)> = expected
        .into_iter()
        .map(|(time, text, commit)| (time.into(), text, commit))
        .collect();
    assert_eq!(found, expected);
}
