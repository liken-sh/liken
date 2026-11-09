// The More like this strip on a movie's page: which pages draw it, where a
// press takes focus on it, and the order the people at the screen see.

use super::*;
use crate::catalog::Reach;

// A page of a film in no set and no franchise, with two films like it
// and two stripes of people.
fn alike() -> (Movie, Films) {
    page(Films {
        alike: true,
        credits: true,
        ..Films::default()
    })
}

fn ids(page: &Movie) -> Vec<&str> {
    let like = page.like.as_ref().expect("the page draws the strip");
    like.members.iter().map(|item| item.id.as_str()).collect()
}

#[test]
fn a_film_in_no_set_and_no_franchise_draws_the_films_like_it() {
    let (page, _) = alike();
    assert_eq!(ids(&page), ["one", "three"]);
}

#[test]
fn a_film_in_a_set_or_a_franchise_draws_no_strip() {
    for films in [
        Films {
            alike: true,
            set: true,
            ..Films::default()
        },
        Films {
            alike: true,
            franchise: true,
            ..Films::default()
        },
        Films::default(),
    ] {
        let (page, _) = page(films);
        assert!(page.like.is_none());
    }
}

#[test]
fn down_from_the_last_stripe_reaches_the_strip_and_then_the_studios() {
    let (mut page, mut source) = alike();
    page.focus = Focus::Stripe(1, 0);
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Like(0));
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Studio(0));
    page.key("up", &mut source);
    assert_eq!(page.focus, Focus::Like(0));
    page.key("up", &mut source);
    assert_eq!(page.focus, Focus::Stripe(1, 0));
}

#[test]
fn a_film_that_credits_nobody_reaches_the_strip_from_the_marks() {
    let (mut page, mut source) = page(Films {
        alike: true,
        unproduced: true,
        ..Films::default()
    });
    page.key("down", &mut source);
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Like(0));
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Like(0));
    page.key("up", &mut source);
    assert_eq!(page.focus, Focus::Marks(0));
}

#[test]
fn left_and_right_move_across_the_strip() {
    let (mut page, mut source) = alike();
    page.focus = Focus::Like(0);
    page.key("right", &mut source);
    assert_eq!(page.focus, Focus::Like(1));
    page.key("right", &mut source);
    assert_eq!(page.focus, Focus::Like(1));
    page.key("left", &mut source);
    assert_eq!(page.focus, Focus::Like(0));
}

#[test]
fn a_select_opens_the_film_s_page_over_this_one() {
    let (mut page, mut source) = alike();
    page.focus = Focus::Like(1);
    let Step::Open(Screen::Movie(opened)) = page.key("enter", &mut source) else {
        panic!("a film of the strip opens its page");
    };
    assert_eq!(opened.id, "three");
    page.focus = Focus::Like(9);
    assert!(matches!(page.key("enter", &mut source), Step::Stay));
}

#[test]
fn a_reread_holds_the_strip_and_lands_on_play_where_it_left() {
    let (mut page, mut source) = alike();
    page.focus = Focus::Like(9);
    page.reread(&mut source);
    assert_eq!(page.focus, Focus::Like(1));

    source.alike = false;
    page.reread(&mut source);
    assert_eq!(page.focus, Focus::Buttons(0));
    page.focus = Focus::Like(0);
    page.key("right", &mut source);
    assert_eq!(page.focus, Focus::Buttons(0));
}

// "one" scores higher, and someone at the screen finished it, so it goes
// after "three" once the page reads how far they got.
#[test]
fn a_film_someone_at_the_screen_finished_goes_last() {
    let (mut page, mut source) = alike();
    source.reach.insert(
        ("screening/films".to_string(), "one".to_string()),
        Reach {
            people: vec![("person-a".to_string(), 1.0)],
        },
    );
    page.read_progress(&mut source, &["person-a".to_string()], &[]);
    assert_eq!(ids(&page), ["three", "one"]);
    let like = page.like.as_ref().expect("the strip");
    assert_eq!(like.members[1].reached.share, Some(1.0));
}
