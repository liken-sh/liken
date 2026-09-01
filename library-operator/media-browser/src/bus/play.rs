// The one request this browser publishes with a body: the play list it
// resolved, on the topic the library operator named. The operator joins
// each path to a claim reference and creates the `Play`, because the
// screen pod holds no API credential.

use serde_json::{Map, Value};

use crate::catalog::{PlayItem, Presentation};

/// The request as bytes. `library` is the catalog's library column,
/// `namespace/name`, and every path is relative to that library's root.
pub fn payload(library: &str, items: &[PlayItem]) -> Vec<u8> {
    let mut request = Map::new();
    request.insert("library".into(), Value::from(library));
    request.insert(
        "items".into(),
        Value::Array(items.iter().map(one).collect()),
    );
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
        serde_json::from_slice(&payload(library, items)).expect("the request is JSON")
    }

    #[test]
    fn a_movie_request_carries_the_library_the_path_and_the_presentation() {
        assert_eq!(
            decoded("default/films", &[movie()]),
            serde_json::json!({
                "library": "default/films",
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
                    presentation: Presentation::default(),
                }]
            )["items"][0]["presentation"],
            serde_json::json!({})
        );
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
