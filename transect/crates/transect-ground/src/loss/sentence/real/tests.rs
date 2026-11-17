use rstest::rstest;

use super::*;
use crate::loss::{Counted, Gone, Lost, Majority, Next};

/// The sidebar for the loss of `machines`, with `pods` pods.  The
/// machines in `gone` are really lost, each for the seconds given.
fn sidebar(machines: &[&str], gone: &[(&str, Option<i64>)], pods: usize) -> Sidebar {
    let gone: Vec<Gone> = gone
        .iter()
        .map(|(machine, seconds)| Gone {
            machine: (*machine).into(),
            seconds: *seconds,
        })
        .collect();
    Sidebar {
        machines: Lost::of(machines.iter().copied(), machines.iter().copied()),
        pods,
        stuck: Vec::new(),
        back: Vec::new(),
        vital: Vec::new(),
        primary: Vec::new(),
        short: Vec::new(),
        folded: 0,
        daemon_sets: 0,
        cascade: Vec::new(),
        unreconciled: Vec::new(),
        plurals: Default::default(),
        next: Next::default(),
        gone: Vec::new(),
        majority: None,
        unknown: None,
        blind: None,
        alerting: None,
        linker: Default::default(),
    }
    .with_gone(&gone)
}

#[rstest]
#[case::supposed(
    sidebar(&["alpha"], &[], 3),
    ["If alpha were lost", "if alpha were lost", "3 pods run there."],
    None
)]
#[case::really(
    sidebar(&["alpha"], &[("alpha", Some(200))], 3),
    ["alpha is lost · 3 minutes", "alpha is lost", "3 pods were running there."],
    None
)]
#[case::just_now(
    sidebar(&["alpha"], &[("alpha", Some(20))], 1),
    ["alpha is lost · under a minute", "alpha is lost", "1 pod was running there."],
    None
)]
#[case::two_really_by_the_longest(
    sidebar(&["alpha", "beta"], &[("alpha", Some(200)), ("beta", Some(700))], 2),
    ["alpha and beta are lost · 11 minutes", "alpha and beta are lost", "2 pods were running there."],
    None
)]
#[case::a_question_on_top(
    sidebar(&["alpha", "beta"], &[("beta", Some(200))], 2),
    ["If alpha were lost too", "if alpha were lost too", "2 pods run there."],
    Some("beta is already lost · 3 minutes.")
)]
#[case::gone_elsewhere_is_left_out(
    sidebar(&["alpha"], &[("beta", Some(200))], 2),
    ["If alpha were lost", "if alpha were lost", "2 pods run there."],
    None
)]
fn the_head_says_whether_a_machine_is_really_lost(
    #[case] sidebar: Sidebar,
    #[case] head: [&str; 3],
    #[case] gone: Option<&str>,
) {
    assert_eq!(
        (
            [sidebar.title(), sidebar.crumb(), sidebar.summary()],
            sidebar.gone_text()
        ),
        (head.map(String::from), gone.map(String::from))
    );
}

#[rstest]
#[case::none(None, None)]
#[case::kept(
    Some(Majority { remain: 2, of: 3, counted: Counted::ControlPlane }),
    Some("2 of 3 control-plane machines remain, which keeps a majority (a guess).")
)]
#[case::lost(
    Some(Majority { remain: 1, of: 3, counted: Counted::ControlPlane }),
    Some("1 of 3 control-plane machines remains, which loses the majority (a guess).")
)]
#[case::half_is_no_majority(
    Some(Majority { remain: 2, of: 4, counted: Counted::ControlPlane }),
    Some("2 of 4 control-plane machines remain, which loses the majority (a guess).")
)]
#[case::counted_from_etcd_labels(
    Some(Majority { remain: 4, of: 5, counted: Counted::Etcd }),
    Some("4 of 5 etcd members remain, which keeps a majority.")
)]
fn the_majority_line_is_marked_a_guess_unless_etcd_labels_count_it(
    #[case] majority: Option<Majority>,
    #[case] text: Option<&str>,
) {
    let sidebar = sidebar(&["alpha"], &[], 0).with_majority(majority);
    assert_eq!(sidebar.majority_text().as_deref(), text);
}

#[rstest]
#[case::still_waiting(Some(100), ("+3m", "Its pods are evicted in about 3 minutes, when the 5 minutes their tolerations allow run out, then the replicas that can start elsewhere do."))]
#[case::evicted(Some(400), ("done", "Its pods were evicted after the 5 minutes their tolerations allow, and the replicas that can start elsewhere do."))]
#[case::since_unknown(None, ("+5m", "Its pods are evicted after the 5 minutes their tolerations allow, then the replicas that can start elsewhere do."))]
fn a_real_loss_counts_the_eviction_from_when_it_began(
    #[case] seconds: Option<i64>,
    #[case] expected: (&str, &str),
) {
    let mut sidebar = sidebar(&["alpha"], &[("alpha", seconds)], 1);
    sidebar.next.usual = Some(crate::placement::Eviction::After(300));
    let (tag, text) = sidebar.eviction().unwrap();
    assert_eq!((tag.as_str(), text.as_str()), expected);
}

#[rstest]
#[case::supposed(false, None, ("if lost", "4 pods run here"))]
#[case::really(true, Some(3), ("Lost 3m", "4 pods were here"))]
#[case::since_unknown(true, None, ("Lost", "4 pods were here"))]
fn a_lost_strip(
    #[case] really_lost: bool,
    #[case] lost_minutes: Option<i64>,
    #[case] expected: (&str, &str),
) {
    let machine = Machine {
        name: "alpha".into(),
        control_plane: false,
        etcd: false,
        ready: !really_lost,
        allocatable: Default::default(),
        requested: Default::default(),
        used: Default::default(),
        pods: 4,
        here: None,
        lost: true,
        really_lost,
        lost_minutes,
        held: Vec::new(),
        pod_memory: Vec::new(),
        alerted: None,
    };
    let found = (machine.lost_tag(), machine.lost_text());
    assert_eq!((found.0.as_str(), found.1.as_str()), expected);
}
