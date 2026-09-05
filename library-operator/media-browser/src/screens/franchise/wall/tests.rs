// The wall's measures over an invented order: the universes it names,
// the rows it lays out, the bars it derives, and the boxes it places.

use super::*;
use crate::catalog::franchise::{Calendar, Held, MOVIE};
use crate::look;
use crate::views::text;

// The day the tests stand on.
const TODAY: &str = "2026-09-04";

const COPPICE: &str = "The Coppice";
const FEN: &str = "The Fen";
const MARSH: &str = "The Marsh";

fn calendar() -> Calendar {
    Calendar {
        unit: "years".into(),
        zero: "the Survey".into(),
        before: "BS".into(),
        after: "AS".into(),
    }
}

fn held(id: &str) -> Held {
    Held {
        arts: vec![format!("{id}.jpg"), format!("{id}/backdrop.jpg")],
        library: "sample/features".into(),
        id: id.into(),
        kind: "movies".into(),
        title: format!("Film {id}"),
        art: format!("{id}.jpg"),
        released: "1980".into(),
        slug: id.into(),
        tagline: format!("A line about {id}."),
        plot: format!("The plot of {id}."),
        duration: 7_440,
    }
}

fn entry(position: i64, span: (f64, f64), universes: &[&str]) -> Entry {
    Entry {
        position,
        kind: MOVIE.into(),
        alias: format!("movie:tmdb:{position}"),
        title: format!("Film {position}"),
        released: "1980".into(),
        release_year: 1980,
        timed: true,
        from: span.0,
        to: span.1,
        universes: universes.iter().map(|name| name.to_string()).collect(),
        held: Some(held(&format!("movie:path:{position}"))),
        episodes: 0,
    }
}

fn gap(position: i64, span: (f64, f64), universes: &[&str]) -> Entry {
    Entry {
        held: None,
        ..entry(position, span, universes)
    }
}

fn franchise(entries: Vec<Entry>) -> Franchise {
    Franchise {
        library: "sample/orders".into(),
        id: "franchise:name:the-cycle".into(),
        title: "The Cycle".into(),
        art: String::new(),
        universe: COPPICE.into(),
        calendar: Some(calendar()),
        eras: Vec::new(),
        entries,
    }
}

#[test]
fn the_franchises_own_universe_is_the_first_column() {
    let page = franchise(vec![
        entry(1, (-32.0, -32.0), &[]),
        entry(2, (-30.0, -30.0), &[MARSH]),
        entry(3, (-28.0, -28.0), &[FEN]),
        entry(4, (-26.0, -26.0), &[MARSH]),
    ]);
    assert_eq!(columns(&page), [COPPICE, MARSH, FEN]);
}

#[test]
fn a_franchise_that_names_no_universe_still_holds_one_column() {
    let page = Franchise {
        universe: String::new(),
        ..franchise(vec![entry(1, (0.0, 0.0), &[])])
    };
    assert_eq!(columns(&page), [""]);
}

#[test]
fn an_entry_takes_a_dot_on_the_line_of_every_universe_it_names() {
    let page = franchise(vec![
        entry(1, (-32.0, -32.0), &[]),
        entry(2, (-30.0, -30.0), &[MARSH]),
        entry(3, (10.0, 12.0), &[COPPICE, MARSH, FEN]),
        entry(4, (14.0, 14.0), &["Nowhere"]),
    ]);
    let columns = columns(&page);
    assert_eq!(columns, [COPPICE, MARSH, FEN, "Nowhere"]);
    let rows = story(&page, &columns[..3], TODAY);
    assert_eq!(rows[0].cell.universes, [0]);
    assert_eq!(rows[1].cell.universes, [1]);
    assert_eq!(rows[2].cell.universes, [0, 1, 2]);
    assert_eq!(rows[3].cell.universes, [0]);
}

#[test]
fn two_entries_the_story_tells_at_once_each_take_a_row_of_their_own() {
    let page = franchise(vec![
        entry(1, (-32.0, -32.0), &[]),
        entry(2, (-30.0, -28.0), &[FEN]),
        entry(3, (-29.0, -27.0), &[MARSH]),
    ]);
    let columns = columns(&page);
    let rows = story(&page, &columns, TODAY);
    assert_eq!(rows.len(), 3);
    assert_eq!((rows[1].from, rows[1].to), (-30.0, -28.0));
    assert_eq!(rows[1].time, "-30 to -28 BS");
    assert_eq!(rows[2].time, "-29 to -27 BS");
}

