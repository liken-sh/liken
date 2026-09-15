// The words a franchise member's runs read as on a facts line, so two
// cards of one show say which cut of it each one is. The spelling is the
// series page's own: "S03 · E02".

use crate::screens::facts;

// How many consecutive numbers make a range. Two whole seasons in a row
// read as "Seasons 1 to 2", while two episodes in a row read as the list
// "E06, E07", because a range of two episodes reads as longer than it is.
const SEASON_SPAN: i64 = 2;
const EPISODE_SPAN: i64 = 3;

/// The name of the cut these runs make: nothing for a member that names
/// no run, which is the whole show.
pub fn run_words(runs: &[(i64, i64)]) -> String {
    let mut phrases: Vec<String> = Vec::new();
    let mut whole: Vec<i64> = Vec::new();
    for (season, episodes) in grouped(runs) {
        if episodes.is_empty() {
            whole.push(season);
            continue;
        }
        if !whole.is_empty() {
            phrases.push(whole_seasons(&whole));
            whole.clear();
        }
        phrases.push(inside_season(season, &episodes));
    }
    if !whole.is_empty() {
        phrases.push(whole_seasons(&whole));
    }
    let phrases: Vec<&str> = phrases.iter().map(String::as_str).collect();
    facts::joined(&phrases)
}

// The pairs by season, in first-seen order. A season that names no
// episode is whole.
fn grouped(runs: &[(i64, i64)]) -> Vec<(i64, Vec<i64>)> {
    let mut groups: Vec<(i64, Vec<i64>)> = Vec::new();
    for &(season, episode) in runs {
        if !groups.iter().any(|(number, _)| *number == season) {
            groups.push((season, Vec::new()));
        }
        if let Some(group) = groups.iter_mut().find(|(number, _)| *number == season)
            && episode != 0
        {
            group.1.push(episode);
        }
    }
    groups
}

// Whole seasons as one phrase, with a range where they run consecutive.
fn whole_seasons(seasons: &[i64]) -> String {
    let word = match seasons.len() {
        1 => "Season",
        _ => "Seasons",
    };
    let list: Vec<String> = spans(seasons, SEASON_SPAN)
        .iter()
        .map(|&(first, last)| match first == last {
            true => format!("{first}"),
            false => format!("{first} to {last}"),
        })
        .collect();
    format!("{word} {}", list.join(", "))
}

// The episodes of one season after the season's own number.
fn inside_season(season: i64, episodes: &[i64]) -> String {
    let list: Vec<String> = spans(episodes, EPISODE_SPAN)
        .iter()
        .map(|&(first, last)| match first == last {
            true => format!("E{first:02}"),
            false => format!("E{first:02} to E{last:02}"),
        })
        .collect();
    facts::joined(&[&format!("S{season:02}"), &list.join(", ")])
}

// Consecutive numbers as first-and-last pairs. A span shorter than
// `least` comes back as one pair per number, so it reads as a list.
fn spans(numbers: &[i64], least: i64) -> Vec<(i64, i64)> {
    let mut spans: Vec<(i64, i64)> = Vec::new();
    for &number in numbers {
        if let Some(span) = spans.last_mut()
            && span.1 + 1 == number
        {
            span.1 = number;
        } else {
            spans.push((number, number));
        }
    }
    spans
        .into_iter()
        .flat_map(|(first, last)| match last - first + 1 >= least {
            true => vec![(first, last)],
            false => (first..=last).map(|number| (number, number)).collect(),
        })
        .collect()
}

#[cfg(test)]
mod tests;
