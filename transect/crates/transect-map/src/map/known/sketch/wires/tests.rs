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
        Presence::Empty | Presence::Elsewhere => vec![Reference {
            text: "no Node".into(),
            href: None,
        }],
        Presence::Absent => vec![],
    };
    Placed {
        presence,
        names,
        fields: vec![Reference {
            text: format!("status.{name}"),
            href: None,
        }],
    }
}

/// Each wire of `MILL`, as its runs, each with its ends and whether it
/// is dashed, then its sentence.
fn drawn(sifter: Presence, scoop: Presence) -> Vec<(Vec<String>, String)> {
    let known = knowledge("mills.widgets.example.com", MILL);
    let wires = wires(
        known.elevations[0].sketch().unwrap(),
        "the KindKnowledge mills.widgets.example.com",
        |name| placed(name, sifter, scoop),
    );
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
#[case::to_a_scoop_at_rest(
    Presence::Parts,
    Presence::Elsewhere,
    vec![
        wire(
            &["hopper right 0/1 -> sifter left 0/2 false", "sifter right 0/1 -> bin left 0/1 false"],
            "grain from Node hopper-1 through Node sifter-1 to Node bin-1"
        ),
        wire(&["scoop right 0/1 -> sifter left 1/2 true"], "flour from no Node to Node sifter-1"),
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
    let wires = wires(
        known.elevations[0].sketch().unwrap(),
        "the KindKnowledge mills.widgets.example.com",
        |name| placed(name, Presence::Parts, Presence::Parts),
    );
    let links: Vec<Option<&str>> = wires[0]
        .sentence
        .iter()
        .map(|p| p.href.as_deref())
        .collect();
    assert_eq!(
        (links, wires[0].source_text().as_str()),
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
#[case::solid(Presence::Parts, "section-path")]
#[case::dashed(Presence::Empty, "section-path section-path-dashed")]
#[case::dashed_at_rest(
    Presence::Elsewhere,
    "section-path section-path-dashed sketch-wire-rests"
)]
fn draws_a_run_as_the_role_at_its_end_stands(#[case] presence: Presence, #[case] class: &str) {
    let end = |area: &str, presence| WireEnd {
        area: area.into(),
        presence,
        edge: Edge::Left,
        slot: 0,
        of: 1,
    };
    let run = Run {
        from: end("scoop", Presence::Parts),
        to: end("sifter", presence),
    };
    assert_eq!(run.class(), class);
}
