use rstest::rstest;

use super::super::super::through::{Followed, ReferenceEnds};
use super::super::super::{KindKnowledge, parse};
use super::super::{Cluster, Derived, Judged, Line, Mark, Marked, Said, judge};
use crate::resources::DocId;
use transect_graph::edges::Object;

const STORE: &str = "
metadata: {name: ledgers.ledgers.example.test}
spec:
  references:
    - {path: status.currentPrimary, kind: Pod}
    - {path: 'status.instances[]', kind: Pod}
    - {path: status.targetPrimary, kind: Pod}
    - {name: '{name}-rw', kind: Service}
    - {name: '{name}-ro', kind: Service}
  controller: {image: example.test/ledger-operator}
  pattern:
    name: primary-with-replicas
    roles:
      instances:     {reference: 'status.instances[]'}
      primary:       {reference: status.currentPrimary}
      target:        {reference: status.targetPrimary}
      write-service: {reference: '{name}-rw'}
      read-service:  {reference: '{name}-ro'}
";

fn knowledge() -> KindKnowledge {
    parse(DocId::default(), serde_yaml::from_str(STORE).unwrap()).unwrap()
}

fn pod(name: &str) -> Object {
    Object::new("", "Pod", Some("books"), name)
}

fn service(name: &str) -> Object {
    Object::new("", "Service", Some("books"), name)
}

fn ledger() -> Object {
    Object::new("ledgers.example.test", "Ledger", Some("books"), "orders")
}

/// A store `orders` as a cluster has it: its instances, the primary
/// and the target that its status names, which pods are ready, and the
/// pods behind each `Service`.
struct Lab {
    instances: Vec<&'static str>,
    primary: Option<&'static str>,
    target: Option<&'static str>,
    unready: Vec<&'static str>,
    gone: Vec<&'static str>,
    writes: Option<Vec<&'static str>>,
    reads: Option<Vec<&'static str>>,
    controller: bool,
}

/// Three instances, every one ready, with the primary `orders-1`.
fn three() -> Lab {
    Lab {
        instances: vec!["orders-1", "orders-2", "orders-3"],
        primary: Some("orders-1"),
        target: Some("orders-1"),
        unready: Vec::new(),
        gone: Vec::new(),
        writes: Some(vec!["orders-1"]),
        reads: Some(vec!["orders-2", "orders-3"]),
        controller: true,
    }
}

/// One instance, which is the primary.
fn one() -> Lab {
    Lab {
        instances: vec!["orders-1"],
        reads: Some(Vec::new()),
        ..three()
    }
}

impl ReferenceEnds for Lab {
    fn ends(&self, _: &Object, path: &str) -> Vec<Followed<'_>> {
        let named: Vec<Object> = match path {
            "status.currentPrimary" => self.primary.into_iter().map(pod).collect(),
            "status.instances[]" => self.instances.iter().map(|name| pod(name)).collect(),
            "status.targetPrimary" => self.target.into_iter().map(pod).collect(),
            "{name}-rw" => vec![service("orders-rw")],
            "{name}-ro" => vec![service("orders-ro")],
            _ => Vec::new(),
        };
        named.into_iter().map(Followed::Unwatched).collect()
    }
}

impl Cluster for Lab {
    fn controller(&self, _: &Object) -> Option<Object> {
        let operator = Object::new("apps", "Deployment", Some("ops"), "ledger-operator");
        self.controller.then_some(operator)
    }

    fn ready(&self, pod: &Object) -> Option<bool> {
        let name = pod.name.as_str();
        (!self.gone.contains(&name)).then(|| !self.unready.contains(&name))
    }

    fn endpoints(&self, service: &Object) -> Option<Vec<Object>> {
        let reached = match service.name.as_str() {
            "orders-rw" => self.writes.as_ref(),
            _ => self.reads.as_ref(),
        };
        reached.map(|names| names.iter().map(|name| pod(name)).collect())
    }
}

fn judged(lab: &Lab) -> Judged {
    let known = knowledge();
    judge(&known, &ledger(), lab).unwrap()
}

#[rstest]
#[case::the_instances_but_the_primary(three(), Derived::Parts(vec![pod("orders-2"), pod("orders-3")]))]
#[case::one_instance(one(), Derived::Nothing("status.instances[] lists only the primary".into()))]
#[case::no_instance(
    Lab { instances: Vec::new(), ..one() },
    Derived::Nothing("status.instances[] names no instance".into())
)]
fn derives_the_replicas(#[case] lab: Lab, #[case] expected: Derived) {
    assert_eq!(judged(&lab).derived.of("replicas"), Some(&expected));
}

#[rstest]
#[case::its_controller(
    three(),
    Derived::Parts(vec![Object::new("apps", "Deployment", Some("ops"), "ledger-operator")])
)]
#[case::no_controller(
    Lab { controller: false, ..three() },
    Derived::Missing("nothing that runs example.test/ledger-operator controls Ledger orders".into())
)]
fn derives_the_promoter(#[case] lab: Lab, #[case] expected: Derived) {
    assert_eq!(judged(&lab).derived.of("promoter"), Some(&expected));
}

