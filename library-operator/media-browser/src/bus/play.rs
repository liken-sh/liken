// The one request this browser publishes with a body: the play list it
// resolved, on the topic the library operator named. The operator joins
// each path to a claim reference and creates the `Play`, because the
// screen pod holds no API credential.

use serde_json::{Map, Value};

use super::next;
use crate::catalog::{Identity, PlayItem, Presentation};
use crate::screens::upnext::Next;

/// The request as bytes. `library` is the catalog's library column,
/// `namespace/name`, and every path is relative to that library's root.
///
/// The slug is the chosen item's, which is the first of the list: the
/// movie, or the episode a person picked, with the rest of its season
/// after it. The operator folds it into the `Play`'s name. A list that
/// resolved nothing carries an empty slug, and the operator then names
/// the `Play` after the unit alone.
///
/// `people` names who is watching, by `Person` name, and `identity` names
/// the work the progress store keys on. Each is left out where it holds
/// nothing, so a play with no audience and no alias is recorded against
/// the `Player` alone.
///
/// `start` is the second the first item starts at, which a resume carries.
/// It goes out as a decimal string, one of the time forms the player
/// accepts, and the key is left out of a request that starts at the
/// beginning.
///
/// `next` is the work that follows this one. The key is left out of a
/// request whose container ends here.
pub fn payload(
    library: &str,
    items: &[PlayItem],
    people: &[String],
    identity: &Identity,
    start: Option<i64>,
    next: Option<&Next>,
) -> Vec<u8> {
    let mut request = Map::new();
    request.insert("library".into(), Value::from(library));
    request.insert(
        "slug".into(),
        Value::from(items.first().map(|item| item.slug.as_str()).unwrap_or("")),
    );
    request.insert(
        "items".into(),
        Value::Array(items.iter().map(one).collect()),
    );
    if !people.is_empty() {
        request.insert(
            "people".into(),
            Value::Array(
                people
                    .iter()
                    .map(|name| Value::from(name.as_str()))
                    .collect(),
            ),
        );
    }
    if !identity.aliases.is_empty() {
        request.insert(
            "aliases".into(),
            Value::Object(
                identity
                    .aliases
                    .iter()
                    .map(|(provider, id)| (provider.clone(), Value::from(id.as_str())))
                    .collect(),
            ),
        );
    }
    for (name, number) in [("season", identity.season), ("episode", identity.episode)] {
        if number != 0 {
            request.insert(name.into(), Value::from(number));
        }
    }
    if let Some(seconds) = start {
        request.insert("start".into(), Value::from(seconds.to_string()));
    }
    if let Some(next) = next {
        request.insert("next".into(), next::value(next));
    }
    Value::Object(request).to_string().into_bytes()
}

// One item: the path of its main file, and the presentation beside it.
fn one(item: &PlayItem) -> Value {
    let mut object = Map::new();
    object.insert("path".into(), Value::from(item.path.as_str()));
    object.insert("presentation".into(), presentation(&item.presentation));
    Value::Object(object)
}

