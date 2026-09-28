// The continue-watching row at the browser: what the audience's plays
// become on the page, the reasons the cards carry, and the page a press on
// a card opens.

use super::*;
use crate::catalog::Progress;

// One play of one work. It is the audience's own unless the case says
// otherwise.
fn play(kind: &str, id: &str, title: &str, progress: Progress) -> Resume {
    Resume {
        library: match kind {
            "movie" => "screening/films".to_string(),
            _ => SERIALS.to_string(),
        },
        kind: kind.to_string(),
        id: id.to_string(),
        title: title.to_string(),
        released: "1980".into(),
        art: format!("{id}.jpg"),
        progress,
        exact: true,
    }
}

// Where one play reached, in the numbers the store holds, and the second
// it was recorded.
fn reached(position: i64, duration: i64, numbers: (i64, i64), recorded: i64) -> Progress {
    Progress {
        play: format!("play-{recorded}"),
        position,
        duration,
        finished: crate::catalog::progress::finished(position, duration),
        running: false,
        recorded,
        season: numbers.0,
        episode: numbers.1,
    }
}

// One play for every branch the row folds, oldest first: a film the
// audience stopped in, a film they finished, a show they stopped inside an
// episode of, a show whose episode they finished with another after it,
// and a show whose last episode they finished.
fn plays() -> Vec<Resume> {
    vec![
        play(
            "movie",
            "movies:1",
            "Entry 1",
            reached(600, 5_400, (0, 0), 10),
        ),
        play(
            "movie",
            "movies:2",
            "Entry 2",
            reached(5_400, 5_400, (0, 0), 20),
        ),
        play(
            "series",
            SERIAL,
            "The Serial",
            reached(1_200, 2_760, (1, 2), 30),
        ),
        play(
            "series",
            OTHER_SERIAL,
            "Another Serial",
            reached(2_760, 2_760, (1, 2), 40),
        ),
        play(
            "series",
            LAST_SERIAL,
            "Last Serial",
            reached(2_760, 2_760, (2, 4), 50),
        ),
    ]
}

// The browser on a bus, over an audience of one, whose progress store
// answers these plays and whose catalog resolves one file to play.
// `orders` says whether the films carry a set and belong to franchises, so
// a finished work has something after it.
fn watched(plays: Vec<Resume>, orders: bool) -> (Browser<Fake, NoArt>, FakeBus) {
    let (mut browser, bus) = playing(vec![one_item()]);
    browser.source.continues = plays;
    browser.source.sets = orders;
    browser.source.orders = orders;
    if orders {
        browser.source.movies = 5;
    }
    browser.source.reached.insert(
        ("screening/films".to_string(), "movies:1".to_string()),
        reached(600, 5_400, (0, 0), 10),
    );
    let mut browser = browser.with_audience(
        vec![crate::audience::Person {
            name: "first".into(),
            display_name: "First".into(),
        }],
        vec!["first".into()],
    );
    browser.pump(0.0);
    (browser, bus)
}

// The browser whose catalog holds no set and no franchise, so a work the
// audience finished leaves the row and nothing takes its place.
fn continuing() -> (Browser<Fake, NoArt>, FakeBus) {
    watched(plays(), false)
}

// The browser whose catalog holds the set and the three orders, so a work
// the audience finished names the works after it.
fn following() -> (Browser<Fake, NoArt>, FakeBus) {
    watched(plays(), true)
}

// The browser with focus on the continue-watching row, the first row under
// the banner.
fn on_the_row(browser: &mut Browser<Fake, NoArt>) {
    browser.key("up");
}

// The continue-watching row's strip.
fn row(browser: &Browser<Fake, NoArt>) -> &crate::screens::home::Strip {
    strip_at(browser, 1)
}

// The two lines of every card of the row, in the order the row draws
// them.
fn lines(browser: &Browser<Fake, NoArt>) -> Vec<(String, String)> {
    row(browser)
        .items
        .iter()
        .map(|item| (item.caption.clone(), item.under.clone()))
        .collect()
}

fn ids(browser: &Browser<Fake, NoArt>) -> Vec<String> {
    row(browser)
        .items
        .iter()
        .map(|item| item.id.clone())
        .collect()
}

