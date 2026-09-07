// The series page over an invented catalog: the words it builds out of a
// body and a season of episodes, where a press takes focus across the
// dividers, and what a select asks the browser to play.

mod resume;
mod seasons;
mod serials;

use super::*;
use serials::*;

#[test]
fn a_page_opens_on_the_first_episode_of_the_first_season() {
    let (page, _) = page(Serials::default());
    assert_eq!(page.focus, Focus::Still(0));
    assert_eq!(page.title, "Serial One");
    assert_eq!(page.facts, "2004 · 3 seasons · TV-14");
    assert_eq!(page.tagline, "One line of it.");
    assert_eq!(page.stills.len(), 14);
}

#[test]
fn the_facts_line_carries_the_genres_and_the_banner_line_does_not() {
    let details = SeriesDetails {
        released: "2026-01-02".into(),
        seasons: 2,
        rating: "TV-MA".into(),
        genres: vec!["Drama".into(), "Thriller".into()],
        ..SeriesDetails::default()
    };

    assert_eq!(
        facts_of(&details),
        "2026 · 2 seasons · TV-MA · Drama, Thriller"
    );
    assert_eq!(facts_without_genres(&details), "2026 · 2 seasons · TV-MA");
}

#[test]
fn a_page_opens_with_focus_on_the_episode_it_was_asked_for() {
    let mut source = Serials::default();
    let page = Series::open_at("screening/serials", SERIES, (2, 3), &mut source)
        .expect("the catalog holds it");
    assert_eq!(page.focus, Focus::Still(7));
    assert_eq!(
        page.focused().map(|still| still.fitted.as_str()),
        Some("Segment 3")
    );
}

#[test]
fn a_page_asked_for_an_episode_it_does_not_hold_opens_on_the_first() {
    let mut source = Serials::default();
    let page = Series::open_at("screening/serials", SERIES, (9, 9), &mut source)
        .expect("the catalog holds it");
    assert_eq!(page.focus, Focus::Still(0));
    assert!(Series::open_at("screening/serials", "series:gone", (1, 1), &mut source).is_none());
}

#[test]
fn a_series_the_library_does_not_hold_has_no_page() {
    let mut source = Serials::default();
    assert!(Series::open("screening/serials", "series:gone", &mut source).is_none());
}

#[test]
fn every_season_gets_a_divider_naming_it_and_its_first_year() {
    let (page, _) = page(Serials::default());
    let names: Vec<&str> = page
        .seasons
        .iter()
        .map(|season| season.name.as_str())
        .collect();
    assert_eq!(
        names,
        [
            "Season 1 · 2004 · 5 episodes",
            "Season 2 · 2005 · 3 episodes",
            "Season 3 · 2006 · 6 episodes"
        ]
    );
    assert_eq!(page.seasons[1].run, Run { first: 5, count: 3 });
}

#[test]
fn a_season_whose_first_episode_holds_no_year_names_the_season_and_its_count() {
    let (page, _) = page(Serials {
        undated: true,
        ..Serials::default()
    });
    assert_eq!(page.seasons[0].name, "Season 1 · 5 episodes");
}

#[test]
fn a_still_leads_with_its_name_and_carries_the_facts_the_header_draws() {
    let (page, _) = page(Serials::default());
    assert_eq!(page.stills[1].fitted, "Segment 2");
    assert_eq!(page.stills[1].under, "E02 · 46m");
    assert_eq!(page.stills[1].facts, "S01 · E02 · Segment 2");
    assert_eq!(page.stills[1].aired, "46m · March 2, 2004");
    assert_eq!(page.stills[1].plot, "The plot of S1 E2.");
    assert_eq!(page.stills[1].art, "s1e2.jpg");
}

#[test]
fn left_and_right_stay_inside_one_season() {
    let (mut page, mut source) = page(Serials::default());
    assert_eq!(still(pressed(&mut page, &mut source, "left")), 0);
    page.focus = Focus::Still(4);
    assert_eq!(still(pressed(&mut page, &mut source, "right")), 4);
    page.focus = Focus::Still(5);
    assert_eq!(still(pressed(&mut page, &mut source, "left")), 5);
    assert_eq!(still(pressed(&mut page, &mut source, "right")), 6);
}

#[test]
fn down_and_up_cross_the_dividers() {
    let (mut page, mut source) = page(Serials::default());
    assert_eq!(still(pressed(&mut page, &mut source, "down")), 4);
    assert_eq!(still(pressed(&mut page, &mut source, "down")), 5);
    assert_eq!(still(pressed(&mut page, &mut source, "down")), 8);
    assert_eq!(still(pressed(&mut page, &mut source, "up")), 5);
    assert_eq!(still(pressed(&mut page, &mut source, "up")), 4);
}

#[test]
fn the_first_and_the_last_row_of_a_page_hold_focus() {
    let (mut page, mut source) = page(Serials::default());
    assert_eq!(still(pressed(&mut page, &mut source, "up")), 0);
    page.focus = Focus::Still(13);
    assert_eq!(still(pressed(&mut page, &mut source, "down")), 13);
}

