use std::sync::Arc;

use rstest::rstest;

use k8s_openapi::api::core::v1::ContainerStatus;

use super::fixtures::*;
use super::*;

#[rstest]
fn a_namespace_takes_its_worst_level_and_lists_the_worst_first() {
    let deployments = [
        Arc::new(deployment("shop", "web", 2, 1)),
        Arc::new(deployment("shop", "worker", 1, 0)),
        Arc::new(deployment("blog", "web", 1, 1)),
    ];
    let objects = Objects {
        deployments: &deployments,
        stateful_sets: &[],
        daemon_sets: &[],
        cron_jobs: &[],
        pods: &[],
    };
    let health = assess(
        &objects,
        &Ownership::default(),
        &Revisions::default(),
        now(),
    );
    assert_eq!(health.len(), 1);
    let shop = &health["shop"];
    assert_eq!(shop.level, Level::Failing);
    assert_eq!(
        shop.title().unwrap(),
        "worker: none of 1 ready\nweb: 1 of 2 ready"
    );
}

#[rstest]
fn pod_reasons_land_in_the_pods_namespace() {
    let pods = [Arc::new(
        pod("blog", "web-1").pending_since(minutes_before(10)),
    )];
    let objects = Objects {
        deployments: &[],
        stateful_sets: &[],
        daemon_sets: &[],
        cron_jobs: &[],
        pods: &pods,
    };
    let health = assess(
        &objects,
        &Ownership::default(),
        &Revisions::default(),
        now(),
    );
    assert_eq!(health["blog"].level, Level::Warning);
}

fn restarting(restarts: i32, clean: bool) -> Reason {
    Reason::Restarting {
        pod: "web-1".into(),
        container: "app".into(),
        restarts,
        minutes_ago: 3,
        clean,
    }
}

#[rstest]
#[case::a_crash(restarting(1, false), Level::Warning)]
#[case::one_clean_exit(restarting(1, true), Level::Notice)]
#[case::repeated_clean_exits(restarting(2, true), Level::Warning)]
fn a_restart_weighs_by_how_it_ended(#[case] reason: Reason, #[case] level: Level) {
    assert_eq!(reason.level(), level);
}

fn speaker_pod(name: &str, mixer: ContainerStatus) -> Pod {
    pod("audio", name)
        .owned_by("DaemonSet", "speaker")
        .container(mixer)
}

fn restarted() -> ContainerStatus {
    container("mixer").restarted(1, minutes_before(13))
}

fn radio_pod(name: &str, machine: &str) -> Pod {
    pod("audio", name)
        .owned_by("DaemonSet", "radio")
        .created(seconds_before(13 * 60 + 30))
        .on(machine)
}

#[rstest]
fn restarts_that_followed_a_rollout_say_which() {
    let pods = [
        speaker_pod("speaker-a", restarted()).on("m1"),
        speaker_pod("speaker-b", restarted()).on("m2"),
        radio_pod("radio-a", "m1"),
        radio_pod("radio-b", "m2"),
    ];
    let ownership = fixtures::ownership(&pods.iter().collect::<Vec<_>>(), &[]);
    let revisions = Revisions::build(&[revision(
        "radio-5f6d",
        "DaemonSet",
        "radio",
        minutes_before(20),
    )]);
    let pods = pods.map(Arc::new);
    let objects = Objects {
        deployments: &[],
        stateful_sets: &[],
        daemon_sets: &[],
        cron_jobs: &[],
        pods: &pods,
    };
    let health = assess(&objects, &ownership, &revisions, now());
    let notes: Vec<String> = health["audio"]
        .reasons
        .iter()
        .filter_map(Reason::note)
        .collect();
    assert_eq!(
        notes,
        vec!["speaker's mixer restarted on 2 machines right after radio rolled"]
    );
}

#[rstest]
fn a_container_restarted_across_a_workloads_pods_reads_as_one_line() {
    let pods = [
        speaker_pod("speaker-a", restarted()),
        speaker_pod("speaker-b", restarted()),
        speaker_pod("speaker-c", container("mixer")),
    ];
    let ownership = fixtures::ownership(&pods.iter().collect::<Vec<_>>(), &[]);
    let pods = pods.map(Arc::new);
    let objects = Objects {
        deployments: &[],
        stateful_sets: &[],
        daemon_sets: &[],
        cron_jobs: &[],
        pods: &pods,
    };
    let health = assess(&objects, &ownership, &Revisions::default(), now());
    assert_eq!(
        health["audio"].title().unwrap(),
        "mixer restarted in 2 of 3 speaker pods, 13m ago"
    );
}

#[rstest]
fn healthy_has_no_title() {
    assert_eq!(Health::default().title(), None);
}

#[rstest]
#[case::earlier(minutes_before(5), 5)]
#[case::future_reads_as_zero(seconds_before(-300), 0)]
fn minutes_between_now_and_then(#[case] then: Timestamp, #[case] minutes: i64) {
    assert_eq!(minutes_between(then, now()), minutes);
}

fn screen_false(namespace: &str) -> (String, Reason) {
    let player = Object::new(
        "widgets.example.com",
        "Widget",
        Some(namespace),
        "front-room",
    );
    let reason = Reason::Condition {
        workload: player,
        condition: "Screen".into(),
        status: Some("False".into()),
        reason: None,
        message: None,
    };
    (namespace.into(), reason)
}

#[rstest]
fn a_custom_resources_condition_joins_its_namespaces_reasons() {
    let deployments = [Arc::new(deployment("shop", "web", 2, 1))];
    let objects = Objects {
        deployments: &deployments,
        stateful_sets: &[],
        daemon_sets: &[],
        cron_jobs: &[],
        pods: &[],
    };
    let health = assess(
        &objects,
        &Ownership::default(),
        &Revisions::default(),
        now(),
    );
    let health = with_reasons(health, vec![screen_false("shop"), screen_false("blog")]);
    let titles: Vec<(&str, Level, String)> = ["blog", "shop"]
        .iter()
        .map(|namespace| {
            let health = &health[*namespace];
            (*namespace, health.level, health.title().unwrap())
        })
        .collect();
    assert_eq!(
        titles,
        vec![
            ("blog", Level::Failing, "front-room: Screen is False".into()),
            (
                "shop",
                Level::Failing,
                "front-room: Screen is False\nweb: 1 of 2 ready".into()
            ),
        ]
    );
}
