// The play requests the browser publishes, and the choices its screens
// resolve them from.

use super::*;

#[test]
fn play_on_a_movies_page_asks_the_operator_to_play_it() {
    let (mut browser, bus) = playing(vec![one_item()]);
    browser.key("enter");
    browser.key("enter");

    browser.key("enter");

    assert_eq!(
        browser.source.chosen,
        Some((
            "screening/films".to_string(),
            Selection::Movie {
                id: "movies:1".into()
            }
        ))
    );
    assert_eq!(
        published(&bus),
        serde_json::json!({
            "library": "screening/films",
            "slug": "some-film-1999",
            "items": [{
                "path": "Some Film (1999)/Some Film (1999).mkv",
                "presentation": {
                    "type": "video",
                    "hint": "movie",
                    "title": "Some Film",
                    "year": 1999,
                },
            }],
        })
    );
}

#[test]
fn play_opens_no_screen() {
    let (mut browser, _bus) = playing(vec![one_item()]);
    browser.key("enter");
    browser.key("enter");

    browser.key("enter");

    assert_eq!(browser.stack.len(), 2);
}

#[test]
fn the_trailer_button_asks_for_the_trailer() {
    let (mut browser, _bus) = playing(vec![one_item()]);
    browser.source.trailers = true;
    browser.key("enter");
    browser.key("enter");
    browser.key("right");

    browser.key("enter");

    assert_eq!(
        browser.source.chosen,
        Some((
            "screening/films".to_string(),
            Selection::Trailer {
                id: "movies:1".into()
            }
        ))
    );
}

#[test]
fn a_sibling_in_the_strip_replaces_the_page_and_back_reaches_the_wall() {
    let (mut browser, _bus) = playing(vec![one_item()]);
    browser.source.sets = true;
    browser.key("enter");
    browser.key("enter");
    assert_eq!(showing_page(&browser).id, "movies:1");

    browser.key("down");
    browser.key("right");
    assert_eq!(showing_page(&browser).focus, Focus::Strip(1));
    browser.key("enter");

    assert_eq!(showing_page(&browser).id, "movies:2");
    assert_eq!(browser.stack.len(), 2);

    browser.key("escape");

    assert_eq!(showing_wall(&browser).slots.focus, 0);
}

#[test]
fn a_select_on_an_episode_names_the_episode_the_still_carries() {
    let (mut browser, _bus) = playing(vec![one_item()]);
    browser.key("down");
    browser.key("enter");
    browser.key("enter");
    browser.key("down");
    browser.key("right");

    browser.key("enter");

    assert_eq!(
        browser.source.chosen,
        Some((
            "screening/serials".to_string(),
            Selection::Episode {
                series: "series:1".into(),
                season: 2,
                episode: 2,
            }
        ))
    );
}

#[test]
fn a_select_on_an_episode_publishes_the_list_in_the_order_it_resolved() {
    let mut second = one_item();
    second.path = "Later.mkv".into();
    let (mut browser, bus) = playing(vec![one_item(), second]);
    browser.key("down");
    browser.key("enter");
    browser.key("enter");

    browser.key("enter");

    let request = published(&bus);
    assert_eq!(
        request["items"][0]["path"],
        "Some Film (1999)/Some Film (1999).mkv"
    );
    assert_eq!(request["items"][1]["path"], "Later.mkv");
}

#[test]
fn a_select_on_a_series_descends_and_asks_for_no_play() {
    let (mut browser, bus) = playing(vec![one_item()]);
    browser.key("down");
    browser.key("enter");

    browser.key("enter");

    assert_eq!(browser.stack.len(), 2);
    assert_eq!(browser.source.chosen, None);
    assert!(published_nothing(&bus));
}

#[test]
fn a_title_with_no_file_to_play_publishes_nothing() {
    let (mut browser, bus) = playing(Vec::new());
    browser.key("enter");
    browser.key("enter");

    browser.key("enter");

    assert!(browser.source.chosen.is_some());
    assert!(published_nothing(&bus));
}

// An older library operator names no play topic, and the browser then
// browses and asks for nothing.
#[test]
fn a_select_with_no_play_topic_publishes_nothing() {
    let bus = FakeBus::default();
    let mut browser = browser(3).with_bus(Some(Box::new(bus.clone())), String::new());
    browser.source.items = vec![one_item()];
    browser.key("enter");
    browser.key("enter");

    browser.key("enter");

    assert!(browser.source.chosen.is_some());
    assert!(published_nothing(&bus));
}

#[test]
fn a_select_on_a_movie_with_no_bus_publishes_nothing() {
    let mut browser = browser(3);
    browser.source.items = vec![one_item()];
    browser.key("enter");
    browser.key("enter");

    browser.key("enter");

    assert_eq!(browser.stack.len(), 2);
    assert!(browser.source.chosen.is_some());
}
