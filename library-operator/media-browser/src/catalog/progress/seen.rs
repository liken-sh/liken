// Every play an audience's people are on, as one read answers it, and the
// rules that the screens' progress reads apply to it. The store holds a few
// thousand plays for a household, so one read of all of them costs about as
// much as one read of one work did, and the rules then cost nothing. A
// franchise page that asked the store once for each member, and again for
// each person, made more than a hundred reads where this makes one.

use std::collections::{BTreeMap, HashMap};

use super::{Progress, Resume};

// The kind word the store's reads give a film's plays.
const MOVIE: &str = "movie";

// The kind word the store's reads give a series' plays.
const SERIES: &str = "series";

/// One play of one work, and every person the play names. `resume.exact`
/// is false here, because it depends on the audience a rule is applied
/// for, and [`every`] sets it.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Seen {
    pub resume: Resume,
    /// The `Person` names of the play, and empty for a play that names
    /// nobody.
    pub people: Vec<String>,
}

impl Seen {
    fn is_of(&self, library: &str, id: &str) -> bool {
        self.resume.library == library && self.resume.id == id
    }

    fn names(&self, person: &str) -> bool {
        self.people.iter().any(|name| name == person)
    }

    // Whether the play belongs to this audience under the rule that reads
    // each person's plays apart: some person of the audience is on it, or,
    // for an empty audience, it names nobody.
    fn includes_any(&self, people: &[String]) -> bool {
        match people.is_empty() {
            true => self.people.is_empty(),
            false => people.iter().any(|person| self.names(person)),
        }
    }

    // Whether every person of the audience is on the play. An empty
    // audience takes only the plays that name nobody.
    fn includes_every(&self, people: &[String]) -> bool {
        match people.is_empty() {
            true => self.people.is_empty(),
            false => people.iter().all(|person| self.names(person)),
        }
    }

    // Whether the play names this audience and nobody more.
    fn names_exactly(&self, people: &[String]) -> bool {
        self.includes_every(people) && self.people.len() == people.len()
    }
}

/// The plays every person of the audience is on, newest first, across every
/// work or of the one work that `work` names. This is the fetch of the
/// thread rule. Each play's `exact` says whether it names exactly these
/// people.
pub fn every(seen: &[Seen], people: &[String], work: Option<(&str, &str)>) -> Vec<Resume> {
    let mut plays: Vec<Resume> = seen
        .iter()
        .filter(|play| work.is_none_or(|(library, id)| play.is_of(library, id)))
        .filter(|play| play.includes_every(people))
        .map(|play| Resume {
            exact: play.names_exactly(people),
            ..play.resume.clone()
        })
        .collect();
    plays.sort_by(|one, other| {
        other
            .progress
            .recorded
            .cmp(&one.progress.recorded)
            .then_with(|| one.library.cmp(&other.library))
            .then_with(|| one.id.cmp(&other.id))
            .then_with(|| one.progress.play.cmp(&other.progress.play))
    });
    plays
}

/// Where this audience reached in each episode of one series: the latest
/// play of each episode, in aired order. An episode is in the answer only
/// when every person of the audience has a play of it, in any group.
pub fn episodes(seen: &[Seen], library: &str, series: &str, people: &[String]) -> Vec<Progress> {
    let mut by_episode: BTreeMap<(i64, i64), Vec<&Seen>> = BTreeMap::new();
    for play in seen
        .iter()
        .filter(|play| play.resume.kind == SERIES && play.is_of(library, series))
        .filter(|play| play.includes_any(people))
    {
        let at = (play.resume.progress.season, play.resume.progress.episode);
        by_episode.entry(at).or_default().push(play);
    }
    by_episode
        .into_values()
        .filter(|plays| everyone_played(plays, people))
        .filter_map(|plays| latest(&plays).map(|play| play.resume.progress.clone()))
        .collect()
}