#[test]
fn an_entry_with_no_time_draws_no_time() {
    let page = franchise(vec![Entry {
        timed: false,
        ..entry(2, (0.0, 0.0), &[FEN])
    }]);
    let rows = story(&page, &columns(&page), TODAY);
    assert_eq!(rows[0].time, "");
    assert!(!rows[0].timed);
}

#[test]
fn a_franchise_with_no_calendar_draws_no_time() {
    let page = Franchise {
        calendar: None,
        ..franchise(vec![entry(1, (-32.0, -32.0), &[])])
    };
    let rows = story(&page, &columns(&page), TODAY);
    assert_eq!(rows[0].time, "");
    assert!(rows[0].timed);
}

#[test]
fn a_row_reads_its_span_in_the_calendars_own_words() {
    let page = franchise(vec![entry(1, (-32.0, -32.0), &[])]);
    let rows = story(&page, &columns(&page), TODAY);
    assert_eq!(rows[0].time, "-32 BS");
}

#[test]
fn a_held_entry_opens_its_own_page_and_a_gap_opens_nothing() {
    let page = franchise(vec![entry(1, (0.0, 0.0), &[]), gap(2, (1.0, 1.0), &[])]);
    let rows = story(&page, &columns(&page), TODAY);
    assert_eq!(
        rows[0].cell.opens(),
        Some(("sample/features", "movies", "movie:path:1"))
    );
    assert!(rows[0].cell.held());
    assert_eq!(rows[1].cell.opens(), None);
    assert!(!rows[1].cell.held());
    assert_eq!(rows[1].cell.name, "Film 2");
    assert_eq!(rows[1].cell.note, "Missing");
}

#[test]
fn a_gap_whose_release_year_is_ahead_of_today_is_coming() {
    let page = franchise(vec![Entry {
        released: "2099".into(),
        release_year: 2099,
        title: "A Later Film".into(),
        ..gap(1, (0.0, 0.0), &[])
    }]);
    let rows = story(&page, &columns(&page), TODAY);
    assert_eq!(rows[0].cell.note, "Coming 2099");
    assert_eq!(rows[0].cell.facts, "Film · 2099");
    assert_eq!(rows[0].cell.standing, Standing::Coming);
}

#[test]
fn a_coming_note_reads_as_much_of_the_date_as_the_file_knows() {
    let cases = [
        ("2027", "Coming 2027"),
        ("2027-03", "Coming March"),
        ("2027-03-12", "Coming March 12"),
        ("2027-03-01", "Coming March 1"),
        ("2027-12", "Coming December 2027"),
    ];
    for (released, note) in cases {
        let page = franchise(vec![Entry {
            released: released.into(),
            release_year: 2027,
            ..gap(1, (0.0, 0.0), &[])
        }]);
        let rows = story(&page, &columns(&page), TODAY);
        assert_eq!(rows[0].cell.note, note, "{released}");
        assert_eq!(rows[0].cell.facts, "Film · 2027", "{released}");
    }
}

#[test]
fn a_coming_note_whose_date_is_not_one_reads_the_year_it_holds() {
    let noted = |released: &str| {
        let page = franchise(vec![Entry {
            released: released.into(),
            release_year: 2027,
            ..gap(1, (0.0, 0.0), &[])
        }]);
        story(&page, &columns(&page), TODAY)[0].cell.note.clone()
    };
    assert_eq!(noted("2027-13"), "Coming 2027");
    assert_eq!(noted("2027-xx-01"), "Coming 2027");
    assert_eq!(noted("2027-00"), "Coming 2027");
}