// The presentation, in media-operator's own field names. An empty field
// is left out rather than sent empty, so the object carries what the
// catalog holds and nothing more.
fn presentation(presentation: &Presentation) -> Value {
    let mut object = Map::new();
    for (name, text) in [
        ("type", &presentation.kind),
        ("hint", &presentation.hint),
        ("title", &presentation.title),
        ("series", &presentation.series),
        ("episodeTitle", &presentation.episode_title),
        ("date", &presentation.date),
        ("art", &presentation.art),
        ("trickplay", &presentation.trickplay),
    ] {
        if !text.is_empty() {
            object.insert(name.into(), Value::from(text.as_str()));
        }
    }
    for (name, number) in [
        ("season", presentation.season),
        ("episode", presentation.episode),
        ("year", presentation.year),
    ] {
        if number != 0 {
            object.insert(name.into(), Value::from(number));
        }
    }
    Value::Object(object)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn movie() -> PlayItem {
        PlayItem {
            path: "Some Film (1999)/Some Film (1999).mkv".into(),
            slug: "some-film-1999".into(),
            presentation: Presentation {
                kind: "video".into(),
                hint: "movie".into(),
                title: "Some Film".into(),
                year: 1999,
                art: "Some Film (1999)/poster.jpg".into(),
                trickplay: "Some Film (1999)/Some Film (1999).trickplay".into(),
                ..Presentation::default()
            },
        }
    }

    fn decoded(library: &str, items: &[PlayItem]) -> Value {
        serde_json::from_slice(&payload(
            library,
            items,
            &[],
            &Identity::default(),
            None,
            None,
        ))
        .expect("the request is JSON")
    }

    #[test]
    fn a_movie_request_carries_the_library_the_path_and_the_presentation() {
        assert_eq!(
            decoded("default/films", &[movie()]),
            serde_json::json!({
                "library": "default/films",
                "slug": "some-film-1999",
                "items": [{
                    "path": "Some Film (1999)/Some Film (1999).mkv",
                    "presentation": {
                        "type": "video",
                        "hint": "movie",
                        "title": "Some Film",
                        "year": 1999,
                        "art": "Some Film (1999)/poster.jpg",
                        "trickplay": "Some Film (1999)/Some Film (1999).trickplay",
                    },
                }],
            })
        );
    }

    #[test]
    fn an_episode_request_carries_the_series_the_numbers_and_the_date() {
        let item = PlayItem {
            path: "Show/S01/Show S01E02.mkv".into(),
            slug: "show-s01e02".into(),
            presentation: Presentation {
                kind: "video".into(),
                hint: "series".into(),
                series: "Show".into(),
                season: 1,
                episode: 2,
                episode_title: "The Second".into(),
                date: "2004-09-22".into(),
                ..Presentation::default()
            },
        };

        assert_eq!(
            decoded("default/shows", &[item])["items"][0]["presentation"],
            serde_json::json!({
                "type": "video",
                "hint": "series",
                "series": "Show",
                "season": 1,
                "episode": 2,
                "episodeTitle": "The Second",
                "date": "2004-09-22",
            })
        );
    }

    #[test]
    fn an_empty_field_is_left_out_of_the_request() {
        assert_eq!(
            decoded(
                "default/films",
                &[PlayItem {
                    path: "film.mkv".into(),
                    slug: String::new(),
                    presentation: Presentation::default(),
                }]
            )["items"][0]["presentation"],
            serde_json::json!({})
        );
    }

    // The request as the browser publishes it for an audience and a work it
    // named.
    fn recorded(people: &[&str], identity: &Identity) -> Value {
        let people: Vec<String> = people.iter().map(|name| (*name).to_string()).collect();
        serde_json::from_slice(&payload(
            "default/films",
            &[movie()],
            &people,
            identity,
            None,
            None,
        ))
        .expect("the request is JSON")
    }

    fn named(aliases: &[(&str, &str)], numbers: (i64, i64)) -> Identity {
        Identity {
            aliases: aliases
                .iter()
                .map(|(provider, id)| ((*provider).to_string(), (*id).to_string()))
                .collect(),
            season: numbers.0,
            episode: numbers.1,
        }
    }

    #[test]
    fn a_request_names_the_people_watching() {
        assert_eq!(
            recorded(&["first", "second"], &Identity::default())["people"],
            serde_json::json!(["first", "second"])
        );
    }

    #[test]
    fn a_request_names_the_work_at_every_provider() {
        assert_eq!(
            recorded(
                &[],
                &named(&[("tmdb", "603"), ("path", "some-film-1999")], (0, 0))
            )["aliases"],
            serde_json::json!({"tmdb": "603", "path": "some-film-1999"})
        );
    }

    #[test]
    fn an_episode_request_names_the_two_aired_numbers() {
        let request = recorded(&[], &named(&[("tvdb", "73739")], (2, 5)));

        assert_eq!(request["season"], 2);
        assert_eq!(request["episode"], 5);
    }

    #[test]
    fn a_request_with_no_audience_and_no_work_carries_neither() {
        let request = recorded(&[], &Identity::default());

        assert_eq!(request.get("people"), None);
        assert_eq!(request.get("aliases"), None);
        assert_eq!(request.get("season"), None);
        assert_eq!(request.get("episode"), None);
    }

    // The request as the browser publishes it for a play that starts where
    // the audience left the work.
    fn resumed(start: Option<i64>) -> Value {
        serde_json::from_slice(&payload(
            "default/films",
            &[movie()],
            &[],
            &Identity::default(),
            start,
            None,
        ))
        .expect("the request is JSON")
    }

    #[test]
    fn a_resume_names_the_second_the_film_starts_at() {
        assert_eq!(resumed(Some(1_337))["start"], "1337");
    }

    #[test]
    fn a_play_from_the_beginning_names_no_second() {
        assert_eq!(resumed(None).get("start"), None);
    }

    #[test]
    fn a_request_keeps_the_order_the_catalog_answered() {
        let mut second = movie();
        second.path = "Later.mkv".into();
        let request = decoded("default/films", &[movie(), second]);

        assert_eq!(
            request["items"][0]["path"],
            "Some Film (1999)/Some Film (1999).mkv"
        );
        assert_eq!(request["items"][1]["path"], "Later.mkv");
    }
}