#[test]
fn the_row_holds_every_leaf_the_audiences_threads_offer_newest_first() {
    let (browser, _bus) = continuing();

    assert_eq!(row(&browser).heading, "Continue watching · ");
    assert_eq!(
        lines(&browser),
        [
            (
                "E03 · Segment 3".to_string(),
                "Next in Another Serial · S01".to_string()
            ),
            (
                "E02 · Segment 2".to_string(),
                "Resume · The Serial · S01".to_string()
            ),
            ("Entry 1".to_string(), "Resume".to_string()),
        ]
    );
}

#[test]
fn a_work_the_audience_finished_with_nothing_after_it_leaves_the_row() {
    let (browser, _bus) = continuing();

    assert!(!ids(&browser).contains(&"movies:2".to_string()));
    assert_eq!(ids(&browser).len(), 3);
}

#[test]
fn a_slot_the_audience_stopped_inside_carries_how_far_they_reached() {
    let (browser, _bus) = continuing();
    let reached: Vec<Option<i64>> = row(&browser)
        .items
        .iter()
        .map(|item| item.progress.map(|played| played.position))
        .collect();

    assert_eq!(reached, [None, Some(1_200), Some(600)]);
}

#[test]
fn a_film_of_the_row_reads_as_its_title_over_its_reason() {
    let (browser, _bus) = continuing();
    let film = &row(&browser).items[2];

    assert_eq!(film.library, "screening/films");
    assert_eq!(film.kind, "movies");
    assert_eq!(film.art, "movies:1.jpg");
    assert_eq!(film.caption, "Entry 1");
    assert_eq!(film.under, "Resume");
    assert_eq!(film.episode, None);
    assert_eq!(film.franchise, None);
}

#[test]
fn an_episode_of_the_row_reads_as_its_title_over_its_reason() {
    let (browser, _bus) = continuing();
    let episode = &row(&browser).items[1];

    assert_eq!(episode.kind, "episodes");
    assert_eq!(episode.id, "episode:1:2");
    assert_eq!(episode.art, "s1e2.jpg");
    assert_eq!(episode.caption, "E02 · Segment 2");
    assert_eq!(episode.under, "Resume · The Serial · S01");
    assert_eq!(
        episode.episode,
        Some(InSeries {
            series: SERIAL.into(),
            name: "The Serial".into(),
            season: 1,
            episode: 2,
        })
    );
}

#[test]
fn a_show_whose_episode_the_audience_finished_stands_at_the_next_one() {
    let (browser, _bus) = continuing();
    let next = &row(&browser).items[0];

    assert_eq!(next.id, "episode:1:3");
    assert_eq!(next.progress, None);
    assert_eq!(
        next.episode,
        Some(InSeries {
            series: OTHER_SERIAL.into(),
            name: "Another Serial".into(),
            season: 1,
            episode: 3,
        })
    );
}

#[test]
fn the_row_is_read_for_the_people_in_the_room() {
    let (browser, _bus) = continuing();

    assert_eq!(browser.source.watching, ["first"]);
}

#[test]
fn a_play_that_names_more_people_than_the_audience_seeds_no_thread() {
    let mut plays = plays();
    for play in &mut plays {
        play.exact = false;
    }
    let (browser, _bus) = watched(plays, true);

    assert!(row(&browser).items.is_empty());
}

#[test]
fn every_container_offers_its_next_leaf_with_its_reason() {
    let (browser, _bus) = following();

    // The run is cut to the last serial's first season. The play of its last
    // episode falls outside the run, so the run has no thread.

    assert_eq!(
        lines(&browser),
        [
            (
                "Entry 1".to_string(),
                "Resume · Next in The Saga".to_string()
            ),
            (
                "E03 · Segment 3".to_string(),
                "Next in Another Serial · S01".to_string()
            ),
            (
                "E02 · Segment 2".to_string(),
                "Resume · The Serial · S01".to_string()
            ),
            (
                "Entry 3".to_string(),
                "Next in The Entries · Next in The Cycle".to_string()
            ),
        ]
    );
}

#[test]
fn a_leaf_two_threads_offer_takes_the_newer_time() {
    let (browser, _bus) = following();

    // The audience stopped in the film long ago, and the saga's thread
    // reached it on the newest night.
    assert_eq!(ids(&browser)[0], "movies:1");
    assert_eq!(
        row(&browser).items[0]
            .progress
            .map(|played| played.position),
        Some(600)
    );
}

