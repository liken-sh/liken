// The words the browser's log lines use for its screens, its presses,
// and the unit's activity. One place holds them, so a line about a
// screen names it the same way whatever the press was. Every name here
// is safe to leave the house: a title is named by its catalog id through
// `log::opaque`, a person's entry by a hash, and a search or a genre by
// its kind alone, because the words a person typed and a genre's name
// are the library's own content.

use media_screen::status::Activity;

use crate::catalog::Query;
use crate::log::{hashed, opaque};
use crate::screens::Screen;
use crate::views;

/// The screen as a log line names it.
pub fn screen(screen: &Screen) -> String {
    match screen {
        Screen::Home(_) => "the home page".to_string(),
        Screen::Wall(wall) => walled(&wall.slots.query),
        Screen::Movie(page) => {
            format!("the movie page of {} in {}", opaque(&page.id), page.library)
        }
        Screen::Series(page) => {
            format!(
                "the series page of {} in {}",
                opaque(&page.id),
                page.library
            )
        }
        Screen::Person(page) => format!(
            "the person page of entry {} in {}",
            hashed(&page.path),
            page.library
        ),
        Screen::Franchise(page) => format!(
            "the franchise page of {} in {}",
            opaque(&page.id),
            page.library
        ),
    }
}

// A wall, by the query that answers it.
fn walled(query: &Query) -> String {
    match query {
        Query::Library { library, .. } => format!("the wall of {library}"),
        Query::Person { library, path } => {
            format!("the credits wall of entry {} in {library}", hashed(path))
        }
        Query::Set { library, id } => format!("the wall of set {} in {library}", opaque(id)),
        Query::Released { .. } => "the recently released wall".to_string(),
        Query::Added { .. } => "the recently added wall".to_string(),
        Query::Franchise { library, id } => {
            format!("the wall of franchise {} in {library}", opaque(id))
        }
        Query::Genre { .. } => "a genre wall".to_string(),
        Query::Search { .. } => "the search wall".to_string(),
    }
}

/// The press as a log line names it: the browser's word, and the kernel's
/// name after it where the press came over the bus. A letter, a digit,
/// and a space are "a character" and nothing more, kernel name included,
/// because the characters a person types spell what they search for.
pub fn press(word: &str, kernel: Option<&str>) -> String {
    if views::field::edits(word) {
        return "a character".to_string();
    }
    match kernel {
        Some(kernel) => format!("{word} ({kernel})"),
        None => word.to_string(),
    }
}

/// The unit's activity as a log line names it.
pub fn activity(activity: Activity) -> &'static str {
    match activity {
        Activity::Idle => "idle",
        Activity::Starting => "starting",
        Activity::Playing => "playing",
    }
}

/// A list of `Person` names, or "nobody" for an empty one.
pub fn people(names: &[String]) -> String {
    match names.is_empty() {
        true => "nobody".to_string(),
        false => names.join(", "),
    }
}

/// A count of things, with the noun in the number the count needs.
pub fn count(count: usize, noun: &str) -> String {
    match count {
        1 => format!("1 {noun}"),
        count => format!("{count} {noun}s"),
    }
}

/// Where a play starts: the beginning, or the second a resume carries.
pub fn start(start: Option<i64>) -> String {
    match start {
        Some(second) => format!("from {second} s"),
        None => "from the start".to_string(),
    }
}