#[test]
fn the_header_shows_the_focused_episodes_plot_in_place_of_the_series() {
    let (mut page, mut source) = page(Serials::default());
    assert_eq!(page.plot, "The series' own plot.");
    pressed(&mut page, &mut source, "down");
    let focused = page.focused().expect("a still holds focus");
    assert_eq!(focused.facts, "S01 · E05 · Segment 5");
    assert_eq!(focused.aired, "46m · March 5, 2004");
    assert_eq!(focused.plot, "The plot of S1 E5.");
}

#[test]
fn the_scores_are_the_series_own_and_stand_while_a_still_holds_focus() {
    let (mut page, mut source) = page(Serials::default());
    let marks: Vec<ratings::Mark> = page.ratings.iter().map(|score| score.mark).collect();
    assert_eq!(marks, [ratings::Mark::Imdb, ratings::Mark::Tomato]);

    pressed(&mut page, &mut source, "right");
    assert_eq!(page.ratings.len(), 2);
}

#[test]
fn the_foot_names_the_studios_and_the_focused_episode_s_file() {
    let (mut page, mut source) = page(Serials::default());
    let first: Vec<String> = page
        .foot
        .rows()
        .map(|row| row.content.to_string())
        .collect();
    assert_eq!(first, ["A Studio", "1920×1080 · x264 · AAC · 1.0 GB"]);

    pressed(&mut page, &mut source, "right");
    let second: Vec<String> = page
        .foot
        .rows()
        .map(|row| row.content.to_string())
        .collect();
    assert_eq!(second, ["A Studio", "1920×1080 · x264 · AAC · 2.0 GB"]);
}

#[test]
fn a_page_draws_a_stripe_for_every_part_the_series_credits() {
    let (credited, _) = credited(Serials::default());
    let headings: Vec<&str> = credited
        .stripes
        .bands()
        .iter()
        .map(|band| band.heading)
        .collect();
    assert_eq!(headings, ["Cast", "Crew"]);

    let (bare, _) = page(Serials::default());
    assert!(bare.stripes.is_empty());
}

#[test]
fn down_from_the_last_row_reaches_the_stripes_and_up_returns_to_it() {
    let (mut page, mut source) = credited(Serials::default());
    page.focus = Focus::Still(13);

    assert_eq!(pressed(&mut page, &mut source, "down"), Focus::Stripe(0, 0));
    assert_eq!(
        pressed(&mut page, &mut source, "right"),
        Focus::Stripe(0, 1)
    );
    assert_eq!(pressed(&mut page, &mut source, "down"), Focus::Stripe(1, 0));
    assert_eq!(pressed(&mut page, &mut source, "up"), Focus::Stripe(0, 0));
    assert_eq!(pressed(&mut page, &mut source, "up"), Focus::Still(13));
}

#[test]
fn the_header_shows_the_series_own_plot_while_a_stripe_holds_focus() {
    let (mut page, mut source) = credited(Serials::default());
    page.focus = Focus::Stripe(1, 0);
    assert!(page.focused().is_none());
    assert_eq!(page.plot, "The series' own plot.");
    assert_eq!(
        pressed(&mut page, &mut source, "right"),
        Focus::Stripe(1, 0)
    );
}

#[test]
fn a_select_on_a_headshot_opens_the_persons_page() {
    let (mut page, mut source) = credited(Serials::default());
    page.focus = Focus::Stripe(0, 1);

    let Step::Open(Screen::Person(opened)) = page.key("enter", &mut source) else {
        panic!("a select on a headshot opens the person");
    };
    assert_eq!(opened.path, ".contributors/Another");
}

#[test]
fn a_series_with_no_episodes_reaches_its_stripes() {
    let (mut page, mut source) = credited(Serials {
        empty: true,
        ..Serials::default()
    });
    assert!(page.stills.is_empty());

    assert_eq!(pressed(&mut page, &mut source, "down"), Focus::Stripe(0, 0));
    assert_eq!(pressed(&mut page, &mut source, "up"), Focus::Stripe(0, 0));
}

#[test]
fn a_reread_whose_credits_left_while_a_stripe_held_focus_returns_to_the_wall() {
    let (mut page, mut source) = credited(Serials {
        credits: true,
        ..Serials::default()
    });
    page.focus = Focus::Stripe(0, 1);
    source.credits = false;
    page.reread(&mut source);
    assert_eq!(page.focus, Focus::Still(0));
}

#[test]
fn a_select_on_a_stripe_slot_past_the_credits_opens_nothing_on_a_series() {
    let (mut page, mut source) = credited(Serials {
        credits: true,
        ..Serials::default()
    });
    page.focus = Focus::Stripe(9, 9);
    assert!(matches!(page.key("enter", &mut source), Step::Stay));
}

#[test]
fn a_reread_whose_credits_left_returns_focus_to_the_wall() {
    let (mut page, mut source) = credited(Serials::default());
    page.focus = Focus::Stripe(1, 1);
    source.credits = false;

    page.reread(&mut source);

    assert_eq!(page.focus, Focus::Still(0));
}