#[test]
fn a_work_the_audience_has_not_started_carries_no_bar() {
    let (browser, _bus) = following();
    let reached: Vec<Option<i64>> = row(&browser)
        .items
        .iter()
        .map(|item| item.progress.map(|played| played.position))
        .collect();

    assert_eq!(reached, [Some(600), None, Some(1_200), None]);
}

// The plays of a household that finished the last serial's second
// episode, so its series and its two franchises offer one episode.
fn mid_serial() -> Vec<Resume> {
    vec![play(
        "series",
        LAST_SERIAL,
        "Last Serial",
        reached(2_760, 2_760, (1, 2), 50),
    )]
}

#[test]
fn an_episode_a_series_and_its_franchises_offer_is_one_card_with_every_reason() {
    let (browser, _bus) = watched(mid_serial(), true);

    assert_eq!(
        lines(&browser),
        [(
            "E03 · Segment 3".to_string(),
            "Next in Last Serial · Next in The Run · Next in The Saga · S01".to_string()
        )]
    );
}

#[test]
fn a_press_on_a_card_with_a_series_reason_opens_the_series_page_on_that_episode() {
    let (mut browser, bus) = watched(mid_serial(), true);
    on_the_row(&mut browser);

    browser.key("enter");

    let page = showing_series(&browser);
    assert_eq!(page.id, LAST_SERIAL);
    assert_eq!(page.focus, SeriesFocus::Still(2));
    assert!(published_nothing(&bus));
}

#[test]
fn a_series_member_cut_to_a_run_ends_where_the_run_ends() {
    let (browser, _bus) = watched(
        vec![play(
            "series",
            LAST_SERIAL,
            "Last Serial",
            reached(2_760, 2_760, (1, 4), 50),
        )],
        true,
    );

    assert_eq!(
        lines(&browser),
        [
            (
                "E01 · Segment 1".to_string(),
                "Next in Last Serial · Next in The Saga · S02".to_string()
            ),
            ("Entry 4".to_string(), "Next in The Run".to_string()),
        ]
    );
    assert_eq!(ids(&browser)[0], "episode:2:1");
}

#[test]
fn a_press_on_a_card_only_a_franchise_offers_opens_the_franchise_page_on_the_member() {
    let (mut browser, bus) = watched(
        vec![play(
            "series",
            LAST_SERIAL,
            "Last Serial",
            reached(2_760, 2_760, (1, 4), 50),
        )],
        true,
    );
    on_the_row(&mut browser);
    browser.key("right");

    browser.key("enter");

    let page = showing_franchise(&browser);
    assert_eq!(page.title, "The Run");
    assert_eq!(page.focus, 1);
    assert_eq!(page.rows[1].cell.id, "movies:4");
    assert!(published_nothing(&bus));
}

#[test]
fn a_press_on_a_film_of_the_row_opens_its_page_on_resume() {
    let (mut browser, bus) = continuing();
    on_the_row(&mut browser);
    browser.key("right");
    browser.key("right");

    browser.key("enter");

    let page = showing_page(&browser);
    assert_eq!(page.id, "movies:1");
    assert_eq!(page.library, "screening/films");
    assert_eq!(page.buttons()[0].word(), "Resume");
    assert_eq!(page.focus, Focus::Buttons(0));
    assert_eq!(
        page.progress.as_ref().map(|reached| reached.position),
        Some(600)
    );
    assert!(published_nothing(&bus));
}

#[test]
fn a_press_on_an_episode_of_the_row_opens_the_series_page_on_that_episode() {
    let (mut browser, bus) = continuing();
    on_the_row(&mut browser);
    browser.key("right");

    browser.key("enter");

    let page = showing_series(&browser);
    assert_eq!(page.id, SERIAL);
    assert_eq!(page.library, SERIALS);
    assert_eq!(page.focus, SeriesFocus::Still(1));
    assert_eq!(page.stills[1].fitted, "Segment 2");
    assert!(published_nothing(&bus));
}

#[test]
fn a_press_on_an_episode_the_audience_has_not_started_opens_the_series_page_on_it() {
    let (mut browser, bus) = continuing();
    on_the_row(&mut browser);

    browser.key("enter");

    let page = showing_series(&browser);
    assert_eq!(page.id, OTHER_SERIAL);
    assert_eq!(page.focus, SeriesFocus::Still(2));
    assert!(published_nothing(&bus));
}