#[test]
fn a_gap_dated_this_month_is_coming_until_its_month_and_missing_after() {
    let dated = |released: &str, today: &str| {
        let page = franchise(vec![Entry {
            released: released.into(),
            ..gap(1, (0.0, 0.0), &[])
        }]);
        story(&page, &columns(&page), today)[0].cell.standing
    };
    assert_eq!(dated("2026-10", "2026-09-30"), Standing::Coming);
    assert_eq!(dated("2026-10", "2026-10-01"), Standing::Missing);
    assert_eq!(dated("2026-09-20", "2026-09-04"), Standing::Coming);
}

#[test]
fn a_series_run_says_how_many_episodes_the_catalog_holds() {
    let serial = |episodes, held| Entry {
        kind: SERIES.into(),
        episodes,
        held,
        ..entry(1, (0.0, 0.0), &[])
    };
    let page = franchise(vec![
        serial(30, Some(held("series:path:1"))),
        serial(1, Some(held("series:path:2"))),
        Entry {
            title: "A Serial".into(),
            ..serial(0, None)
        },
    ]);
    let rows = story(&page, &columns(&page), TODAY);
    assert_eq!(rows[0].cell.facts, "Series · 1980 · 30 episodes");
    assert_eq!(rows[0].cell.note, "");
    assert_eq!(rows[1].cell.facts, "Series · 1980 · 1 episode");
    // A series no library holds counts no episodes, so its note is the
    // standing alone.
    assert_eq!(rows[2].cell.facts, "Series · 1980");
    assert_eq!(rows[2].cell.note, "Missing");
}

fn era(name: &str, from: f64, to: f64) -> Era {
    Era {
        name: name.into(),
        from,
        to,
    }
}

fn walled() -> Vec<Row> {
    let page = franchise(vec![
        entry(1, (-32.0, -32.0), &[]),
        entry(2, (-20.0, -20.0), &[]),
        entry(3, (0.0, 0.0), &[]),
        Entry {
            timed: false,
            ..entry(4, (0.0, 0.0), &[])
        },
    ]);
    story(&page, &columns(&page), TODAY)
}

#[test]
fn an_era_covers_the_rows_its_span_meets() {
    let bars = bars(&[era("The Long Survey", -40.0, 40.0)], &walled());
    assert_eq!(bars.len(), 1);
    assert_eq!((bars[0].first, bars[0].last), (0, 2));
    assert_eq!(bars[0].lane, 0);
}

#[test]
fn an_era_a_wider_one_holds_draws_in_the_inner_lane() {
    let bars = bars(
        &[
            era("The Coppice Years", -5.0, 5.0),
            era("The Long Survey", -40.0, 40.0),
        ],
        &walled(),
    );
    let lanes: Vec<(&str, usize, usize, usize)> = bars
        .iter()
        .map(|bar| (bar.label.as_str(), bar.first, bar.last, bar.lane))
        .collect();
    assert_eq!(
        lanes,
        [("The Long Survey", 0, 2, 0), ("The Coppice Years", 2, 2, 1),]
    );
}

#[test]
fn two_eras_that_overlap_and_hold_neither_take_two_lanes() {
    let bars = bars(
        &[era("Early", -40.0, -10.0), era("Late", -25.0, 40.0)],
        &walled(),
    );
    assert_eq!(bars.len(), 2);
    assert_eq!((bars[0].label.as_str(), bars[0].lane), ("Late", 0));
    assert_eq!((bars[1].label.as_str(), bars[1].lane), ("Early", 1));
}

#[test]
fn two_eras_that_never_meet_share_the_outer_lane() {
    let bars = bars(
        &[era("Early", -40.0, -25.0), era("Late", -15.0, 40.0)],
        &walled(),
    );
    assert_eq!(bars.len(), 2);
    assert!(bars.iter().all(|bar| bar.lane == 0));
}

#[test]
fn an_era_no_row_meets_draws_no_bar() {
    assert!(bars(&[era("Before", -500.0, -100.0)], &walled()).is_empty());
    assert!(bars(&[], &walled()).is_empty());
}

#[test]
fn a_wall_of_untimed_rows_draws_no_rail() {
    let page = Franchise {
        calendar: None,
        ..franchise(vec![Entry {
            timed: false,
            ..entry(1, (0.0, 0.0), &[])
        }])
    };
    let rows = story(&page, &columns(&page), TODAY);
    assert!(bars(&[era("An Era", -40.0, 40.0)], &rows).is_empty());
}

