// The seasons of a series: the dividers and the stills one read of the
// episodes builds, and the bars of the rail beside a long wall.

use super::{COLUMNS, Focus, Season, Series, Still};
use crate::catalog::Episode;
use crate::focus::{self, Run};
use crate::screens::facts;
use crate::views::{card, rail, scroll, wall};
use iced_winit::core::Rectangle;

/// How many seasons a series holds and still draws no rail.
pub const SHORT: usize = 4;

// The wall and its dividers out of one read of the episodes. The rows
// arrive in aired order, so a season starts wherever the season number
// changes, and its year is the year of the first episode that aired in
// it.
pub fn wall_of(episodes: Vec<Episode>, today: &str) -> (Vec<Still>, Vec<Season>) {
    let band = wall::band(COLUMNS);
    let mut stills = Vec::with_capacity(episodes.len());
    let mut seasons: Vec<Season> = Vec::new();
    let mut years: Vec<String> = Vec::new();
    for (index, episode) in episodes.into_iter().enumerate() {
        match seasons.last_mut() {
            Some(season) if season.number == episode.season => season.run.count += 1,
            _ => {
                seasons.push(Season {
                    number: episode.season,
                    name: String::new(),
                    run: Run {
                        first: index,
                        count: 1,
                    },
                });
                years.push(facts::year(&episode.released).to_string());
            }
        }
        stills.push(still_of(episode, today, band));
    }
    // The heading is written after the read, because it counts the
    // season's episodes, and the count is known only once the next
    // season starts or the episodes run out.
    for (season, year) in seasons.iter_mut().zip(&years) {
        season.name = named(season.number, year, season.run.count);
    }
    (stills, seasons)
}

// The divider's heading: the season, the year of its first episode
// where the catalog holds one, and how many episodes it holds.
fn named(season: i64, year: &str, episodes: usize) -> String {
    facts::joined(&[&format!("Season {season}"), year, &counted(episodes)])
}

// The episode count as a person reads it, singular at one.
fn counted(episodes: usize) -> String {
    match episodes {
        1 => "1 episode".to_string(),
        count => format!("{count} episodes"),
    }
}

pub fn still_of(episode: Episode, today: &str, band: f32) -> Still {
    let season = format!("S{:02}", episode.season);
    let numbered = format!("E{:02}", episode.episode);
    let runtime = facts::runtime(episode.duration);
    let under = facts::joined(&[&numbered, &runtime]);
    Still {
        id: episode.id,
        fitted: card::cut(&episode.title, band),
        under: card::under_cut(&under, band),
        facts: facts::joined(&[&season, &numbered, &episode.title]),
        aired: facts::joined(&[&runtime, &facts::date_worded(&episode.released, today)]),
        season: episode.season,
        episode: episode.episode,
        name: episode.title,
        plot: episode.plot,
        art: episode.art,
        progress: None,
    }
}

/// The bars of the rail: one per season while the seasons fit the
/// region, and even ranges of neighbouring seasons where they do not, so
/// every bar is on the screen at once. A range's label is longer than a
/// season's, and a longer label fits fewer bars, so the count is asked
/// again until the labels of the count answered fit it. None on a series
/// of four seasons or fewer.
pub fn bars(seasons: &[Season], region: Rectangle) -> Vec<rail::Bar> {
    if seasons.len() <= SHORT {
        return Vec::new();
    }
    let tops = rows(seasons);
    let mut count = seasons.len();
    loop {
        let labels = labels(seasons, count);
        let held = rail::fits(region, longest(&labels));
        if held >= count {
            return labels
                .into_iter()
                .enumerate()
                .map(|(index, label)| {
                    let (start, end) = range(seasons.len(), count, index);
                    rail::Bar {
                        label,
                        first: tops[start],
                        last: tops[end] - 1,
                        lane: 0,
                    }
                })
                .collect();
        }
        count = held;
    }
}

