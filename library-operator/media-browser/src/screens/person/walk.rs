// Where focus is on a person's page and what each press does to it. The
// page has two focus rows, the axis and the posters, and the row of
// posters has a rest of its own: the place it scrolls to. The two are
// kept apart because a press moves each thing once. Up from a poster
// moves only the outline, so the row keeps its rest; down from the axis
// lands on the poster the row already shows, so the row keeps it again.
// Only left and right move the row.

use super::axis::Axis;
use super::credit::Credit;

/// Which row holds focus, and where on it.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Focus {
    /// A year with credits on the axis, as an index into its years.
    Axis(usize),
    /// A poster, as an index into the credits.
    Poster(usize),
}

/// Where the row of posters comes to rest.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Rest {
    /// The poster in the middle of the row, by the strip's rule, while a
    /// poster holds focus.
    Centered(usize),
    /// The poster at the left edge of the row, after a move on the axis,
    /// so a busy year shows as many of its credits as the row holds.
    Leading(usize),
}

impl Rest {
    /// The poster the row rests on.
    pub fn anchor(self) -> usize {
        match self {
            Self::Centered(index) | Self::Leading(index) => index,
        }
    }

    fn with(self, index: usize) -> Self {
        match self {
            Self::Centered(_) => Self::Centered(index),
            Self::Leading(_) => Self::Leading(index),
        }
    }
}

/// What a press did.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Pressed {
    /// Focus or the row moved.
    Moved,
    /// Nothing moved: an end of a row, or up from the axis, which leaves
    /// the press to the browser's strip.
    Still,
    /// Select on this poster, which opens the work's page.
    Open(usize),
}

/// Focus on the page, the row's rest, and the poster focus left for the
/// axis, which down returns to while no press moved on the axis.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Walk {
    pub focus: Focus,
    pub rest: Rest,
    left: Option<usize>,
}

impl Walk {
    /// Focus as a page opens: on the credit of the title whose page opened
    /// this one, with the row centered on it, or else on the newest dated
    /// credit, with the row at its right end.
    pub fn opened(credits: &[Credit], via: Option<&(String, String)>) -> Self {
        let from = via.and_then(|work| credits.iter().position(|credit| credit.is(work)));
        if let Some(index) = from {
            return Self::on(index, Rest::Centered(index));
        }
        let newest = credits
            .iter()
            .rposition(|credit| credit.year.is_some())
            .unwrap_or(0);
        Self::on(newest, Rest::Leading(credits.len().saturating_sub(1)))
    }

    fn on(index: usize, rest: Rest) -> Self {
        Self {
            focus: Focus::Poster(index),
            rest,
            left: None,
        }
    }

    /// The poster that holds focus, and nothing while the axis does.
    pub fn poster(&self) -> Option<usize> {
        match self.focus {
            Focus::Poster(index) => Some(index),
            Focus::Axis(_) => None,
        }
    }

    /// Fold one press in, over these many credits and this axis, or no
    /// axis on a page that draws none.
    pub fn press(&mut self, key: &str, axis: Option<&Axis>, count: usize) -> Pressed {
        match (self.focus, axis) {
            (Focus::Poster(index), _) if count == 0 || index >= count => Pressed::Still,
            (Focus::Poster(index), Some(axis)) if key == "up" => {
                let year = axis.year_of(index).unwrap_or(axis.years.len() - 1);
                self.focus = Focus::Axis(year);
                self.left = Some(index);
                Pressed::Moved
            }
            (Focus::Poster(index), _) => self.across(index, key, count),
            (Focus::Axis(year), Some(axis)) => self.along(year, key, axis),
            (Focus::Axis(_), None) => Pressed::Still,
        }
    }

    // A press on the row of posters: one credit left or right, with the
    // row centered on it, or select.
    fn across(&mut self, index: usize, key: &str, count: usize) -> Pressed {
        let to = match key {
            "enter" => return Pressed::Open(index),
            "left" if index > 0 => index - 1,
            "right" if index + 1 < count => index + 1,
            _ => return Pressed::Still,
        };
        self.focus = Focus::Poster(to);
        self.rest = Rest::Centered(to);
        Pressed::Moved
    }

    // A press on the axis: a year left or right, with the row's anchor at
    // the year's first credit, or down to the poster.
    fn along(&mut self, year: usize, key: &str, axis: &Axis) -> Pressed {
        match key {
            "down" | "enter" => {
                let first = axis.years[year].credits[0];
                self.focus = Focus::Poster(self.left.take().unwrap_or(first));
                return Pressed::Moved;
            }
            "left" if year > 0 => self.focus = Focus::Axis(year - 1),
            "right" if year + 1 < axis.years.len() => self.focus = Focus::Axis(year + 1),
            _ => return Pressed::Still,
        }
        if let Focus::Axis(to) = self.focus {
            self.rest = Rest::Leading(axis.years[to].credits[0]);
        }
        self.left = None;
        Pressed::Moved
    }

    /// The works this focus is on, which a later read finds it again by.
    pub fn held(&self, credits: &[Credit], axis: Option<&Axis>) -> Held {
        let work = |index: usize| credits.get(index).map(Credit::work);
        Held {
            poster: self.poster().and_then(work),
            year: match (self.focus, axis) {
                (Focus::Axis(index), Some(axis)) => axis.years.get(index).map(|year| year.year),
                _ => None,
            },
            rest: (self.rest, work(self.rest.anchor())),
            left: self.left.and_then(work),
        }
    }

    /// Focus on the same works after a read, at the same places where a
    /// work is gone, and on the same year of the axis, or the nearest
    /// year with credits. A page whose axis is gone has focus on the
    /// poster the row rests on.
    pub fn hold(&mut self, held: &Held, credits: &[Credit], axis: Option<&Axis>) {
        let last = credits.len().saturating_sub(1);
        let find = |work: &Option<(String, String)>, place: usize| {
            work.as_ref()
                .and_then(|work| credits.iter().position(|credit| credit.is(work)))
                .unwrap_or(place.min(last))
        };
        let (rest, anchor) = &held.rest;
        self.rest = rest.with(find(anchor, rest.anchor()));
        self.left = held.left.as_ref().map(|work| find(&Some(work.clone()), 0));
        self.focus = match (self.focus, held.year, axis) {
            (Focus::Axis(_), Some(year), Some(axis)) => Focus::Axis(nearest(axis, year)),
            (Focus::Axis(_), _, _) => Focus::Poster(self.rest.anchor()),
            (Focus::Poster(index), _, _) => Focus::Poster(find(&held.poster, index)),
        };
    }
}

/// The works a focus was on, as `Walk::held` answers them.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Held {
    poster: Option<(String, String)>,
    year: Option<i64>,
    rest: (Rest, Option<(String, String)>),
    left: Option<(String, String)>,
}

// The year with credits nearest to this year, as an index into the
// axis's years, the earlier of two at the same distance.
fn nearest(axis: &Axis, year: i64) -> usize {
    axis.years
        .iter()
        .enumerate()
        .min_by_key(|(_, held)| (held.year - year).abs())
        .map(|(index, _)| index)
        .unwrap_or(0)
}

#[cfg(test)]
mod tests;