const REGION: Rectangle = Rectangle {
    x: 0.0,
    y: 100.0,
    width: 1920.0,
    height: 900.0,
};

// The room the rows have in a 1080p frame, which the art's cap is two
// fifths of.
const ROWS: f32 = 924.0;

// A wall of a card, a thin row, and a card.
fn mixed() -> Vec<Row> {
    let page = franchise(vec![
        entry(1, (-32.0, -32.0), &[]),
        gap(2, (-20.0, -20.0), &[]),
        entry(3, (0.0, 0.0), &[]),
    ]);
    story(&page, &columns(&page), TODAY)
}

#[test]
fn the_strip_and_the_cards_stand_to_the_right_of_the_time_label() {
    let columned = columned(REGION, true);
    assert_eq!(columned.x, TIME);
    assert_eq!(columned.width, REGION.width - TIME);
    assert_eq!(self::columned(REGION, false), REGION);
}

#[test]
fn a_wall_with_no_eras_no_time_labels_and_one_universe_centers_its_cards() {
    let page = Franchise {
        calendar: None,
        ..franchise(vec![entry(1, (0.0, 0.0), &[]), gap(2, (1.0, 1.0), &[])])
    };
    let rows = story(&page, &columns(&page), TODAY);
    assert!(!labelled(&rows));
    let lane = Lane::of(REGION, &[], &rows, 1);
    assert_eq!(lane.wall, REGION);
    assert_eq!(lane.strip.width, 0.0);
    assert_eq!(lane.cards.width, REGION.width - TIME);
    assert_eq!(lane.cards.center_x(), REGION.center_x());
    assert_eq!(lane.cards.y, REGION.y);
}

#[test]
fn a_time_label_earns_its_column_and_a_strip_its_pitches() {
    let rows = mixed();
    assert!(labelled(&rows));
    let one = Lane::of(REGION, &[], &rows, 1);
    assert_eq!(one.columned.x, REGION.x + TIME);
    assert_eq!(one.cards, one.columned);

    let three = Lane::of(REGION, &[], &rows, 3);
    assert_eq!(three.strip.x, REGION.x + TIME);
    assert_eq!(three.strip.width, 3.0 * metro::PITCH);
    assert_eq!(three.cards.x, three.strip.x + three.strip.width + GAP);
    assert_eq!(three.cards.x + three.cards.width, REGION.x + REGION.width);

    let bar = rail::Bar {
        first: 0,
        last: 1,
        ..rail::Bar::default()
    };
    let railed = Lane::of(REGION, &[bar], &rows, 1);
    assert_eq!(railed.wall.x, REGION.x + rail::LANE);
    assert_eq!(railed.cards.x, REGION.x + rail::LANE + TIME);
}

#[test]
fn a_card_is_the_arts_height_and_the_gaps_around_it_and_a_gap_is_thin() {
    let art = art_height(ROWS);
    let rows = mixed();
    assert_eq!(rows[0].height(art), art + 2.0 * GAP);
    assert_eq!(rows[1].height(art), THIN);
    assert_eq!(THIN, 56.0);
    assert_eq!(card_height(art), art + 2.0 * GAP);
}

#[test]
fn the_rows_start_under_the_legend_and_each_after_the_one_before_it() {
    let art = art_height(ROWS);
    let tops = tops(&mixed(), art);
    assert_eq!(tops.len(), 4);
    assert_eq!(tops[0], HEAD);
    assert_eq!(tops[1], HEAD + card_height(art) + GAP);
    assert_eq!(tops[2], tops[1] + THIN + GAP);
    assert_eq!(tops[3], tops[2] + card_height(art) + GAP);
    assert_eq!(self::tops(&[], art), [HEAD]);
}

#[test]
fn a_row_takes_the_cards_width_and_its_own_height_and_scrolls_with_the_wall() {
    let art = art_height(ROWS);
    let tops = tops(&mixed(), art);
    let cards = area(500.0, 100.0, 1200.0, 900.0);
    let card = cell_box(cards, 0, &tops, 0.0);
    assert_eq!(card.x, cards.x);
    assert_eq!(card.width, cards.width);
    assert_eq!(card.y, cards.y + HEAD);
    assert_eq!(card.height, card_height(art));

    let thin = cell_box(cards, 1, &tops, 40.0);
    assert_eq!(thin.y, cards.y + tops[1] - 40.0);
    assert_eq!(thin.height, THIN);
    assert_eq!(cell_box(cards, 9, &tops, 0.0).height, 0.0);
}