/// Each line as what it expects, what the cluster says, and its detail.
fn lines(judged: &Judged) -> Vec<(String, Said, String)> {
    let line = |line: &Line| (line.expects.clone(), line.said, line.detail_text());
    judged.lines.iter().map(line).collect()
}

fn owned(expected: Vec<(&str, Said, &str)>) -> Vec<(String, Said, String)> {
    let line = |(expects, said, detail): (&str, Said, &str)| (expects.into(), said, detail.into());
    expected.into_iter().map(line).collect()
}

const PRIMARY: &str = "the primary is ready, and it is the operator's target";
const WRITES: &str = "writes reach only the primary";
const REPLICAS: &str = "each replica is ready";
const READS: &str = "reads reach only replicas";

#[rstest]
#[case::three_that_are_well(three(), vec![
    (PRIMARY, Said::Met, "orders-1"),
    (WRITES, Said::Met, "orders-rw reaches orders-1"),
    (REPLICAS, Said::Met, "2 of 2"),
    (READS, Said::Met, "orders-ro reaches orders-2 and orders-3"),
])]
#[case::one_instance(one(), vec![
    (PRIMARY, Said::Met, "orders-1"),
    (WRITES, Said::Met, "orders-rw reaches orders-1"),
    (REPLICAS, Said::Moot, "does not apply, no replica"),
    (READS, Said::Moot, "does not apply, no replica"),
])]
#[case::a_primary_that_moves(Lab { target: Some("orders-2"), ..three() }, vec![
    (PRIMARY, Said::Unmet, "the primary is orders-1, and the operator's target is orders-2"),
    (WRITES, Said::Met, "orders-rw reaches orders-1"),
    (REPLICAS, Said::Met, "2 of 2"),
    (READS, Said::Met, "orders-ro reaches orders-2 and orders-3"),
])]
#[case::a_primary_that_is_not_ready(Lab { unready: vec!["orders-1"], ..one() }, vec![
    (PRIMARY, Said::Unmet, "orders-1 is not ready"),
    (WRITES, Said::Met, "orders-rw reaches orders-1"),
    (REPLICAS, Said::Moot, "does not apply, no replica"),
    (READS, Said::Moot, "does not apply, no replica"),
])]
#[case::a_primary_that_is_gone(Lab { gone: vec!["orders-1"], writes: Some(Vec::new()), ..one() }, vec![
    (PRIMARY, Said::Unmet, "the cluster has no Pod orders-1"),
    (WRITES, Said::Unmet, "orders-rw has no ready endpoint"),
    (REPLICAS, Said::Moot, "does not apply, no replica"),
    (READS, Said::Moot, "does not apply, no replica"),
])]
#[case::a_primary_that_is_no_instance(Lab { primary: Some("orders-9"), target: Some("orders-9"), writes: Some(vec!["orders-9"]), ..three() }, vec![
    (PRIMARY, Said::Unmet, "orders-9 is not one of the instances"),
    (WRITES, Said::Met, "orders-rw reaches orders-9"),
    (REPLICAS, Said::Met, "3 of 3"),
    (READS, Said::Met, "orders-ro reaches orders-2 and orders-3"),
])]
#[case::no_primary(Lab { primary: None, writes: Some(vec!["orders-2"]), ..three() }, vec![
    (PRIMARY, Said::NoSignal, "status.currentPrimary names nothing now"),
    (WRITES, Said::Unmet, "orders-rw also reaches orders-2"),
    (REPLICAS, Said::Met, "3 of 3"),
    (READS, Said::Met, "orders-ro reaches orders-2 and orders-3"),
])]
#[case::writes_that_reach_a_replica(Lab { writes: Some(vec!["orders-1", "orders-2"]), ..three() }, vec![
    (PRIMARY, Said::Met, "orders-1"),
    (WRITES, Said::Unmet, "orders-rw also reaches orders-2"),
    (REPLICAS, Said::Met, "2 of 2"),
    (READS, Said::Met, "orders-ro reaches orders-2 and orders-3"),
])]
#[case::writes_that_miss_the_primary(Lab { writes: Some(vec!["orders-2"]), ..three() }, vec![
    (PRIMARY, Said::Met, "orders-1"),
    (WRITES, Said::Unmet, "orders-rw does not reach the primary orders-1"),
    (REPLICAS, Said::Met, "2 of 2"),
    (READS, Said::Met, "orders-ro reaches orders-2 and orders-3"),
])]
#[case::no_service_for_writes(Lab { writes: None, reads: None, ..three() }, vec![
    (PRIMARY, Said::Met, "orders-1"),
    (WRITES, Said::Unmet, "the cluster has no Service orders-rw"),
    (REPLICAS, Said::Met, "2 of 2"),
    (READS, Said::Unmet, "the cluster has no Service orders-ro"),
])]
#[case::two_replicas_not_ready(Lab { unready: vec!["orders-2", "orders-3"], reads: Some(vec!["orders-1"]), ..three() }, vec![
    (PRIMARY, Said::Met, "orders-1"),
    (WRITES, Said::Met, "orders-rw reaches orders-1"),
    (REPLICAS, Said::Unmet, "0 of 2, and orders-2 and orders-3 are not ready"),
    (READS, Said::Unmet, "orders-ro also reaches orders-1"),
])]
#[case::one_replica_not_ready(Lab { unready: vec!["orders-3"], reads: Some(Vec::new()), ..three() }, vec![
    (PRIMARY, Said::Met, "orders-1"),
    (WRITES, Said::Met, "orders-rw reaches orders-1"),
    (REPLICAS, Said::Unmet, "1 of 2, and orders-3 is not ready"),
    (READS, Said::Unmet, "orders-ro has no ready endpoint"),
])]
fn says_whether_the_store_is_as_the_pattern_expects(
    #[case] lab: Lab,
    #[case] expected: Vec<(&str, Said, &str)>,
) {
    assert_eq!(lines(&judged(&lab)), owned(expected));
}

