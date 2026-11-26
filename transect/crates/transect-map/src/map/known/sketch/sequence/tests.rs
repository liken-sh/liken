use rstest::rstest;

use super::*;

fn step(from: usize, to: usize, judged: Judged) -> Step {
    Step {
        from,
        to,
        verb: "rings".into(),
        state: String::new(),
        judged,
        marks: Vec::new(),
        source: String::new(),
    }
}

fn lane(kind: LaneKind) -> Lane {
    Lane {
        label: "bell".into(),
        parts: Vec::new(),
        source: String::new(),
        note: None,
        kind,
    }
}

/// A sequence of four stated lanes and `steps`.
fn four(steps: Vec<Step>) -> Sequence {
    Sequence {
        name: "the peal".into(),
        lanes: (0..4).map(|_| lane(LaneKind::Stated)).collect(),
        steps,
    }
}

#[rstest]
#[case::to_the_right(step(0, 2, Judged::Met), (1, 4, 3), "sequence-arrow sequence-right")]
#[case::to_the_left(step(3, 1, Judged::Met), (2, 5, 3), "sequence-arrow sequence-left")]
#[case::to_its_own_lane_with_room_beside(
    step(1, 1, Judged::Met),
    (2, 4, 2),
    "sequence-arrow sequence-self"
)]
#[case::to_the_last_lane_itself(step(3, 3, Judged::Met), (4, 5, 1), "sequence-arrow sequence-self")]
#[case::to_an_audience_no_knowledge_accepts(
    step(0, 1, Judged::Unknown),
    (1, 3, 2),
    "sequence-arrow sequence-right sequence-dashed"
)]
fn places_a_step_between_the_middles_of_its_lanes(
    #[case] step: Step,
    #[case] place: (usize, usize, usize),
    #[case] class: &str,
) {
    let sequence = four(Vec::new());
    assert_eq!(
        (sequence.place(&step), step.class()),
        (place, class.to_string())
    );
}

#[rstest]
#[case::only_stated(vec![LaneKind::Stated, LaneKind::Stated], None)]
#[case::after_the_stated(vec![LaneKind::Stated, LaneKind::Added, LaneKind::Unknown], Some(1))]
fn sets_the_added_lanes_apart_from_the_stated(
    #[case] kinds: Vec<LaneKind>,
    #[case] first: Option<usize>,
) {
    let sequence = Sequence {
        lanes: kinds.into_iter().map(lane).collect(),
        ..four(Vec::new())
    };
    assert_eq!(sequence.first_added(), first);
}
