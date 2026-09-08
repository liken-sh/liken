// The offer block on the wire, both ways: out on the play request, and back
// on the Player's commands topic when a person takes the offer. Only this
// browser reads the request block, so its shape is this file's own.

use serde_json::{Map, Value};

use crate::catalog::Selection;
use crate::screens::InFranchise;
use crate::screens::upnext::{Next, Request};

/// The offer as the play request carries it: the library of the next work,
/// the three lines, the art relative to that library's root, and the
/// request block.
pub fn value(next: &Next) -> Value {
    let mut object = Map::new();
    // The library of the next work. The operator reads it to stamp the art
    // onto that library's own claim. A franchise crosses libraries on every
    // series member, so the art of the next work is often not on the claim
    // the items came from.
    object.insert("library".into(), Value::from(next.request.library.as_str()));
    for (name, text) in [
        ("reason", &next.reason),
        ("title", &next.title),
        ("detail", &next.detail),
        ("art", &next.art),
    ] {
        if !text.is_empty() {
            object.insert(name.into(), Value::from(text.as_str()));
        }
    }
    object.insert("request".into(), asked(&next.request));
    Value::Object(object)
}

// The request block: the library, the choice, and the franchise the run
// follows. The franchise is left out where the run follows none.
fn asked(request: &Request) -> Value {
    let mut object = Map::new();
    object.insert("library".into(), Value::from(request.library.as_str()));
    object.insert("selection".into(), chosen(&request.selection));
    if let Some(place) = &request.via {
        object.insert("via".into(), through(place));
    }
    Value::Object(object)
}

fn chosen(selection: &Selection) -> Value {
    let (name, body) = match selection {
        Selection::Movie { id } => ("movie", serde_json::json!({ "id": id })),
        Selection::Trailer { id } => ("trailer", serde_json::json!({ "id": id })),
        Selection::Episode {
            series,
            season,
            episode,
        } => (
            "episode",
            serde_json::json!({"series": series, "season": season, "episode": episode}),
        ),
    };
    let mut object = Map::new();
    object.insert(name.into(), body);
    Value::Object(object)
}

fn through(place: &InFranchise) -> Value {
    serde_json::json!({
        "library": place.library,
        "id": place.id,
        "position": place.position,
    })
}

/// The request block as it comes back on the commands topic, or nothing
/// where the bytes are not ones this browser wrote.
pub fn request(payload: &[u8]) -> Option<Request> {
    let value: Value = serde_json::from_slice(payload).ok()?;
    Some(Request {
        library: value.get("library")?.as_str()?.to_string(),
        selection: read_chosen(value.get("selection")?)?,
        via: value.get("via").and_then(read_through),
    })
}

fn read_chosen(value: &Value) -> Option<Selection> {
    if let Some(body) = value.get("movie") {
        return Some(Selection::Movie {
            id: body.get("id")?.as_str()?.to_string(),
        });
    }
    if let Some(body) = value.get("trailer") {
        return Some(Selection::Trailer {
            id: body.get("id")?.as_str()?.to_string(),
        });
    }
    let body = value.get("episode")?;
    Some(Selection::Episode {
        series: body.get("series")?.as_str()?.to_string(),
        season: body.get("season")?.as_i64()?,
        episode: body.get("episode")?.as_i64()?,
    })
}