#[test]
fn a_row_the_store_answers_nothing_for_holds_nothing() {
    let (browser, _bus) = watched(Vec::new(), false);

    assert!(row(&browser).items.is_empty());
    assert_eq!(row(&browser).heading, "Continue watching · ");
    assert_eq!(row(&browser).letters, ["F"]);
}

#[test]
fn the_heading_carries_the_letters_of_the_people_at_the_screen() {
    let (browser, _bus) = continuing();

    assert_eq!(row(&browser).letters, ["F"]);
    assert!(!row(&browser).rung);
}

#[test]
fn left_from_the_first_card_reaches_the_circles_and_right_returns() {
    let (mut browser, _bus) = continuing();
    on_the_row(&mut browser);

    assert!(browser.key("left"));
    assert!(row(&browser).rung);
    assert_eq!(row(&browser).focus, 0);
    assert!(browser.picker.is_none());

    assert!(browser.key("right"));
    assert!(!row(&browser).rung);
    assert_eq!(row(&browser).focus, 0);
}

#[test]
fn enter_on_the_circles_raises_the_picker_with_the_room_chosen() {
    let (mut browser, _bus) = continuing();
    on_the_row(&mut browser);
    browser.key("left");

    assert!(browser.key("enter"));

    let picker = browser.picker.as_ref().expect("the picker stands");
    assert_eq!(picker.tiles(), 1);
    assert!(picker.holds(0));
}

#[test]
fn up_from_any_card_reaches_the_circles_and_up_again_leaves_the_row_upward() {
    let (mut browser, _bus) = continuing();
    on_the_row(&mut browser);
    browser.key("right");
    assert_eq!(row(&browser).focus, 1);

    assert!(browser.key("up"));
    assert!(row(&browser).rung);
    assert_eq!(showing_home(&browser).focus, 1);
    assert!(!browser.on_strip);

    // The banner over this row holds nothing, so up goes on to the
    // browser's strip, as up from a card of the row does.
    assert!(browser.key("up"));
    assert!(browser.on_strip);
    assert_eq!(showing_home(&browser).focus, 1);
}

#[test]
fn down_from_the_circles_returns_to_the_card_focus_left() {
    let (mut browser, _bus) = continuing();
    on_the_row(&mut browser);
    browser.key("right");
    browser.key("up");
    assert!(row(&browser).rung);

    assert!(browser.key("down"));

    assert_eq!(showing_home(&browser).focus, 1);
    assert!(!row(&browser).rung);
    assert_eq!(row(&browser).focus, 1);
}

#[test]
fn leaving_the_row_puts_focus_back_on_the_cards() {
    let (mut browser, _bus) = continuing();
    on_the_row(&mut browser);
    browser.key("left");
    assert!(row(&browser).rung);

    // Down returns to the card, down again leaves the row, and up comes
    // back to the row on its cards.
    browser.key("down");
    browser.key("down");
    assert_ne!(showing_home(&browser).focus, 1);
    browser.key("up");

    assert_eq!(showing_home(&browser).focus, 1);
    assert!(!row(&browser).rung);
}

// Whether the home page holds the continue-watching row at all.
fn holds_the_row(browser: &Browser<Fake, NoArt>) -> bool {
    showing_home(browser)
        .blocks
        .iter()
        .filter_map(|block| block.strip())
        .any(|strip| strip.row == crate::screens::home::Row::Continue)
}

#[test]
fn an_empty_audience_draws_no_continue_watching_row() {
    let (mut browser, _bus) = continuing();
    assert!(holds_the_row(&browser));

    // The answer lapses, the picker rises, and back answers nobody.
    browser.tick(crate::audience::IDLE_SECONDS + 1.0);
    browser.key("escape");
    browser.pump(crate::audience::IDLE_SECONDS + 2.0);

    assert!(!holds_the_row(&browser));
}

#[test]
fn the_row_comes_back_with_the_pickers_answer() {
    let (mut browser, _bus) = continuing();
    browser.tick(crate::audience::IDLE_SECONDS + 1.0);
    browser.key("escape");
    browser.pump(crate::audience::IDLE_SECONDS + 2.0);
    assert!(!holds_the_row(&browser));

    // The people key raises the picker, and the one tile is chosen and
    // answered.
    browser.key("people");
    browser.key("enter");
    browser.key("down");
    browser.key("enter");
    browser.pump(crate::audience::IDLE_SECONDS + 3.0);

    assert!(holds_the_row(&browser));
    assert_eq!(row(&browser).letters, ["F"]);
}
