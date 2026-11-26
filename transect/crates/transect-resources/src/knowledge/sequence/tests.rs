use rstest::rstest;

use super::super::elevation::Form;
use super::super::{KindKnowledge, parse};
use super::{Sequence, Step, StepState};
use crate::resources::DocId;

const ROLES: &str = "references:
    - {path: spec.bell, kind: Bell, group: widgets.example.com}
    - {path: 'spec.ropes[]', kind: Rope, group: widgets.example.com}
  roles:
    bell: {reference: spec.bell}
    ropes: {reference: 'spec.ropes[]', stack: true}
    keeper: {controller: true}";

/// The knowledge of a kind with `ROLES` and one elevation whose
/// sequence `sequence` writes.
fn read(sequence: &str) -> Result<KindKnowledge, String> {
    let text = format!(
        "metadata: {{name: towers.widgets.example.com}}\nspec:\n  {ROLES}\n  elevations:\n    - name: the peal\n      question: Who rings what?\n      sequence: {sequence}\n"
    );
    parse(DocId::default(), serde_yaml_ng::from_str(&text).unwrap())
}

fn sequence(known: &KindKnowledge) -> &Sequence {
    match &known.elevations[0].form {
        Form::Sequence(sequence) => sequence,
        other => panic!("not a sequence: {other:?}"),
    }
}

#[rstest]
fn reads_the_lanes_and_the_steps_in_the_authors_order() {
    let known = read(
        "{lanes: [keeper, ropes, bell], presents: ropes, steps: [
          {from: keeper, to: ropes, verb: ' pulls each rope ', state: {role: ropes, columns: [Length]}},
          {from: ropes, to: bell, verb: swings the bell, state: {role: bell, condition: Hung}},
          {from: keeper, to: keeper, verb: rests while the bell swings, state: {role: bell, condition: Cracked, good: 'False'}},
          {from: bell, to: keeper, verb: rings}]}",
    )
    .unwrap();
    let step = |from: &str, to: &str, verb: &str, state| Step {
        from: from.into(),
        to: to.into(),
        verb: verb.into(),
        state,
    };
    let condition = |condition: &str, good: &str| StepState::Condition {
        role: "bell".into(),
        condition: condition.into(),
        good: good.into(),
        said: None,
    };
    let columns = StepState::Columns {
        role: "ropes".into(),
        columns: vec!["Length".into()],
    };
    assert_eq!(
        sequence(&known),
        &Sequence {
            lanes: vec!["keeper".into(), "ropes".into(), "bell".into()],
            steps: vec![
                step("keeper", "ropes", "pulls each rope", Some(columns)),
                step(
                    "ropes",
                    "bell",
                    "swings the bell",
                    Some(condition("Hung", "True"))
                ),
                step(
                    "keeper",
                    "keeper",
                    "rests while the bell swings",
                    Some(condition("Cracked", "False"))
                ),
                step("bell", "keeper", "rings", None),
            ],
            presents: Some("ropes".into()),
        }
    );
}

#[rstest]
fn places_its_lanes() {
    let known = read("{lanes: [keeper, bell]}").unwrap();
    assert_eq!(known.elevations[0].names(), vec!["keeper", "bell"]);
}

#[rstest]
#[case::no_lanes("{lanes: []}", "elevations[0]: the sequence has no lanes")]
#[case::a_lane_that_is_not_a_role(
    "{lanes: [bell, tower]}",
    "elevations[0]: the sequence has the lane tower, and spec.roles has no tower"
)]
#[case::a_lane_twice(
    "{lanes: [bell, bell]}",
    "elevations[0]: the sequence has the lane bell twice"
)]
#[case::presents_from_no_lane(
    "{lanes: [bell], presents: ropes}",
    "elevations[0]: presents is ropes, which is not a lane of the sequence"
)]
#[case::an_end_that_is_not_a_lane(
    "{lanes: [bell], steps: [{from: bell, to: ropes, verb: pulls}]}",
    "elevations[0]: steps[0]: ropes is not a lane of the sequence"
)]
#[case::a_verb_with_no_words(
    "{lanes: [bell], steps: [{from: bell, to: bell, verb: ' '}]}",
    "elevations[0]: steps[0]: verb has no words"
)]
#[case::a_state_of_no_role(
    "{lanes: [bell], steps: [{from: bell, to: bell, verb: rings, state: {role: tower, condition: Up}}]}",
    "elevations[0]: steps[0]: state reads the role tower, and spec.roles has no tower"
)]
#[case::a_state_with_both(
    "{lanes: [bell], steps: [{from: bell, to: bell, verb: rings, state: {role: bell, condition: Up, columns: [Length]}}]}",
    "elevations[0]: steps[0]: state has one of condition and columns"
)]
#[case::a_state_with_neither(
    "{lanes: [bell], steps: [{from: bell, to: bell, verb: rings, state: {role: bell}}]}",
    "elevations[0]: steps[0]: state has one of condition and columns"
)]
#[case::good_with_columns(
    "{lanes: [bell], steps: [{from: bell, to: bell, verb: rings, state: {role: bell, columns: [Length], good: 'False'}}]}",
    "elevations[0]: steps[0]: state has good and columns"
)]
#[case::no_columns(
    "{lanes: [bell], steps: [{from: bell, to: bell, verb: rings, state: {role: bell, columns: []}}]}",
    "elevations[0]: steps[0]: state has an empty list of columns"
)]
fn refuses_a_sequence_it_cannot_draw(#[case] sequence: &str, #[case] error: &str) {
    let found = read(sequence).unwrap_err();
    assert!(found.contains(error), "{found}");
}

#[rstest]
#[case::a_sketch_and_a_sequence(
    "sketch: bell\n      sequence: {lanes: [bell]}",
    "elevations[0]: the elevation has more than one of sketch, sequence, and rows"
)]
#[case::no_form(
    "align: top",
    "elevations[0]: the elevation has none of sketch, sequence, and rows"
)]
#[case::wires_on_a_sequence(
    "sequence: {lanes: [bell, ropes]}\n      wires: [{from: bell, to: ropes, carries: sound}]",
    "elevations[0]: the elevation has wires, and only a sketch has wires"
)]
#[case::align_on_a_sequence(
    "sequence: {lanes: [bell]}\n      align: top",
    "elevations[0]: the elevation has align, and only a sketch aligns its parts"
)]
fn refuses_an_elevation_of_more_or_less_than_one_form(#[case] form: &str, #[case] error: &str) {
    let text = format!(
        "metadata: {{name: towers.widgets.example.com}}\nspec:\n  {ROLES}\n  elevations:\n    - name: the peal\n      question: Who rings what?\n      {form}\n"
    );
    let found = parse(DocId::default(), serde_yaml_ng::from_str(&text).unwrap()).unwrap_err();
    assert!(found.contains(error), "{found}");
}