#[test]
fn select_plays_the_episode_and_the_rest_of_its_season() {
    let (mut page, mut source) = page(Serials::default());
    page.focus = Focus::Still(6);
    let Step::Play {
        library, selection, ..
    } = page.key("enter", &mut source)
    else {
        panic!("a select on a still plays it");
    };
    assert_eq!(library, "screening/serials");
    assert_eq!(
        selection,
        Selection::Episode {
            series: SERIES.into(),
            season: 2,
            episode: 2,
        }
    );
}

#[test]
fn a_series_with_no_episodes_draws_its_own_plot_and_plays_nothing() {
    let (page, _) = page(Serials {
        empty: true,
        ..Serials::default()
    });
    assert!(page.stills.is_empty());
    assert!(page.seasons.is_empty());
    assert_eq!(page.plot, "The series' own plot.");

    let (mut page, mut source) = (page, Serials::default());
    source.empty = true;
    assert!(matches!(page.key("enter", &mut source), Step::Stay));
    assert_eq!(still(pressed(&mut page, &mut source, "down")), 0);
}

#[test]
fn a_reread_that_shortens_the_wall_clamps_the_focus() {
    let (mut page, mut source) = page(Serials::default());
    page.focus = Focus::Still(13);
    source.last = Some(1);
    page.reread(&mut source);
    assert_eq!(page.stills.len(), 9);
    assert_eq!(page.focus, Focus::Still(8));
}

#[test]
fn a_reread_of_a_series_that_left_the_library_keeps_the_page() {
    let (mut page, _) = page(Serials::default());
    let mut gone = Serials::default();
    page.id = "series:gone".into();
    page.reread(&mut gone);
    assert_eq!(page.stills.len(), 14);
}

#[test]
fn a_series_whose_episodes_have_not_landed_names_no_seasons() {
    assert_eq!(seasons_of(0), "");
    assert_eq!(seasons_of(1), "1 season");
    assert_eq!(seasons_of(4), "4 seasons");
}

// A page whose series is in a franchise and credits its people, which
// is the page with every block on it.
fn ordered() -> (Series, Serials) {
    page(Serials {
        franchise: true,
        credits: true,
        ..Serials::default()
    })
}

#[test]
fn a_series_in_a_franchise_draws_a_strip_after_its_last_season() {
    let (ordered, _) = ordered();
    assert_eq!(ordered.franchises.bands().len(), 1);
    assert_eq!(
        ordered.franchises.bands()[0].heading,
        "The Cycle · a franchise of 2 series"
    );
    assert_eq!(ordered.franchises.bands()[0].current, Some(0));
    let (plain, _) = page(Serials::default());
    assert!(plain.franchises.is_empty());
}

#[test]
fn down_from_the_last_row_of_stills_reaches_the_franchise_strip() {
    let (mut page, mut source) = ordered();
    page.focus = Focus::Still(13);
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Franchise(0, Place::Heading));
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Franchise(0, Place::Member(0)));
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Stripe(0, 0));
}

#[test]
fn up_from_the_stripes_climbs_back_through_the_franchise_strip() {
    let (mut page, mut source) = ordered();
    page.focus = Focus::Stripe(0, 0);
    page.key("up", &mut source);
    assert_eq!(page.focus, Focus::Franchise(0, Place::Member(0)));
    page.key("up", &mut source);
    assert_eq!(page.focus, Focus::Franchise(0, Place::Heading));
    page.key("up", &mut source);
    assert_eq!(page.focus, Focus::Still(13));
}

#[test]
fn a_press_on_the_strips_heading_opens_the_franchises_page() {
    let (mut page, mut source) = ordered();
    page.focus = Focus::Franchise(0, Place::Heading);
    assert!(matches!(
        page.key("enter", &mut source),
        Step::Open(Screen::Franchise(_))
    ));
}

#[test]
fn a_press_on_a_member_replaces_this_page_with_that_members() {
    let (mut page, mut source) = ordered();
    page.focus = Focus::Franchise(0, Place::Member(1));
    assert!(matches!(
        page.key("enter", &mut source),
        Step::Replace(Screen::Series(_))
    ));
}

#[test]
fn a_reread_holds_the_rung_of_the_franchise_strip() {
    let (mut page, mut source) = ordered();
    page.focus = Focus::Franchise(0, Place::Member(1));
    page.reread(&mut source);
    assert_eq!(page.focus, Focus::Franchise(0, Place::Member(1)));

    source.franchise = false;
    page.reread(&mut source);
    assert_eq!(page.focus, Focus::Still(0));
}

#[test]
fn a_series_with_no_episode_holds_focus_on_its_franchise_strip() {
    let (mut page, mut source) = page(Serials {
        empty: true,
        franchise: true,
        ..Serials::default()
    });
    page.focus = Focus::Franchise(0, Place::Heading);
    page.key("up", &mut source);
    assert_eq!(page.focus, Focus::Franchise(0, Place::Heading));
}