#[test]
fn the_art_stops_at_the_cap_so_a_page_shows_about_two_and_a_half_cards() {
    let art = art_height(ROWS);
    assert_eq!(art, ROWS * 0.3);
    assert!(2.0 * (card_height(art) + GAP) <= ROWS);
    assert!(3.0 * (card_height(art) + GAP) > ROWS);
    assert_eq!(art_height(0.0), 0.0);
}

#[test]
fn a_title_takes_two_lines_and_the_second_ends_in_an_ellipsis() {
    let width = 200.0;
    let room = text::fits(look::NAME, width);
    assert_eq!(
        titled("A Film", width),
        ("A Film".to_string(), String::new())
    );

    let two = "Star Wars: Episode I The Phantom Menace";
    let (first, second) = titled(two, width);
    assert!(first.chars().count() <= room);
    assert!(!second.is_empty());
    assert!(!first.ends_with(' '));
    assert!(two.starts_with(&first));

    let long: String = "wordy ".repeat(20);
    let (_, cut) = titled(&long, width);
    assert!(cut.ends_with('\u{2026}'));
    assert!(cut.chars().count() <= room);
}

#[test]
fn a_title_of_one_long_word_breaks_where_the_line_ends() {
    let width = 60.0;
    let room = text::fits(look::NAME, width);
    let (first, second) = titled(&"a".repeat(room * 3), width);
    assert_eq!(first.chars().count(), room);
    assert!(second.ends_with('\u{2026}'));
}

#[test]
fn a_focused_row_lies_wholly_inside_the_clip() {
    let columned = columned(REGION, true);
    let tops = tops(&mixed(), art_height(ROWS));
    let row = cell_box(columned, 0, &tops, 0.0);
    let marked = crate::views::marked(row);
    let clip = clipped(columned);
    assert!(marked.x >= clip.x, "{marked:?} {clip:?}");
    assert!(marked.y >= clip.y, "{marked:?} {clip:?}");
    assert!(marked.x + marked.width <= clip.x + clip.width);
    assert!(marked.y + marked.height <= clip.y + clip.height);
}

#[test]
fn the_rows_and_the_legend_band_draw_in_no_common_ground() {
    let columned = columned(REGION, true);
    let band = banded(columned);
    assert_eq!(band.y, columned.y);
    assert_eq!(band.y + band.height, clipped(columned).y);
    assert!(band.height >= text::height(1, look::HEADING));
}

#[test]
fn the_legend_band_never_climbs_over_the_top_of_the_region() {
    let columned = columned(REGION, true);
    assert_eq!(pinned(columned, 0.0), columned.y);
    assert_eq!(pinned(columned, 400.0), columned.y);
}

#[test]
fn the_time_label_stands_beside_its_own_row() {
    let tops = tops(&mixed(), art_height(ROWS));
    let box_of = time_box(REGION, 1, &tops, 40.0);
    assert_eq!(box_of.x, REGION.x);
    assert_eq!(box_of.y, REGION.y + tops[1] - 40.0);
    assert_eq!(box_of.width, TIME - GAP);
    assert_eq!(box_of.height, THIN);
}

#[test]
fn a_wall_that_fits_stands_at_its_top_and_a_long_one_scrolls() {
    let art = art_height(ROWS);
    let page = franchise((1..=40).map(|n| entry(n, (0.0, 0.0), &[])).collect());
    let tops = tops(&story(&page, &columns(&page), TODAY), art);
    assert_eq!(scroll(0, &tops, 900.0), 0.0);
    assert!(scroll(30, &tops, 900.0) > 0.0);
    assert_eq!(scroll(39, &tops, 900.0), content(&tops) - 900.0);
    assert_eq!(content(&tops), tops[40] + TAIL);
    assert_eq!(content(&[]), HEAD + TAIL);
    assert_eq!(scroll(0, &[], 900.0), 0.0);

    let two = self::tops(&mixed()[..2], art);
    assert_eq!(scroll(1, &two, 900.0), 0.0);
}

