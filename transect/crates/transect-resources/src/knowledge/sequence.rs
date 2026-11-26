//! A sequence is the form of an elevation for a system whose parts take
//! turns, such as an event that becomes a turn of an agent. Each lane is
//! a role, and each step is one interaction from a lane to a lane, in
//! the order that the author states. The order is a claim of the
//! author: the cluster records no order, so `transect` draws the steps
//! as written and judges only the state that a step names.

use serde::Deserialize;

/// The lanes and the steps of a sequence. The parser guarantees that
/// each lane is a role, that no lane repeats, and that each step's ends
/// are lanes.
#[derive(Clone, Debug, PartialEq)]
pub struct Sequence {
    /// The names of the roles, from the left.
    pub lanes: Vec<String>,
    pub steps: Vec<Step>,
    /// The lane whose pods present the tokens of the page object's pod
    /// templates. `transect` adds a lane at the right for each program
    /// that accepts one of their audiences, with a step from this lane.
    /// Without it, the sequence adds no lane.
    pub presents: Option<String>,
}

/// One interaction, from a lane to a lane, which can be the same lane.
#[derive(Clone, Debug, PartialEq)]
pub struct Step {
    pub from: String,
    pub to: String,
    /// What the step does, such as "delivers an event". The page writes
    /// it on the arrow.
    pub verb: String,
    /// What says whether the step happens as stated. A step with none
    /// says that no signal names it.
    pub state: Option<StepState>,
}

/// What a step reads of the parts of one role. The role need not be a
/// lane, because the object that says whether a step happens is not
/// always one of its ends: the `Allowance` of a `Mandate` holds the
/// operator's turn.
#[derive(Clone, Debug, PartialEq)]
pub enum StepState {
    /// The step is met when the condition of each part of the role has
    /// the status `good`. Each other part marks the step.
    Condition {
        role: String,
        condition: String,
        good: String,
        /// What the good status means, which the row writes in place of
        /// the condition and its status, such as "has room" for an
        /// `Allowance` whose `Holding` is `False`.
        said: Option<String>,
    },
    /// The step shows how many parts of the role have each value of the
    /// printer columns, and judges nothing. A finished `Session` is not a
    /// fault, so counts say more than a verdict.
    Columns { role: String, columns: Vec<String> },
}

impl StepState {
    /// The role whose parts the state reads.
    pub fn role(&self) -> &str {
        match self {
            Self::Condition { role, .. } | Self::Columns { role, .. } => role,
        }
    }
}

/// `sequence`, as written in the document.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Written {
    lanes: Vec<String>,
    #[serde(default)]
    steps: Vec<WrittenStep>,
    presents: Option<String>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct WrittenStep {
    from: String,
    to: String,
    verb: String,
    state: Option<WrittenState>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct WrittenState {
    role: String,
    condition: Option<String>,
    good: Option<String>,
    said: Option<String>,
    columns: Option<Vec<String>>,
}

/// The sequence of `written`, whose lanes and states name roles of
/// `roles`. The error names the first fault.
pub fn read(written: Written, roles: &[&str]) -> Result<Sequence, String> {
    if written.lanes.is_empty() {
        return Err("the sequence has no lanes".into());
    }
    for (at, lane) in written.lanes.iter().enumerate() {
        if !roles.contains(&lane.as_str()) {
            return Err(format!(
                "the sequence has the lane {lane}, and spec.roles has no {lane}"
            ));
        }
        if written.lanes[..at].contains(lane) {
            return Err(format!("the sequence has the lane {lane} twice"));
        }
    }
    if let Some(presents) = written
        .presents
        .as_ref()
        .filter(|p| !written.lanes.contains(p))
    {
        return Err(format!(
            "presents is {presents}, which is not a lane of the sequence"
        ));
    }
    let lanes = written.lanes;
    let steps = written.steps.into_iter().enumerate().map(|(at, written)| {
        step(written, &lanes, roles).map_err(|fault| format!("steps[{at}]: {fault}"))
    });
    let steps = steps.collect::<Result<_, _>>()?;
    Ok(Sequence {
        lanes,
        steps,
        presents: written.presents,
    })
}

fn step(written: WrittenStep, lanes: &[String], roles: &[&str]) -> Result<Step, String> {
    for end in [&written.from, &written.to] {
        if !lanes.contains(end) {
            return Err(format!("{end} is not a lane of the sequence"));
        }
    }
    let verb = written.verb.trim();
    if verb.is_empty() {
        return Err("verb has no words".into());
    }
    let state = written
        .state
        .map(|state| self::state(state, roles))
        .transpose()?;
    Ok(Step {
        from: written.from,
        to: written.to,
        verb: verb.into(),
        state,
    })
}

fn state(written: WrittenState, roles: &[&str]) -> Result<StepState, String> {
    let role = written.role;
    if !roles.contains(&role.as_str()) {
        return Err(format!(
            "state reads the role {role}, and spec.roles has no {role}"
        ));
    }
    if written.said.is_some() && written.condition.is_none() {
        return Err("state has said, which names what a condition's good status means".into());
    }
    match (written.condition, written.columns, written.good) {
        (Some(condition), None, good) => Ok(StepState::Condition {
            role,
            condition,
            good: good.unwrap_or_else(|| "True".into()),
            said: written.said.filter(|said| !said.trim().is_empty()),
        }),
        (None, Some(columns), None) if !columns.is_empty() => {
            Ok(StepState::Columns { role, columns })
        }
        (None, Some(_), None) => Err("state has an empty list of columns".into()),
        (None, Some(_), Some(_)) => {
            Err("state has good and columns, and good is the status of a condition".into())
        }
        _ => Err("state has one of condition and columns".into()),
    }
}

#[cfg(test)]
mod tests;
