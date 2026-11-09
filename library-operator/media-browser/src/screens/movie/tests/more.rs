// The More from strip on a movie's page: which studio takes it, what it
// leaves out, where a press takes focus on it, and what a select opens.

use super::*;
use crate::catalog::Reach;

// A page of a film in no set whose first studio made six more titles,
// with two stripes of people.
fn made() -> (Movie, Films) {
    page(Films {
        made: true,
        credits: true,
        ..Films::default()
    })
}

fn ids(page: &Movie) -> Vec<&str> {
    let more = page.more.as_ref().expect("the page draws the strip");
    more.members.iter().map(|item| item.id.as_str()).collect()
}

fn named(page: &Movie) -> Vec<&str> {
    page.foot.studios().collect()
}

#[test]
fn a_studio_that_made_enough_else_draws_its_strip_and_leaves_the_line() {
    let (page, _) = made();
    let more = page.more.as_ref().expect("the page draws the strip");
    assert_eq!(more.heading, "More from A Studio");
    assert_eq!(ids(&page), ["one", "four", "five", "six", "seven", "three"]);
    assert_eq!(named(&page), ["Another Studio"]);
}

#[test]
fn the_strip_leaves_out_what_the_set_and_the_franchise_strips_draw() {
    for films in [
        Films {
            made: true,
            set: true,
            ..Films::default()
        },
        Films {
            made: true,
            franchise: true,
            ..Films::default()
        },
    ] {
        let (page, _) = page(films);
        assert_eq!(ids(&page), ["four", "five", "six", "seven"]);
    }
}

#[test]
fn a_studio_with_too_few_other_titles_stays_a_name() {
    let (page, _) = credited();
    assert!(page.more.is_none());
    assert_eq!(named(&page), ["A Studio", "Another Studio"]);
}

#[test]
fn down_from_the_last_stripe_reaches_the_strip_and_then_the_studios() {
    let (mut page, mut source) = made();
    page.focus = Focus::Stripe(1, 0);
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::More(0));
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Studio(0));
    page.key("up", &mut source);
    assert_eq!(page.focus, Focus::More(0));
    page.key("up", &mut source);
    assert_eq!(page.focus, Focus::Stripe(1, 0));
}

#[test]
fn the_strip_follows_more_like_this() {
    let (mut page, mut source) = page(Films {
        made: true,
        alike: true,
        credits: true,
        ..Films::default()
    });
    page.focus = Focus::Like(0);
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::More(0));
    page.key("up", &mut source);
    assert_eq!(page.focus, Focus::Like(0));
}

#[test]
fn the_strip_follows_the_set_strip_where_the_film_credits_nobody() {
    let (mut page, mut source) = page(Films {
        made: true,
        set: true,
        ..Films::default()
    });
    page.focus = Focus::Strip(1);
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::More(0));
    page.key("up", &mut source);
    assert_eq!(page.focus, Focus::Strip(1));
}

// The body names one studio, and the strip takes it, so the line draws
// nothing and the strip is the last stop of the page.
#[test]
fn a_page_whose_one_studio_draws_the_strip_ends_at_the_strip() {
    let (mut page, mut source) = page(Films {
        made: true,
        solo: true,
        credits: true,
        ..Films::default()
    });
    assert!(named(&page).is_empty());
    page.focus = Focus::More(0);
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::More(0));
}

#[test]
fn right_moves_across_the_titles_to_see_all() {
    let (mut page, mut source) = made();
    page.focus = Focus::More(5);
    page.key("right", &mut source);
    assert_eq!(page.focus, Focus::More(6));
    page.key("right", &mut source);
    assert_eq!(page.focus, Focus::More(6));
}

#[test]
fn a_select_opens_a_title_s_page_and_see_all_the_studio_s_wall() {
    let (mut page, mut source) = made();
    page.focus = Focus::More(0);
    let Step::Open(Screen::Movie(opened)) = page.key("enter", &mut source) else {
        panic!("a title of the strip opens its page over this one");
    };
    assert_eq!(opened.id, "one");
    page.focus = Focus::More(6);
    let Step::Open(Screen::Wall(wall)) = page.key("enter", &mut source) else {
        panic!("see all opens the studio's wall");
    };
    assert_eq!(wall.slots.query, Query::studio("A Studio"));
}

#[test]
fn a_reread_holds_the_strip_and_lands_on_play_where_it_left() {
    let (mut page, mut source) = made();
    page.focus = Focus::More(9);
    page.reread(&mut source);
    assert_eq!(page.focus, Focus::More(6));

    source.made = false;
    page.reread(&mut source);
    assert!(page.more.is_none());
    assert_eq!(page.focus, Focus::Buttons(0));
    page.focus = Focus::More(0);
    page.key("right", &mut source);
    assert_eq!(page.focus, Focus::Buttons(0));
}

#[test]
fn a_title_someone_at_the_screen_reached_draws_their_progress() {
    let (mut page, mut source) = made();
    source.reach.insert(
        ("screening/films".to_string(), "four".to_string()),
        Reach {
            people: vec![("person-a".to_string(), 0.5)],
            ..Reach::default()
        },
    );
    page.read_progress(&mut source, &["person-a".to_string()], &[]);
    let more = page.more.as_ref().expect("the strip");
    assert_eq!(ids(&page)[1], "four");
    assert_eq!(more.members[1].reached.share, Some(0.5));
}
