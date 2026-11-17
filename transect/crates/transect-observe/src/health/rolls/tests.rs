use rstest::rstest;

use super::super::fixtures::*;
use super::*;

fn workload(name: &str) -> Object {
    Object::new("apps", "DaemonSet", Some("audio"), name)
}

/// A restart of `speaker`'s mixer on `machine`, `seconds` past noon.
fn mixer(machine: &str, seconds: i64) -> Restart {
    Restart {
        workload: workload("speaker"),
        container: "mixer".into(),
        machine: machine.into(),
        at: seconds_before(-seconds),
    }
}

/// A step of `name`'s rollout onto `machine`, `seconds` past noon.
fn step(name: &str, machine: &str, seconds: i64) -> Rolled {
    Rolled {
        workload: workload(name),
        machine: machine.into(),
        at: seconds_before(-seconds),
    }
}

fn after(rolled: &str, machines: usize) -> Option<AfterRoll> {
    Some(AfterRoll {
        rolled: rolled.into(),
        machines,
    })
}

#[rstest]
#[case::follows_on_two_machines(
    vec![mixer("m1", 30), mixer("m2", 200)],
    vec![step("radio", "m1", 0), step("radio", "m2", 150)],
    after("radio", 2)
)]
#[case::one_machine_is_chance(
    vec![mixer("m1", 30), mixer("m2", 200)],
    vec![step("radio", "m1", 0)],
    None
)]
#[case::too_long_after(
    vec![mixer("m1", 121), mixer("m2", 400)],
    vec![step("radio", "m1", 0), step("radio", "m2", 150)],
    None
)]
#[case::before_the_rollout(
    vec![mixer("m1", -10), mixer("m2", -10)],
    vec![step("radio", "m1", 0), step("radio", "m2", 0)],
    None
)]
#[case::a_different_machine(
    vec![mixer("m1", 30), mixer("m2", 30)],
    vec![step("radio", "m3", 0), step("radio", "m4", 0)],
    None
)]
#[case::its_own_rollout_is_not_a_cause(
    vec![mixer("m1", 30), mixer("m2", 30)],
    vec![step("speaker", "m1", 0), step("speaker", "m2", 0)],
    None
)]
#[case::the_most_machines_wins(
    vec![mixer("m1", 30), mixer("m2", 30), mixer("m3", 30)],
    vec![
        step("clock", "m1", 0),
        step("clock", "m2", 0),
        step("radio", "m1", 10),
        step("radio", "m2", 10),
        step("radio", "m3", 10),
    ],
    after("radio", 3)
)]
fn restarts_that_follow_a_rollout(
    #[case] restarts: Vec<Restart>,
    #[case] rolled: Vec<Rolled>,
    #[case] expected: Option<AfterRoll>,
) {
    let found = after_rolls(&restarts, &rolled);
    let key = (workload("speaker"), "mixer".to_owned());
    assert_eq!(found.get(&key).cloned(), expected);
}

fn revisions() -> Revisions {
    Revisions::build(&[
        revision("radio-old", "DaemonSet", "radio", minutes_before(600)),
        revision("radio-new", "DaemonSet", "radio", minutes_before(10)),
        revision("web-5d8f7c6b9", "Deployment", "web", minutes_before(10)),
    ])
}

#[rstest]
#[case::a_daemon_sets_new_pod(
    pod("audio", "radio-a").owned_by("DaemonSet", "radio").created(minutes_before(5)).on("m1"),
    Some(minutes_before(5))
)]
#[case::a_daemon_sets_old_pod(
    pod("audio", "radio-a").owned_by("DaemonSet", "radio").created(minutes_before(20)).on("m1"),
    None
)]
#[case::a_deployments_new_pod(
    deployment_pod("audio", "web", minutes_before(3)).on("m1"),
    Some(minutes_before(3))
)]
#[case::a_pod_on_no_machine(
    pod("audio", "radio-a").owned_by("DaemonSet", "radio").created(minutes_before(5)),
    None
)]
fn a_pod_of_the_newest_revision_is_a_step_of_its_rollout(
    #[case] pod: Pod,
    #[case] at: Option<Timestamp>,
) {
    let rolled = rolled_of(&pod, &workload("radio"), &revisions());
    assert_eq!(rolled.map(|rolled| rolled.at), at);
}

#[rstest]
fn a_restarted_container_stopped_when_its_last_run_ended() {
    let pod = pod("audio", "speaker-a")
        .container(container("mixer").restarted(1, minutes_before(2)))
        .on("m1");
    assert_eq!(
        restarts_of(&pod, &workload("speaker")),
        vec![Restart {
            at: minutes_before(2),
            ..mixer("m1", 0)
        }]
    );
}