// The labels of a rail of `count` bars over these seasons, in bar order.
fn labels(seasons: &[Season], count: usize) -> Vec<String> {
    (0..count)
        .map(|index| {
            let (start, end) = range(seasons.len(), count, index);
            numbered(&seasons[start], &seasons[end - 1])
        })
        .collect()
}

// The longest label, which is the one the rail has to hold.
fn longest(labels: &[String]) -> &str {
    labels
        .iter()
        .max_by_key(|label| label.chars().count())
        .map(String::as_str)
        .unwrap_or_default()
}

// The seasons one bar of a rail of `count` bars covers, as the first and
// one past the last, so the bars share the seasons evenly and in order.
fn range(seasons: usize, count: usize, bar: usize) -> (usize, usize) {
    (bar * seasons / count, (bar + 1) * seasons / count)
}

// A bar's label: the season's number alone, or the first and last
// numbers of a range. A bar is one lane wide, so the label is the
// number with no word beside it; the divider over the rows keeps the
// heading that names the season in full.
fn numbered(first: &Season, last: &Season) -> String {
    match first.number == last.number {
        true => first.number.to_string(),
        false => format!("{}\u{2013}{}", first.number, last.number),
    }
}

// The first row of every season, and the row after the last, so a bar
// over a range of seasons reads its rows off the ends.
fn rows(seasons: &[Season]) -> Vec<usize> {
    let mut row = 0;
    let mut tops = Vec::with_capacity(seasons.len() + 1);
    for season in seasons {
        tops.push(row);
        row += scroll::rows(season.run.count, COLUMNS);
    }
    tops.push(row);
    tops
}

/// The bar a right press from this still moves onto, or nothing where
/// the still is not at the wall's right edge or the page draws no rail.
pub fn onto(page: &Series, index: usize) -> Option<usize> {
    let (at, season) = page.seasons.iter().enumerate().find(|(_, season)| {
        index >= season.run.first && index < season.run.first + season.run.count
    })?;
    let local = index - season.run.first;
    if local % COLUMNS != COLUMNS - 1 && local + 1 != season.run.count {
        return None;
    }
    rail::covering(&page.bars, rows(&page.seasons)[at] + local / COLUMNS)
}

/// One press while a bar holds focus: up and down along the bars, select
/// onto the first still the bar covers, and left back to the wall.
pub fn key(page: &Series, bar: usize, key: &str) -> Focus {
    let Some(run) = covers(page, bar) else {
        return Focus::Rail(bar);
    };
    match key {
        "up" | "down" => Focus::Rail(focus::list(bar, page.bars.len(), key)),
        "enter" => Focus::Still(run.first),
        "left" => Focus::Still(back(run, page.entered)),
        _ => Focus::Rail(bar),
    }
}

/// Where the wall stands while a bar holds focus: at the still a select
/// on that bar lands on, so the scroll reads the rail's focus as a still.
pub fn standing(page: &Series) -> Focus {
    match page.focus {
        Focus::Rail(bar) => match covers(page, bar) {
            Some(run) => Focus::Still(run.first),
            None => Focus::Still(0),
        },
        held => held,
    }
}

// The stills one bar covers, as one run, or nothing for a bar the rail
// does not hold.
fn covers(page: &Series, bar: usize) -> Option<Run> {
    let (start, end) = range(page.seasons.len(), page.bars.len().max(1), bar);
    let first = page.seasons.get(start)?;
    let last = page.seasons.get(end.saturating_sub(1))?;
    Some(Run {
        first: first.run.first,
        count: last.run.first + last.run.count - first.run.first,
    })
}

// The still a left press lands on: the one the rail was entered from
// while the focused bar still covers it, else the bar's first still.
fn back(run: Run, entered: usize) -> usize {
    let inside = entered >= run.first && entered < run.first + run.count;
    match inside {
        true => entered,
        false => run.first,
    }
}
