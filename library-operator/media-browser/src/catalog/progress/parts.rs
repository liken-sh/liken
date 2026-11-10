// The split of one play over a file of several episodes. The store keeps
// the shape that mpv and Jellyfin use: a row from a `Play` or from Jellyfin
// holds a position in the whole file and names the first episode it
// covers. The catalog records each episode's part of its file in
// `file_items`. The read of the store turns each such row into one row for
// each episode it covers, with a position and a duration inside that
// episode, so the thread rule, the episode wall, and next up read one row
// for each episode and need no rule of their own for a shared file.
//
// The episodes split the file evenly. Part k of n starts at
// floor((k-1)·D/n) seconds and ends where part k+1 starts, or at D for the
// last part. A file whose episodes are not even marks an episode watched at
// the wrong second, and the project accepts that limit.

use super::finish::Origin;
use super::{Credits, Progress, finished};

// The second where part `part` of a file of `parts` parts and `duration`
// seconds starts. Every caller has checked that the part is one of the
// file's, counted from 1.
fn starts(part: i64, parts: i64, duration: i64) -> i64 {
    (part - 1) * duration / parts
}

// The second where part `part` ends, which is where the next part starts,
// or the end of the file for the last part.
fn ends(part: i64, parts: i64, duration: i64) -> i64 {
    match part < parts {
        true => starts(part + 1, parts, duration),
        false => duration,
    }
}

/// One episode's place in its main file, as the catalog records it.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct Place {
    /// The episode's part of the file, counted from 1, and 0 for a file
    /// that no scan has split.
    pub part: i64,
    /// How many episodes the file holds, and 0 or 1 for a file of one work.
    pub parts: i64,
    /// The probe's duration of the file in seconds, and 0 where the probe
    /// found none.
    pub duration: i64,
    /// The season and episode numbers of the file's last episode, which are
    /// the episode's own for a file of one work. A play of any episode of
    /// the file runs to the end of the file, so the work after a play is
    /// the work after this episode.
    pub last: (i64, i64),
}

impl Place {
    /// The place of an episode that is the whole of its file.
    pub fn whole(numbers: (i64, i64)) -> Self {
        Self {
            last: numbers,
            ..Self::default()
        }
    }

    fn split(&self) -> bool {
        self.parts > 1 && (1..=self.parts).contains(&self.part)
    }

    /// The second of the file where a play of the episode starts: 0 for a
    /// file of one work, and nothing for a file of several episodes with no
    /// duration to split, which a play can only start at its beginning.
    pub fn start(&self) -> Option<i64> {
        match (self.split(), self.duration > 0) {
            (false, _) => Some(0),
            (true, true) => Some(starts(self.part, self.parts, self.duration)),
            (true, false) => None,
        }
    }

    /// The seconds a play of the episode runs, from its start to the end of
    /// the file, or nothing where the file is one work or has no duration.
    /// One `Play` covers the file from the chosen episode to its end.
    pub fn runs(&self) -> Option<i64> {
        let start = self.start().filter(|_| self.split())?;
        Some(self.duration - start)
    }
}

/// The file that one episode play's named episode is in: its parts, the
/// part the row names, the probe's duration, the numbers of each episode
/// it holds, and its credits marks in seconds of the file.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct File {
    /// How many episodes the file holds, and 0 or 1 for a file of one work.
    pub parts: i64,
    /// The part of the episode the row names, counted from 1.
    pub part: i64,
    /// The probe's duration in seconds, and 0 where the probe found none.
    pub duration: i64,
    /// Each episode of the file as its part, its season, and its episode.
    /// A part with no numbered episode is absent, and its seconds still
    /// count in `parts`.
    pub episodes: Vec<(i64, i64, i64)>,
    /// The file's credits marks, in seconds from the start of the file.
    pub credits: Vec<Credits>,
}

impl File {
    // The numbers of the episode at one part, where the file holds one.
    fn numbers(&self, part: i64) -> Option<(i64, i64)> {
        self.episodes
            .iter()
            .find(|(at, _, _)| *at == part)
            .map(|(_, season, episode)| (*season, *episode))
    }
}

/// The rows that one row of the store answers for the episodes it covers,
/// each with `finished` set by the finished rule. A row covers the part it
/// names and every later part of the file.
///
/// - A file of one work answers the row as it is.
/// - A file with no duration answers the row as it is, and a row with no
///   duration is never finished.
/// - A mark answers the row as it is, because a mark already holds one
///   episode's position. The credits that start inside its episode are
///   moved into the episode's seconds, so a credits mark near the end of
///   the file does not move the line of an earlier episode.
/// - A row at 0 or less answers its named episode at 0, so a clear and an
///   unplayed toggle in Jellyfin keep their meaning.
/// - A row before the start of its named part answers nothing, because the
///   play reached only seconds the row does not cover.
/// - Any other row answers each covered part that starts before the
///   position. A part the position passed is at its own length, and the
///   part that holds the position is at the position less its start.
///
/// No covered part after the named one is ever answered at 0, because the
/// thread rule and `since_clear` read a row at 0 as a clear.
pub fn split(row: Progress, file: &File) -> Vec<Progress> {
    let (parts, named) = (file.parts, file.part);
    let length = match file.duration > 0 {
        true => file.duration,
        false => row.duration,
    };
    if parts <= 1 || !(1..=parts).contains(&named) || length <= 0 {
        let finished = finished(row.position, row.duration, &file.credits);
        return vec![Progress { finished, ..row }];
    }
    if row.origin == Origin::Mark {
        let credits = within(&file.credits, named, parts, length);
        let finished = finished(row.position, row.duration, &credits);
        return vec![Progress { finished, ..row }];
    }
    let first = starts(named, parts, length);
    if row.position <= 0 {
        return vec![part(&row, file, named, length, 0)];
    }
    if row.position < first {
        return Vec::new();
    }
    (named..=parts)
        .filter(|&at| starts(at, parts, length) < row.position || at == named)
        .filter(|&at| at == named || file.numbers(at).is_some())
        .map(|at| {
            let start = starts(at, parts, length);
            let size = ends(at, parts, length) - start;
            part(&row, file, at, length, (row.position - start).min(size))
        })
        .collect()
}

// The row of one part at a position in the part's own seconds. The named
// part keeps the row's numbers, and every later part takes its own from the
// file.
fn part(row: &Progress, file: &File, at: i64, length: i64, position: i64) -> Progress {
    let start = starts(at, file.parts, length);
    let duration = ends(at, file.parts, length) - start;
    let (season, episode) = match at == file.part {
        true => (row.season, row.episode),
        false => file.numbers(at).unwrap_or_default(),
    };
    let credits = within(&file.credits, at, file.parts, length);
    Progress {
        play: format!("{}#{at:02}", row.play),
        position,
        duration,
        finished: finished(position, duration, &credits),
        season,
        episode,
        ..row.clone()
    }
}

// The credits marks that start inside one part, moved to the part's own
// seconds. An absent start is the start of the file, which is inside the
// first part, and an absent end becomes the part's end.
fn within(credits: &[Credits], at: i64, parts: i64, length: i64) -> Vec<Credits> {
    let start = starts(at, parts, length) as f64;
    let end = ends(at, parts, length) as f64;
    credits
        .iter()
        .filter(|span| (start..end).contains(&span.start.unwrap_or(0.0)))
        .map(|span| Credits {
            start: Some(span.start.unwrap_or(0.0) - start),
            end: Some(span.end.unwrap_or(end) - start),
        })
        .collect()
}

#[cfg(test)]
mod tests;
