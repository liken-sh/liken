//! The lanes that a sequence adds after its stated lanes: one for each
//! program that accepts an audience that the page object's templates
//! present, and a dashed lane for each audience that no loaded knowledge
//! accepts. A program whose workload is already a part of a stated lane
//! adds nothing, because that lane draws it, so the `simmer` operator's
//! own audience does not add a lane to the loop of a `Mandate`.

use super::Judged;
use super::LaneKind;
use crate::presents::Presents;
use transect_graph::edges::Object;

/// One lane to add, with its steps from the lane that presents.
#[derive(Debug, PartialEq)]
pub struct Added {
    pub label: String,
    pub kind: LaneKind,
    /// The workloads that run the program.
    pub runs: Vec<Object>,
    pub note: Option<String>,
    pub source: String,
    pub steps: Vec<AddedStep>,
}

/// One step to an added lane.
#[derive(Debug, PartialEq)]
pub struct AddedStep {
    pub verb: String,
    pub state: String,
    pub judged: Judged,
    pub source: String,
}

/// The lanes to add for `presents`, in the order of the audiences'
/// names. `stated` is the objects of each stated lane, and `top` gives
/// the top owner of each, because a program's workloads are top owners.
/// A program that accepts two audiences has one lane with a step for
/// each.
pub fn lanes(
    presents: &Presents,
    stated: &[Vec<Object>],
    top: &dyn Fn(&Object) -> Object,
) -> Vec<Added> {
    let mut audiences: Vec<&str> = presents.audiences.iter().map(String::as_str).collect();
    audiences.sort_unstable();
    audiences.dedup();
    let drawn = |runs: &[Object]| {
        let mut objects = stated.iter().flatten();
        objects.any(|object| runs.contains(&top(object)))
    };
    let mut lanes: Vec<Added> = Vec::new();
    for audience in audiences {
        let accepting = presents.accepting(audience);
        if accepting.is_empty() {
            lanes.push(Added {
                label: audience.into(),
                kind: LaneKind::Unknown,
                runs: Vec::new(),
                note: Some(format!("no knowledge loaded for {audience}")),
                source: format!("no loaded knowledge accepts the audience {audience}"),
                steps: vec![AddedStep {
                    verb: format!("presents a token for {audience}"),
                    state: format!("no knowledge loaded for {audience}"),
                    judged: Judged::Unknown,
                    source: format!(
                        "a pod template presents the audience {audience}, and no loaded knowledge accepts it"
                    ),
                }],
            });
            continue;
        }
        for one in accepting.into_iter().filter(|one| !drawn(&one.runs)) {
            let step = AddedStep {
                verb: format!("{}, as {audience}", one.verb),
                state: "transect reads no state for this step".into(),
                judged: Judged::Unsaid,
                source: format!(
                    "a pod template presents the audience {audience}, which the program {} accepts",
                    one.program
                ),
            };
            if let Some(lane) = lanes
                .iter_mut()
                .find(|lane| lane.kind == LaneKind::Added && lane.label == one.program)
            {
                lane.steps.push(step);
                continue;
            }
            lanes.push(Added {
                label: one.program.clone(),
                kind: LaneKind::Added,
                runs: one.runs.clone(),
                note: one.runs.is_empty().then(|| "runs nowhere now".to_string()),
                source: format!("the knowledge of the program {}", one.program),
                steps: vec![step],
            });
        }
    }
    lanes
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use crate::presents::Accepting;

    fn workload(name: &str) -> Object {
        Object::new("apps", "Deployment", Some("tools"), name)
    }

    fn accepting(audience: &str, program: &str) -> Accepting {
        Accepting {
            audience: audience.into(),
            program: program.into(),
            verb: format!("calls {program}"),
            runs: vec![workload(program)],
        }
    }

    fn presents(audiences: &[&str], accepting: Vec<Accepting>) -> Presents {
        Presents {
            audiences: audiences.iter().map(|a| a.to_string()).collect(),
            accepting,
        }
    }

    /// Each lane as its label, its kind, and the verbs of its steps.
    fn shown(lanes: Vec<Added>) -> Vec<(String, LaneKind, Vec<String>)> {
        let lanes = lanes.into_iter().map(|lane| {
            let verbs = lane.steps.into_iter().map(|step| step.verb).collect();
            (lane.label, lane.kind, verbs)
        });
        lanes.collect()
    }

    fn lane(label: &str, kind: LaneKind, verbs: &[&str]) -> (String, LaneKind, Vec<String>) {
        let verbs = verbs.iter().map(|verb| verb.to_string()).collect();
        (label.into(), kind, verbs)
    }

    #[rstest]
    #[case::nothing_presented(presents(&[], vec![]), vec![])]
    #[case::a_lane_for_each_program_by_the_audiences_names(
        presents(&["shed", "gate"], vec![accepting("shed", "rake"), accepting("gate", "latch")]),
        vec![
            lane("latch", LaneKind::Added, &["calls latch, as gate"]),
            lane("rake", LaneKind::Added, &["calls rake, as shed"]),
        ]
    )]
    #[case::an_audience_no_knowledge_accepts(
        presents(&["well"], vec![]),
        vec![lane("well", LaneKind::Unknown, &["presents a token for well"])]
    )]
    #[case::a_program_a_stated_lane_draws(
        presents(&["keeper", "gate"], vec![accepting("keeper", "keeper"), accepting("gate", "latch")]),
        vec![lane("latch", LaneKind::Added, &["calls latch, as gate"])]
    )]
    #[case::one_program_of_two_audiences(
        presents(&["gate", "door"], vec![accepting("gate", "latch"), accepting("door", "latch")]),
        vec![lane("latch", LaneKind::Added, &["calls latch, as door", "calls latch, as gate"])]
    )]
    fn adds_a_lane_for_each_program_that_accepts_an_audience(
        #[case] presents: Presents,
        #[case] expected: Vec<(String, LaneKind, Vec<String>)>,
    ) {
        let stated = vec![vec![workload("keeper")], vec![]];
        assert_eq!(shown(lanes(&presents, &stated, &Object::clone)), expected);
    }
}