const UNFILLED: &str = "
metadata: {name: ledgers.ledgers.example.test}
spec:
  references:
    - {path: status.currentPrimary, kind: Pod}
    - {path: 'status.instances[]', kind: Pod}
  pattern:
    name: primary-with-replicas
    roles:
      instances: {reference: 'status.instances[]'}
      primary:   {reference: status.currentPrimary}
";

#[rstest]
fn says_what_a_kind_that_fills_no_service_leaves_without_a_signal() {
    let known = parse(DocId::default(), serde_yaml::from_str(UNFILLED).unwrap()).unwrap();
    let judged = judge(&known, &ledger(), &three()).unwrap();
    assert_eq!(
        lines(&judged),
        owned(vec![
            (
                PRIMARY,
                Said::Met,
                "orders-1, and the KindKnowledge names no target"
            ),
            (
                WRITES,
                Said::NoSignal,
                "the KindKnowledge names no Service for writes"
            ),
            (REPLICAS, Said::Met, "2 of 2"),
            (
                READS,
                Said::NoSignal,
                "the KindKnowledge names no Service for reads"
            ),
        ])
    );
}

fn mark(object: Object, marked: Marked, why: &str) -> Mark {
    Mark {
        object,
        marked,
        why: why.into(),
    }
}

#[rstest]
#[case::none_on_a_store_that_is_well(three(), vec![])]
#[case::the_parts_that_serve_no_replica(one(), vec![
    mark(service("orders-ro"), Marked::Rests, "selects the replicas, and there is none"),
    mark(Object::new("apps", "Deployment", Some("ops"), "ledger-operator"), Marked::Rests, "no replica to promote"),
])]
#[case::each_part_that_an_unmet_line_names(
    Lab { target: Some("orders-2"), writes: Some(vec!["orders-1", "orders-3"]), unready: vec!["orders-3"], ..three() },
    vec![
        mark(pod("orders-1"), Marked::Unmet, "not the operator's target"),
        mark(service("orders-rw"), Marked::Unmet, "reaches more than the primary"),
        mark(pod("orders-3"), Marked::Unmet, "not ready"),
    ]
)]
#[case::a_service_that_reaches_nothing(
    Lab { reads: None, ..three() },
    vec![mark(service("orders-ro"), Marked::Unmet, "no such Service")]
)]
fn marks_the_parts_that_its_lines_name(#[case] lab: Lab, #[case] expected: Vec<Mark>) {
    assert_eq!(judged(&lab).marks.all(), expected);
}

#[rstest]
fn names_the_fields_each_line_read() {
    let sources: Vec<String> = judged(&three())
        .lines
        .into_iter()
        .map(|l| l.source)
        .collect();
    assert_eq!(
        sources,
        vec![
            "from status.currentPrimary, status.instances[], and status.targetPrimary, as transect's pattern expects",
            "from orders-rw and status.currentPrimary, as transect's pattern expects",
            "from status.instances[] and status.currentPrimary, as transect's pattern expects",
            "from orders-ro and status.instances[], as transect's pattern expects",
        ]
    );
}

#[rstest]
fn finds_the_mark_on_a_part() {
    let judged = judged(&one());
    let found = judged
        .marks
        .of(&service("orders-ro"))
        .map(|mark| mark.marked);
    assert_eq!(
        (found, judged.marks.of(&pod("orders-1"))),
        (Some(Marked::Rests), None)
    );
}

#[rstest]
fn rests_a_promoter_that_no_knowledge_names() {
    let known = parse(DocId::default(), serde_yaml::from_str(UNFILLED).unwrap()).unwrap();
    let lab = Lab {
        controller: false,
        ..three()
    };
    let judged = judge(&known, &ledger(), &lab).unwrap();
    assert_eq!(
        judged.derived.of("promoter"),
        Some(&Derived::Nothing(
            "the KindKnowledge names no controller of Ledger".into()
        ))
    );
}
