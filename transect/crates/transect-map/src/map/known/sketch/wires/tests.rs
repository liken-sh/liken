use rstest::rstest;

use super::*;
use transect_resources::knowledge::fixtures::knowledge;

/// A mill: grain from the hopper to the bin, through the sifter when it
/// has a part, and flour from the scoop to the sifter.
const MILL: &str = "{references: [{path: spec.a, kind: Node}],
    roles: {hopper: {reference: spec.a}, sifter: {reference: spec.a}, bin: {reference: spec.a}, scoop: {reference: spec.a}},
    elevations: [{name: the mill, question: Where does the grain go?,
      sketch: \"hopper sifter bin\\nscoop sifter .\",
      wires: [{from: hopper, to: bin, via: sifter, carries: grain}, {from: scoop, to: sifter, carries: flour}]}]}";

/// How the page draws `name` when the sifter is `sifter`, the scoop is
/// `scoop`, and every other role has one part, named for the role.
fn placed(name: &str, sifter: Presence, scoop: Presence) -> Placed {
    let presence = match name {
        "sifter" => sifter,
        "scoop" => scoop,
        _ => Presence::Parts,
    };
    let names = match presence {
        Presence::Parts => vec![Reference {
            text: format!("Node {name}-1"),
            href: Some(format!("/machines/{name}-1")),
        }],
        Presence::Empty => vec![Reference {
            text: "no Node".into(),
            href: None,
        }],
        Presence::Absent => vec![],
    };
    Placed {
        presence,
        names,
        path: format!("status.{name}"),
    }
}

/// Each wire of `MILL`, as its runs, each with its ends and whether it
/// is dashed, then its sentence.
fn drawn(sifter: Presence, scoop: Presence) -> Vec<(Vec<String>, String)> {
    let known = knowledge("mills.widgets.example.com", MILL);
    let wires = wires(&known.elevations[0], "mills.widgets.example.com", |name| {
        placed(name, sifter, scoop)
    });
    let end = |end: &WireEnd| format!("{} {} {}/{}", end.area, end.edge.word(), end.slot, end.of);
    let run = |run: &Run| format!("{} -> {} {}", end(&run.from), end(&run.to), run.dashed());
    wires
        .iter()
        .map(|w| (w.runs.iter().map(run).collect(), w.text()))
        .collect()
}

fn wire(runs: &[&str], sentence: &str) -> (Vec<String>, String) {
    (
        runs.iter().map(|run| run.to_string()).collect(),
        sentence.into(),
    )
}

#[rstest]
#[case::through_a_sifter(
    Presence::Parts,
    Presence::Parts,
    vec![
        wire(
            &["hopper right 0/1 -> sifter left 0/2 false", "sifter right 0/1 -> bin left 0/1 false"],
            "grain from Node hopper-1 through Node sifter-1 to Node bin-1"
        ),
        wire(&["scoop right 0/1 -> sifter left 1/2 false"], "flour from Node scoop-1 to Node sifter-1"),
    ]
)]
#[case::past_an_empty_sifter(
    Presence::Empty,
    Presence::Parts,
    vec![
        wire(&["hopper right 0/1 -> bin left 0/1 false"], "grain from Node hopper-1 to Node bin-1"),
        wire(&["scoop right 0/1 -> sifter left 0/1 true"], "flour from Node scoop-1 to no Node"),
    ]
)]
#[case::past_a_sifter_left_out(
    Presence::Absent,
    Presence::Parts,
    vec![wire(&["hopper right 0/1 -> bin left 0/1 false"], "grain from Node hopper-1 to Node bin-1")]
)]
#[case::from_a_scoop_left_out(
    Presence::Parts,
    Presence::Absent,
    vec![wire(
        &["hopper right 0/1 -> sifter left 0/1 false", "sifter right 0/1 -> bin left 0/1 false"],
        "grain from Node hopper-1 through Node sifter-1 to Node bin-1"
    )]
)]
fn draws_each_wire_by_the_parts_of_the_roles_it_joins(
    #[case] sifter: Presence,
    #[case] scoop: Presence,
    #[case] expected: Vec<(Vec<String>, String)>,
) {
    assert_eq!(drawn(sifter, scoop), expected);
}

/// The sentence links each part to its page, and the source names each
/// field the wire was read from and the knowledge that states it.
#[rstest]
fn names_the_parts_and_the_source_of_a_wire() {
    let known = knowledge("mills.widgets.example.com", MILL);
    let wires = wires(&known.elevations[0], "mills.widgets.example.com", |name| {
        placed(name, Presence::Parts, Presence::Parts)
    });
    let links: Vec<Option<&str>> = wires[0]
        .sentence
        .iter()
        .map(|p| p.href.as_deref())
        .collect();
    assert_eq!(
        (links, wires[0].source.as_str()),
        (
            vec![
                None,
                None,
                Some("/machines/hopper-1"),
                None,
                Some("/machines/sifter-1"),
                None,
                Some("/machines/bin-1")
            ],
            "read from status.hopper, status.sifter, and status.bin, as the KindKnowledge mills.widgets.example.com states"
        )
    );
}

#[rstest]
#[case::one(&["a"], "a")]
#[case::two(&["a", "b"], "a and b")]
#[case::three(&["a", "b", "c"], "a, b, and c")]
fn lists_the_parts_of_a_role_in_a_sentence(#[case] names: &[&str], #[case] expected: &str) {
    let names: Vec<Reference> = names
        .iter()
        .map(|name| Reference {
            text: name.to_string(),
            href: None,
        })
        .collect();
    let text: String = listed(&names).iter().map(|p| p.text.as_str()).collect();
    assert_eq!(text, expected);
}

#[rstest]
#[case::solid(false, "section-path")]
#[case::dashed(true, "section-path section-path-dashed")]
fn draws_a_run_to_a_role_with_no_part_dashed(#[case] empty: bool, #[case] class: &str) {
    let end = |area: &str, empty| WireEnd {
        area: area.into(),
        empty,
        edge: Edge::Left,
        slot: 0,
        of: 1,
    };
    let run = Run {
        from: end("scoop", false),
        to: end("sifter", empty),
    };
    assert_eq!(run.class(), class);
}