fn read_through(value: &Value) -> Option<InFranchise> {
    Some(InFranchise {
        library: value.get("library")?.as_str()?.to_string(),
        id: value.get("id")?.as_str()?.to_string(),
        position: value.get("position")?.as_i64()?,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn offer(selection: Selection, via: Option<InFranchise>) -> Next {
        Next {
            reason: "Next in The Serial · S01".into(),
            title: "E03 · Segment 3".into(),
            detail: "The Serial · S01 · E03 · 46 min".into(),
            art: "s1e3.jpg".into(),
            request: Request {
                library: "screening/serials".into(),
                selection,
                via,
            },
        }
    }

    fn episode() -> Selection {
        Selection::Episode {
            series: "series:1".into(),
            season: 1,
            episode: 3,
        }
    }

    fn place() -> InFranchise {
        InFranchise {
            library: "screening/orders".into(),
            id: "franchise:name:the-run".into(),
            position: 2,
        }
    }

    // The block a request round-trips through, as the sidecar sends it back
    // byte for byte.
    fn returned(next: &Next) -> Option<Request> {
        request(value(next)["request"].to_string().as_bytes())
    }

    #[test]
    fn the_offer_carries_the_three_lines_the_art_and_the_request() {
        assert_eq!(
            value(&offer(episode(), None)),
            serde_json::json!({
                "library": "screening/serials",
                "reason": "Next in The Serial · S01",
                "title": "E03 · Segment 3",
                "detail": "The Serial · S01 · E03 · 46 min",
                "art": "s1e3.jpg",
                "request": {
                    "library": "screening/serials",
                    "selection": {"episode": {
                        "series": "series:1", "season": 1, "episode": 3,
                    }},
                },
            })
        );
    }

    // The operator stamps the art against the library the offer names, so
    // the two must always name one work.
    #[test]
    fn the_offer_names_the_library_the_request_resolves_against() {
        let value = value(&offer(episode(), None));

        assert_eq!(value["library"], "screening/serials");
        assert_eq!(value["library"], value["request"]["library"]);
    }

    #[test]
    fn an_empty_line_is_left_out_of_the_offer() {
        let mut next = offer(episode(), None);
        next.art = String::new();
        next.detail = String::new();
        let value = value(&next);

        assert_eq!(value.get("art"), None);
        assert_eq!(value.get("detail"), None);
        assert!(value.get("reason").is_some());
    }

    #[test]
    fn a_run_along_a_franchise_carries_the_member_it_reached() {
        assert_eq!(
            value(&offer(episode(), Some(place())))["request"]["via"],
            serde_json::json!({
                "library": "screening/orders",
                "id": "franchise:name:the-run",
                "position": 2,
            })
        );
    }

    #[test]
    fn every_choice_comes_back_the_way_it_went_out() {
        let cases = [
            episode(),
            Selection::Movie {
                id: "movies:2".into(),
            },
            Selection::Trailer {
                id: "movies:2".into(),
            },
        ];
        for selection in cases {
            let next = offer(selection.clone(), Some(place()));
            let back = returned(&next).expect("the block decodes");

            assert_eq!(back.library, "screening/serials");
            assert_eq!(back.selection, selection);
            assert_eq!(back.via, Some(place()));
        }
    }

    #[test]
    fn a_run_that_follows_no_franchise_comes_back_with_none() {
        let back = returned(&offer(episode(), None)).expect("the block decodes");

        assert_eq!(back.via, None);
    }

    #[test]
    fn a_block_this_browser_did_not_write_decodes_to_nothing() {
        let cases: [&[u8]; 6] = [
            b"play it",
            b"{}",
            br#"{"library": "screening/serials"}"#,
            br#"{"library": 7, "selection": {"movie": {"id": "movies:2"}}}"#,
            br#"{"library": "screening/serials", "selection": {"movie": {}}}"#,
            br#"{"library": "screening/serials", "selection": {"episode": {"series": "series:1"}}}"#,
        ];
        for payload in cases {
            assert_eq!(request(payload), None);
        }
    }

    #[test]
    fn a_member_the_block_names_badly_comes_back_as_none() {
        let cases: [&[u8]; 2] = [
            br#"{"library": "l", "selection": {"movie": {"id": "m"}}, "via": {"id": "f"}}"#,
            br#"{"library": "l", "selection": {"movie": {"id": "m"}}, "via": 7}"#,
        ];
        for payload in cases {
            assert_eq!(request(payload).expect("the block decodes").via, None);
        }
    }

    #[test]
    fn a_trailer_that_names_no_id_decodes_to_nothing() {
        assert_eq!(
            request(br#"{"library": "l", "selection": {"trailer": {}}}"#),
            None
        );
    }
}