#[test]
fn a_poster_is_the_arts_height_at_the_walls_poster_ratio() {
    assert_eq!(poster_width(150.0), 150.0 / wall::POSTER);
}

#[test]
fn a_cell_draws_the_landscape_art_of_its_item_and_falls_back_to_the_poster() {
    let cases = [
        (
            vec!["a/folder.jpg", "a/landscape.jpg", "a/fanart.jpg"],
            "a/landscape.jpg",
            true,
        ),
        (
            vec!["a/folder.jpg", "a/fanart.jpg", "a/backdrop.jpg"],
            "a/fanart.jpg",
            true,
        ),
        (
            vec!["a/folder.jpg", "a/backdrop.jpg"],
            "a/backdrop.jpg",
            true,
        ),
        (
            vec!["a/folder.jpg", "a/clearlogo.png"],
            "a/folder.jpg",
            false,
        ),
        (Vec::new(), "a/folder.jpg", false),
    ];
    for (arts, drawn, wide) in cases {
        let entry = Entry {
            held: Some(Held {
                art: "a/folder.jpg".into(),
                arts: arts.iter().map(|art| art.to_string()).collect(),
                ..held("movie:path:1")
            }),
            ..entry(1, (0.0, 0.0), &[])
        };
        let cell = &story(&franchise(vec![entry]), &[], TODAY)[0].cell;
        assert_eq!((cell.art.as_str(), cell.wide), (drawn, wide), "{arts:?}");
    }
}

#[test]
fn a_gap_draws_no_art_at_all() {
    let page = franchise(vec![gap(1, (0.0, 0.0), &[])]);
    let cell = &story(&page, &[], TODAY)[0].cell;
    assert_eq!(cell.art, "");
    assert!(!cell.wide);
}

#[test]
fn a_film_the_catalog_holds_no_running_time_for_says_its_kind_and_its_year() {
    let page = franchise(vec![Entry {
        held: Some(Held {
            duration: 0,
            ..held("movie:path:1")
        }),
        ..entry(1, (0.0, 0.0), &[])
    }]);
    let cell = &story(&page, &columns(&page), TODAY)[0].cell;
    assert_eq!(cell.facts, "Film · 1980");
}

#[test]
fn a_cell_carries_the_year_and_the_tagline_of_its_item() {
    let page = franchise(vec![entry(1, (0.0, 0.0), &[])]);
    let cell = &story(&page, &columns(&page), TODAY)[0].cell;
    assert_eq!(cell.facts, "Film · 1980 · 2h 4m");
    assert_eq!(cell.note, "");
    assert_eq!(cell.blurb, "A line about movie:path:1.");
}

#[test]
fn a_cell_falls_back_to_the_plot_where_the_item_has_no_tagline() {
    let page = franchise(vec![Entry {
        held: Some(Held {
            tagline: String::new(),
            ..held("movie:path:1")
        }),
        ..entry(1, (0.0, 0.0), &[])
    }]);
    let cell = &story(&page, &columns(&page), TODAY)[0].cell;
    assert_eq!(cell.blurb, "The plot of movie:path:1.");
}

#[test]
fn a_cell_takes_the_year_of_the_items_release_where_the_file_gives_none() {
    let page = franchise(vec![Entry {
        release_year: 0,
        held: Some(Held {
            released: "1999-12-31".into(),
            ..held("movie:path:1")
        }),
        ..entry(1, (0.0, 0.0), &[])
    }]);
    let cell = &story(&page, &columns(&page), TODAY)[0].cell;
    assert_eq!(cell.facts, "Film · 1999 · 2h 4m");
}

#[test]
fn a_gap_carries_its_year_and_no_blurb() {
    let page = franchise(vec![gap(1, (0.0, 0.0), &[])]);
    let rows = story(&page, &columns(&page), TODAY);
    assert_eq!(rows[0].cell.facts, "Film · 1980");
    assert_eq!(rows[0].cell.blurb, "");
}