// Whether every person of the audience is on at least one of these plays.
// An empty audience's plays already name nobody, so it keeps them all.
fn everyone_played(plays: &[&Seen], people: &[String]) -> bool {
    people
        .iter()
        .all(|person| plays.iter().any(|play| play.names(person)))
}

// The play recorded last, and of two recorded in the same second, the one
// whose name sorts first, so a re-read answers the same play.
fn latest<'a>(plays: &[&'a Seen]) -> Option<&'a Seen> {
    plays.iter().copied().min_by(|one, other| {
        other
            .resume
            .progress
            .recorded
            .cmp(&one.resume.progress.recorded)
            .then_with(|| one.resume.progress.play.cmp(&other.resume.progress.play))
    })
}

/// A work as the progress reads key it: its library and its id.
pub type Work = (String, String);

/// How far each person of an audience got in one film. Each person's share
/// is the furthest of their plays, alone or with anyone, and a play that
/// passed the finished mark counts as the whole film. The furthest play is
/// taken over the latest, because a person who finished a film and then
/// started it again has still seen it. A play at position 0 is a clear, the
/// mark a person sends to say they have not seen the film, so it takes back
/// every earlier play of that person, and only the plays after it count.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Reach {
    /// Each person of the audience with a play of the film, in the
    /// audience's order, and their share. An empty audience has one entry
    /// with an empty name, for the plays that name nobody.
    pub people: Vec<(String, f32)>,
}

impl Reach {
    /// The share the bar under the film draws: the least of every person's
    /// share, which is how far the whole audience got. Nothing when a person
    /// of the audience has no play of the film, because the audience has
    /// not all started it.
    pub fn shared(&self, audience: &[String]) -> Option<f32> {
        let wanted = audience.len().max(1);
        if self.people.len() < wanted {
            return None;
        }
        self.people.iter().map(|(_, share)| *share).reduce(f32::min)
    }

    /// The names of the people with a play of the film, in the audience's
    /// order.
    pub fn names(&self) -> Vec<String> {
        self.people.iter().map(|(name, _)| name.clone()).collect()
    }
}

/// The reach of every film any person of the audience has a play of, across
/// every library.
pub fn films(seen: &[Seen], people: &[String]) -> HashMap<Work, Reach> {
    let nobody = [String::new()];
    let audience: &[String] = match people.is_empty() {
        true => &nobody,
        false => people,
    };
    // Every play of each film, for each person of the audience, as the
    // second it was recorded and where it reached.
    let mut plays: HashMap<Work, Vec<Vec<&Progress>>> = HashMap::new();
    for play in seen
        .iter()
        .filter(|play| play.resume.kind == MOVIE && play.includes_any(people))
    {
        let work = (play.resume.library.clone(), play.resume.id.clone());
        let held = plays
            .entry(work)
            .or_insert_with(|| vec![Vec::new(); audience.len()]);
        for (at, person) in audience.iter().enumerate() {
            if people.is_empty() || play.names(person) {
                held[at].push(&play.resume.progress);
            }
        }
    }
    plays
        .into_iter()
        .map(|(work, held)| {
            let people = audience
                .iter()
                .zip(held)
                .filter_map(|(person, plays)| Some((person.clone(), furthest(&plays)?)))
                .collect();
            (work, Reach { people })
        })
        .collect()
}

// The furthest share of one person's plays of a film after their latest
// clear, and nothing where no play follows it.
fn furthest(plays: &[&Progress]) -> Option<f32> {
    let cleared = plays
        .iter()
        .filter(|play| play.position == 0)
        .map(|play| play.recorded)
        .max();
    plays
        .iter()
        .filter(|play| cleared.is_none_or(|at| play.recorded > at))
        .map(|play| share(play))
        .reduce(f32::max)
}

// The share of the film one play reached, and the whole film for a play
// past the finished mark.
fn share(progress: &Progress) -> f32 {
    match progress.finished {
        true => 1.0,
        false => progress.played().fraction().clamp(0.0, 1.0),
    }
}

#[cfg(test)]
mod tests;
