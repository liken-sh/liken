// The two recency strips differ from the walls behind them. Both
// queries fold the same shows, so the released strip keeps what is new in
// the world, and the added strip keeps what arrived and is not. The walls
// answer whole. This module holds the two rules, applied to the strips on
// the home page alone.

use crate::catalog::Slot;
use crate::catalog::recency::{SHOWN, current};
use crate::screens::Item;

/// The released strip's slots: the ones inside the window of today, in
/// seconds, and a folded show that holds an episode inside it whatever
/// the date of the newest episode it draws, newest first, cut to `SHOWN`.
pub fn released(slots: Vec<Slot>, today: i64) -> Vec<Slot> {
    slots
        .into_iter()
        .filter(|slot| slot.new > 0 || current(&slot.released, today))
        .take(SHOWN)
        .collect()
}

/// The added strip's slots: every one the released strip shows left
/// out, then cut to `SHOWN`. The subtraction runs before the cut, because
/// a strip that dropped its first slots after the cut would come up
/// short.
pub fn added(slots: Vec<Slot>, released: &[Item]) -> Vec<Slot> {
    slots
        .into_iter()
        .filter(|slot| {
            !released
                .iter()
                .any(|shown| shown.library == slot.library && shown.id == slot.id)
        })
        .take(SHOWN)
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::catalog::Query;
    use crate::catalog::recency::date_seconds;

    fn today() -> i64 {
        date_seconds("2026-09-03").expect("a full date")
    }

    fn slot(id: &str, released: &str) -> Slot {
        Slot {
            library: "screening/films".into(),
            kind: "movies".into(),
            id: id.into(),
            title: id.into(),
            released: released.into(),
            ..Slot::default()
        }
    }

    fn ids(slots: &[Slot]) -> Vec<&str> {
        slots.iter().map(|slot| slot.id.as_str()).collect()
    }

    #[test]
    fn the_released_strip_keeps_only_dates_inside_the_window() {
        for (date, kept) in [
            ("2026-09-03", true),
            ("2026-09-01", true),
            ("2026-08-04", true),
            ("2026-08-03", false),
            ("2026-09-20", true),
            ("2025-09-03", false),
            ("2026", false),
            ("", false),
        ] {
            let slots = released(vec![slot("one", date)], today());
            assert_eq!(!slots.is_empty(), kept, "{date}");
        }
    }

    #[test]
    fn the_released_strip_keeps_the_order_and_cuts_to_shown() {
        let slots: Vec<Slot> = (0..SHOWN + 5)
            .map(|number| slot(&format!("movie:{number}"), "2026-09-01"))
            .collect();
        let shown = released(slots, today());
        assert_eq!(shown.len(), SHOWN);
        assert_eq!(shown[0].id, "movie:0");
        assert_eq!(shown[SHOWN - 1].id, format!("movie:{}", SHOWN - 1));
    }

    #[test]
    fn the_added_strip_drops_what_the_released_strip_shows_before_the_cut() {
        let query = Query::Released {
            fold: crate::catalog::Fold::Airing,
        };
        let shown: Vec<Item> = (0..3)
            .map(|number| Item::of(&query, slot(&format!("movie:{number}"), "2026-09-01")))
            .collect();
        let arrived: Vec<Slot> = (0..SHOWN + 3)
            .map(|number| slot(&format!("movie:{number}"), "1980"))
            .collect();
        let left = added(arrived, &shown);
        assert_eq!(left.len(), SHOWN);
        assert_eq!(ids(&left)[0], "movie:3");
        assert!(!ids(&left).contains(&"movie:0"));
    }

    // One show as the `Shows` fold answers it: the newest episode's
    // still under the series' own id.
    fn show() -> Slot {
        Slot {
            library: "screening/serials".into(),
            kind: "episodes".into(),
            id: "series:1".into(),
            title: "Segment 08".into(),
            released: "2026-09-01".into(),
            new: 2,
            episode: Some(crate::catalog::InSeries {
                series: "series:1".into(),
                name: "The Serial".into(),
                season: 4,
                episode: 8,
            }),
            ..Slot::default()
        }
    }

    #[test]
    fn the_released_strip_keeps_a_show_that_holds_a_new_episode() {
        let ahead = Slot {
            released: "2027-01-01".into(),
            new: 2,
            ..show()
        };
        assert_eq!(ids(&released(vec![ahead], today())), ["series:1"]);
        let none = Slot {
            released: "2027-01-01".into(),
            new: 0,
            ..show()
        };
        assert!(released(vec![none], today()).is_empty());
    }

    #[test]
    fn the_added_strip_drops_a_folded_show_the_released_strip_shows() {
        let query = Query::Released {
            fold: crate::catalog::Fold::Shows { today: today() },
        };
        let shown = vec![Item::of(&query, show())];
        assert_eq!(shown[0].id, "series:1");
        let left = added(vec![show(), slot("movie:1", "1980")], &shown);
        assert_eq!(ids(&left), ["movie:1"]);
    }

    #[test]
    fn a_slot_of_another_library_with_the_same_id_stays_in_the_added_strip() {
        let query = Query::Released {
            fold: crate::catalog::Fold::Airing,
        };
        let shown = vec![Item::of(&query, slot("series:1", "2026-09-01"))];
        let elsewhere = Slot {
            library: "screening/serials".into(),
            ..slot("series:1", "2026-09-01")
        };
        assert_eq!(ids(&added(vec![elsewhere], &shown)), ["series:1"]);
    }
}
