use super::*;

const FILMS: &str = "default/films";
const SHOWS: &str = "default/shows";

// One play of a film: its name, the people on it, how far it reached of a
// 6000-second film, and when it was last recorded.
fn film(play: &str, people: &[&str], position: i64, recorded: i64) -> Seen {
    played(FILMS, MOVIE, "film", play, people, position, recorded)
}

fn played(
    library: &str,
    kind: &str,
    id: &str,
    play: &str,
    people: &[&str],
    position: i64,
    recorded: i64,
) -> Seen {
    Seen {
        resume: Resume {
            library: library.to_string(),
            kind: kind.to_string(),
            id: id.to_string(),
            progress: Progress {
                play: play.to_string(),
                position,
                duration: 6000,
                finished: position >= 5900,
                recorded,
                ..Progress::default()
            },
            ..Resume::default()
        },
        people: people.iter().map(|name| name.to_string()).collect(),
    }
}

// One play of one episode of the show.
fn episode(play: &str, people: &[&str], at: (i64, i64), recorded: i64) -> Seen {
    let mut seen = played(SHOWS, SERIES, "show", play, people, 600, recorded);
    seen.resume.progress.season = at.0;
    seen.resume.progress.episode = at.1;
    seen
}

fn names(people: &[&str]) -> Vec<String> {
    people.iter().map(|name| name.to_string()).collect()
}

fn plays(resumes: &[Resume]) -> Vec<(&str, bool)> {
    resumes
        .iter()
        .map(|resume| (resume.progress.play.as_str(), resume.exact))
        .collect()
}

#[test]
fn every_takes_the_plays_all_of_the_audience_is_on_newest_first() {
    let seen = [
        film("alone", &["first"], 600, 10),
        film("pair", &["first", "second"], 900, 30),
        film("crowd", &["first", "second", "third"], 1200, 20),
    ];

    let answer = every(&seen, &names(&["first", "second"]), None);

    assert_eq!(plays(&answer), [("pair", true), ("crowd", false)]);
}

#[test]
fn every_for_nobody_takes_the_plays_that_name_nobody() {
    let seen = [
        film("named", &["first"], 600, 10),
        film("bare", &[], 900, 20),
    ];

    assert_eq!(plays(&every(&seen, &[], None)), [("bare", true)]);
}

#[test]
fn every_of_one_work_leaves_the_other_works_out() {
    let seen = [
        film("this", &["first"], 600, 10),
        played(FILMS, MOVIE, "other", "that", &["first"], 600, 20),
    ];

    let answer = every(&seen, &names(&["first"]), Some((FILMS, "film")));

    assert_eq!(plays(&answer), [("this", true)]);
}

#[test]
fn newest_takes_the_latest_play_and_of_a_tie_the_name_that_sorts_last() {
    let seen = [
        film("finished", &["first", "second"], 5950, 10),
        film("b-again", &["first", "second", "third"], 600, 20),
        film("a-again", &["first", "second"], 900, 20),
    ];

    let answer = every(&seen, &names(&["first", "second"]), None);

    assert_eq!(
        newest(&answer).map(|play| play.play.as_str()),
        Some("b-again")
    );
    assert_eq!(newest(&[]), None);
}

#[test]
fn an_episode_is_kept_only_when_every_person_has_a_play_of_it() {
    let seen = [
        episode("one-first", &["first"], (1, 1), 10),
        episode("one-second", &["second"], (1, 1), 20),
        episode("two-first", &["first"], (1, 2), 30),
    ];

    let answer = episodes(&seen, SHOWS, "show", &names(&["first", "second"]));

    let kept: Vec<&str> = answer.iter().map(|row| row.play.as_str()).collect();
    assert_eq!(kept, ["one-second"]);
}

#[test]
fn episodes_answer_in_aired_order() {
    let seen = [
        episode("late", &["first"], (2, 1), 10),
        episode("early", &["first"], (1, 3), 20),
    ];

    let answer = episodes(&seen, SHOWS, "show", &names(&["first"]));

    let at: Vec<(i64, i64)> = answer.iter().map(|row| (row.season, row.episode)).collect();
    assert_eq!(at, [(1, 3), (2, 1)]);
}

#[test]
fn each_person_reaches_their_furthest_play_of_a_film() {
    let seen = [
        film("finished", &["first"], 5950, 10),
        film("restarted", &["first"], 300, 20),
        film("together", &["first", "second"], 1500, 15),
    ];

    let reach = &films(&seen, &names(&["first", "second"]))[&(FILMS.into(), "film".into())];

    assert_eq!(
        reach.people,
        [("first".to_string(), 1.0), ("second".to_string(), 0.25)]
    );
    assert_eq!(reach.shared(&names(&["first", "second"])), Some(0.25));
}

#[test]
fn a_film_one_person_has_not_played_draws_no_shared_bar() {
    let seen = [film("alone", &["first"], 3000, 10)];
    let audience = names(&["first", "second"]);

    let reach = &films(&seen, &audience)[&(FILMS.into(), "film".into())];

    assert_eq!(reach.names(), ["first"]);
    assert_eq!(reach.shared(&audience), None);
}

#[test]
fn a_film_for_nobody_reaches_its_furthest_play_that_names_nobody() {
    let seen = [
        film("bare", &[], 1500, 10),
        film("named", &["first"], 3000, 20),
    ];

    let reach = &films(&seen, &[])[&(FILMS.into(), "film".into())];

    assert_eq!(reach.shared(&[]), Some(0.25));
}

#[test]
fn a_series_play_reaches_no_film() {
    let seen = [episode("show", &["first"], (1, 1), 10)];

    assert!(films(&seen, &names(&["first"])).is_empty());
}

// A clear writes a play at 0, and a person who cleared a film has not seen
// it, whatever they reached before. A play after the clear counts again.
#[test]
fn a_clear_takes_back_every_play_of_that_person_before_it() {
    let seen = [
        film("finished", &["first", "second"], 5950, 10),
        film("cleared", &["first"], 0, 20),
        film("again", &["second"], 600, 30),
    ];
    let audience = names(&["first", "second"]);

    let reach = &films(&seen, &audience)[&(FILMS.into(), "film".into())];

    assert_eq!(reach.people, [("second".to_string(), 1.0)]);
}

#[test]
fn a_play_after_a_clear_counts() {
    let seen = [
        film("cleared", &["first"], 0, 10),
        film("after", &["first"], 1500, 20),
    ];

    let reach = &films(&seen, &names(&["first"]))[&(FILMS.into(), "film".into())];

    assert_eq!(reach.shared(&names(&["first"])), Some(0.25));
}

// The poster's faces and the page's status line read one answer, so each
// person's finish comes from the same plays as their share: the plays
// after their latest clear.
#[test]
fn each_person_finishes_by_their_own_plays_after_their_clear() {
    use super::super::finish::Origin;
    let mut watched = film("watched", &["first", "second"], 5950, 10);
    watched.resume.progress.origin = Origin::Screen;
    let mut marked = film("marked", &["second"], 6000, 30);
    marked.resume.progress.origin = Origin::Mark;
    let seen = [
        watched,
        film("cleared", &["second"], 0, 20),
        marked,
        film("third", &["third"], 3000, 10),
    ];
    let audience = names(&["first", "second", "third"]);

    let reach = &films(&seen, &audience)[&(FILMS.into(), "film".into())];

    assert_eq!(reach.finish("first"), Some(Finish::Played { at: 10 }));
    assert_eq!(reach.finish("second"), Some(Finish::Marked));
    assert_eq!(reach.finish("third"), None);
}
