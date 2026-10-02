// The player's file: who is on screen in each span of one video, in the
// shape the liken display draws on pause. match writes it beside the
// detections record, as .liken/appearances/<video>.spans.json, so the
// display reads finished spans and does no matching, merging, or ordering
// of its own.
//
// A span is a run of keyframes that name the same people. Each keyframe
// stands for its shot, from its own time to the next keyframe's, the rule
// spans.rs holds. A span lists its people left to right by where their
// faces sat across its keyframes, so the cards on screen read in the order
// the people stand in the picture. A short run of keyframes that names
// nobody belongs to the span before it, as HOLD and BLANK say. A longer run
// that names nobody is no span: the display shows nothing there.
//
// Each person carries what a card shows: the name, the part, the portrait,
// and the dates the display turns into ages. The display subtracts the
// birth date from the release date and from its own clock, so the file
// holds dates and not ages, and an age that grows each year is never stale
// in it.

use std::collections::{BTreeMap, HashMap};
use std::fs;
use std::path::{Path, PathBuf};

use serde::{Deserialize, Serialize};

use crate::gallery::Person;
use crate::matches::Observation;
use crate::record::Record;

pub const FORMAT: &str = "liken.sh/appearances/spans/v1";

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Spans {
    pub format: String,
    // The video's release date, YYYY-MM-DD, from its .nfo. Absent when the
    // .nfo states none, and the display then leaves out the age in the film.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub released: Option<String>,
    // Every person a span names, by their entry's path under .contributors/.
    pub people: BTreeMap<String, Card>,
    pub spans: Vec<Span>,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Card {
    pub name: String,
    // The part the person plays, absent when the credits name none.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub character: Option<String>,
    // The headshot's path under .contributors/.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub portrait: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub born: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub died: Option<String>,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Span {
    pub start: f64,
    pub end: f64,
    // The people's keys in Spans::people, left to right on screen.
    pub people: Vec<String>,
}

// What a person's .contributors/ entry adds to a card: the dates from its
// contributor.yaml and the headshot's file name. The match reads the entry,
// and the tests hand it in.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Entry {
    pub born: Option<String>,
    pub died: Option<String>,
    pub portrait: Option<String>,
}

const CONTRIBUTORS: &str = ".contributors/";

// The longest time after the last person named that the people seen last
// hold, in seconds. The matcher misses a face turned away, in shadow, or
// too small to name, so a scene breaks into pieces with gaps between them,
// and a pause in a gap shows no cast. A longer hold carries people past the
// end of their scene.
const HOLD: f64 = 30.0;

// The shortest stretch with no face at all that ends the people's span, in
// seconds. A wide shot of a landscape or a street ends the scene's people,
// and a cut of a moment to a hand or a door does not.
const BLANK: f64 = 4.0;

pub fn path(title: &Path, video: &str) -> PathBuf {
    title
        .join(".liken")
        .join("appearances")
        .join(format!("{video}.spans.json"))
}

// The spans of one video. observations are the faces the match named in
// record, and people is the gallery that named them, with entries holding
// each person's dates and headshot.
pub fn spans(
    record: &Record,
    observations: &[Observation],
    people: &[Person],
    entries: &HashMap<String, Entry>,
    released: Option<String>,
) -> Spans {
    let width = record.header.width.max(1) as f32;
    let mut named: HashMap<u64, Vec<(&str, f32)>> = HashMap::new();
    for observation in observations {
        let Some(face) = record
            .keyframes
            .iter()
            .find(|k| k.time == observation.time)
            .and_then(|k| k.faces.get(observation.face))
        else {
            continue;
        };
        let centre = (face.face.x + face.face.w / 2.0) / width;
        named
            .entry(observation.time.to_bits())
            .or_default()
            .push((observation.contributor.as_str(), centre));
    }

    // One run: its start, its end, its sorted set of people, whether its
    // keyframes hold no face at all, the end of its last keyframe that named
    // someone, and the sum and count of each person's face centre.
    struct Run<'a> {
        start: f64,
        end: f64,
        set: Vec<&'a str>,
        blank: bool,
        seen: f64,
        places: HashMap<&'a str, (f32, u32)>,
    }
    let mut runs: Vec<Run> = Vec::new();
    for (index, keyframe) in record.keyframes.iter().enumerate() {
        let end = record
            .keyframes
            .get(index + 1)
            .map_or(record.header.duration, |next| next.time);
        let faces = named
            .get(&keyframe.time.to_bits())
            .cloned()
            .unwrap_or_default();
        let mut set: Vec<&str> = faces.iter().map(|(who, _)| *who).collect();
        set.sort();
        set.dedup();
        let blank = keyframe.faces.is_empty();
        let run = match runs.last_mut() {
            Some(last) if last.set == set && last.blank == blank => {
                last.end = end;
                last.seen = end;
                last
            }
            _ => {
                runs.push(Run {
                    start: keyframe.time,
                    end,
                    set,
                    blank,
                    seen: end,
                    places: HashMap::new(),
                });
                runs.last_mut().unwrap()
            }
        };
        for (who, centre) in faces {
            let place = run.places.entry(who).or_default();
            place.0 += centre;
            place.1 += 1;
        }
    }

    // The held runs: a run that names nobody between two named runs goes to
    // the run before it while it ends within HOLD of the last person named,
    // unless it holds no face for BLANK or longer. Two runs of the same
    // people that meet merge, with the places of both.
    let mut held: Vec<Run> = Vec::new();
    let mut runs = runs.into_iter().peekable();
    while let Some(run) = runs.next() {
        if run.set.is_empty()
            && !(run.blank && run.end - run.start >= BLANK)
            && runs.peek().is_some()
            && let Some(last) = held
                .last_mut()
                .filter(|last| !last.set.is_empty() && run.end - last.seen < HOLD)
        {
            last.end = run.end;
            continue;
        }
        match held.last_mut() {
            Some(last) if last.set == run.set && last.blank == run.blank => {
                last.end = run.end;
                last.seen = run.seen;
                for (who, (sum, count)) in run.places {
                    let place = last.places.entry(who).or_default();
                    place.0 += sum;
                    place.1 += count;
                }
            }
            _ => held.push(run),
        }
    }

    let mut cards = BTreeMap::new();
    let mut spans = Vec::new();
    for run in held.into_iter().filter(|r| !r.set.is_empty()) {
        let mut order: Vec<(&str, f32)> = run
            .set
            .iter()
            .map(|who| {
                let (sum, count) = run.places[who];
                (*who, sum / count as f32)
            })
            .collect();
        order.sort_by(|a, b| a.1.total_cmp(&b.1).then(a.0.cmp(b.0)));
        let mut keys = Vec::new();
        for (who, _) in order {
            let key = who.strip_prefix(CONTRIBUTORS).unwrap_or(who).to_string();
            if !cards.contains_key(&key) {
                cards.insert(key.clone(), card(who, people, entries));
            }
            keys.push(key);
        }
        spans.push(Span {
            start: run.start,
            end: run.end,
            people: keys,
        });
    }
    Spans {
        format: FORMAT.into(),
        released,
        people: cards,
        spans,
    }
}

fn card(contributor: &str, people: &[Person], entries: &HashMap<String, Entry>) -> Card {
    let person = people.iter().find(|p| p.contributor == contributor);
    let entry = entries.get(contributor).cloned().unwrap_or_default();
    let key = contributor
        .strip_prefix(CONTRIBUTORS)
        .unwrap_or(contributor);
    Card {
        name: person.map(|p| p.name.clone()).unwrap_or_default(),
        character: person
            .map(|p| p.role.trim().to_string())
            .filter(|role| !role.is_empty()),
        portrait: entry.portrait.map(|file| format!("{key}/{file}")),
        born: entry.born,
        died: entry.died,
    }
}

#[derive(Deserialize)]
struct Contributor {
    #[serde(default)]
    born: Option<String>,
    #[serde(default)]
    died: Option<String>,
}

// The dates and the headshot of one entry, read from the library. An entry
// with no contributor.yaml, or one that cannot be read, gives no dates, and
// its card shows no ages.
pub fn entry(root: &Path, contributor: &str) -> Entry {
    let folder = root.join(contributor);
    let dates: Option<Contributor> = fs::read_to_string(folder.join("contributor.yaml"))
        .ok()
        .and_then(|text| serde_yaml_ng::from_str(&text).ok());
    let portrait = ["headshot.jpg", "headshot.png"]
        .into_iter()
        .find(|name| folder.join(name).is_file())
        .map(String::from);
    Entry {
        born: dates.as_ref().and_then(|d| d.born.clone()),
        died: dates.and_then(|d| d.died),
        portrait,
    }
}

// The release date of a video: <premiered> from the .nfo named for the
// video, which an episode has, or else from the folder's movie.nfo. The
// date is the first ten characters, YYYY-MM-DD.
pub fn released(title: &Path, video: &str) -> Option<String> {
    let stem = Path::new(video).file_stem()?.to_string_lossy().into_owned();
    [format!("{stem}.nfo"), "movie.nfo".to_string()]
        .iter()
        .filter_map(|name| fs::read_to_string(title.join(name)).ok())
        .find_map(|text| premiered(&text))
}

fn premiered(nfo: &str) -> Option<String> {
    let start = nfo.find("<premiered>")? + "<premiered>".len();
    let end = start + nfo[start..].find("</premiered>")?;
    let date = nfo[start..end].trim();
    let valid = date.len() >= 10 && date.as_bytes()[4] == b'-' && date.as_bytes()[7] == b'-';
    valid.then(|| date[..10].to_string())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::gallery::Headshot;
    use crate::record::{self, Detection, Header, KeyframeLine, Model};
    use crate::yunet::Face;

    fn model() -> Model {
        Model {
            name: "m".into(),
            sha256: "00".into(),
        }
    }

    // A face whose centre sits at x on a frame 100 wide.
    fn face_at(x: f32) -> Detection {
        Detection {
            face: Face {
                x: x - 5.0,
                y: 0.0,
                w: 10.0,
                h: 10.0,
                score: 0.95,
                landmarks: [(0.0, 0.0); 5],
            },
            embedding: String::new(),
        }
    }

    fn keyframe(time: f64, places: &[f32]) -> KeyframeLine {
        KeyframeLine {
            time,
            faces: places.iter().map(|x| face_at(*x)).collect(),
        }
    }

    fn record(keyframes: Vec<KeyframeLine>) -> Record {
        Record {
            header: Header {
                format: record::FORMAT.into(),
                video: "film.mkv".into(),
                size: 1,
                duration: 40.0,
                width: 100,
                height: 100,
                detector: model(),
                detect_width: 100,
                embedder: model(),
                device: "CPU".into(),
            },
            keyframes,
        }
    }

    fn seen(time: f64, face: usize, who: &str) -> Observation {
        Observation {
            time,
            face,
            contributor: format!(".contributors/{who}"),
            similarity: 0.5,
            runner_up: None,
        }
    }

    fn person(who: &str, name: &str, role: &str) -> Person {
        Person {
            contributor: format!(".contributors/{who}"),
            name: name.into(),
            role: role.into(),
            headshot: Headshot::Found,
            headshot_sha256: None,
            embedding: None,
        }
    }

    fn people() -> Vec<Person> {
        vec![
            person("ja/jane", "Jane Roe", "The Pilot"),
            person("jo/john", "John Doe", ""),
        ]
    }

    fn span(start: f64, end: f64, people: &[&str]) -> Span {
        Span {
            start,
            end,
            people: people.iter().map(|who| who.to_string()).collect(),
        }
    }

    #[test]
    fn keyframes_that_name_the_same_people_merge_and_nobody_is_no_span() {
        let mut record = record(vec![
            keyframe(0.0, &[]),
            keyframe(10.0, &[30.0]),
            keyframe(15.0, &[40.0]),
            keyframe(20.0, &[]),
            keyframe(60.0, &[70.0]),
        ]);
        record.header.duration = 70.0;
        let observations = [
            seen(10.0, 0, "ja/jane"),
            seen(15.0, 0, "ja/jane"),
            seen(60.0, 0, "jo/john"),
        ];
        let spans = spans(&record, &observations, &people(), &HashMap::new(), None);
        assert_eq!(
            spans.spans,
            vec![
                span(10.0, 20.0, &["ja/jane"]),
                span(60.0, 70.0, &["jo/john"])
            ]
        );
    }

    // A shot of the same people from behind, or too far off to name, breaks
    // a scene into pieces. A short stretch of faces nobody named between two
    // spans of the same people is part of their span.
    #[test]
    fn a_short_gap_between_the_same_people_joins_their_spans() {
        let record = record(vec![
            keyframe(0.0, &[30.0]),
            keyframe(10.0, &[60.0]),
            keyframe(30.0, &[40.0]),
        ]);
        let observations = [seen(0.0, 0, "ja/jane"), seen(30.0, 0, "ja/jane")];
        let spans = spans(&record, &observations, &people(), &HashMap::new(), None);
        assert_eq!(spans.spans, vec![span(0.0, 40.0, &["ja/jane"])]);
    }

    // Between two spans of different people, the people seen last hold
    // a short stretch of faces nobody named, until the next person is named.
    #[test]
    fn a_short_gap_before_other_people_holds_the_people_seen_last() {
        let record = record(vec![
            keyframe(0.0, &[30.0]),
            keyframe(10.0, &[60.0]),
            keyframe(30.0, &[40.0]),
        ]);
        let observations = [seen(0.0, 0, "ja/jane"), seen(30.0, 0, "jo/john")];
        let spans = spans(&record, &observations, &people(), &HashMap::new(), None);
        assert_eq!(
            spans.spans,
            vec![
                span(0.0, 30.0, &["ja/jane"]),
                span(30.0, 40.0, &["jo/john"])
            ]
        );
    }

    // A wide shot with no face in it ends the scene's people, so a stretch
    // with no face at all, a few seconds long, closes the span.
    #[test]
    fn a_stretch_with_no_face_at_all_closes_the_span() {
        let record = record(vec![
            keyframe(0.0, &[30.0]),
            keyframe(10.0, &[]),
            keyframe(20.0, &[40.0]),
        ]);
        let observations = [seen(0.0, 0, "ja/jane"), seen(20.0, 0, "ja/jane")];
        let spans = spans(&record, &observations, &people(), &HashMap::new(), None);
        assert_eq!(
            spans.spans,
            vec![
                span(0.0, 10.0, &["ja/jane"]),
                span(20.0, 40.0, &["ja/jane"])
            ]
        );
    }

    // A cut away from every face shorter than that, an insert of a hand or
    // a door, keeps the span.
    #[test]
    fn a_moment_with_no_face_keeps_the_span() {
        let record = record(vec![
            keyframe(0.0, &[30.0]),
            keyframe(10.0, &[]),
            keyframe(12.0, &[40.0]),
        ]);
        let observations = [seen(0.0, 0, "ja/jane"), seen(12.0, 0, "ja/jane")];
        let spans = spans(&record, &observations, &people(), &HashMap::new(), None);
        assert_eq!(spans.spans, vec![span(0.0, 40.0, &["ja/jane"])]);
    }

    // The hold counts from the last time someone was named, so a run of
    // short gaps adds up to no more than the one hold.
    #[test]
    fn the_hold_counts_from_the_last_person_named() {
        let mut record = record(vec![
            keyframe(0.0, &[30.0]),
            keyframe(10.0, &[60.0]),
            keyframe(30.0, &[]),
            keyframe(32.0, &[60.0]),
            keyframe(50.0, &[40.0]),
        ]);
        record.header.duration = 60.0;
        let observations = [seen(0.0, 0, "ja/jane"), seen(50.0, 0, "jo/john")];
        let spans = spans(&record, &observations, &people(), &HashMap::new(), None);
        assert_eq!(
            spans.spans,
            vec![
                span(0.0, 32.0, &["ja/jane"]),
                span(50.0, 60.0, &["jo/john"])
            ]
        );
    }

    // After the last person named, nobody holds the rest of the video.
    #[test]
    fn a_gap_at_the_end_of_the_video_names_nobody() {
        let record = record(vec![keyframe(0.0, &[30.0]), keyframe(10.0, &[])]);
        let observations = [seen(0.0, 0, "ja/jane")];
        let spans = spans(&record, &observations, &people(), &HashMap::new(), None);
        assert_eq!(spans.spans, vec![span(0.0, 10.0, &["ja/jane"])]);
    }

    #[test]
    fn a_span_lists_its_people_left_to_right_by_their_average_place() {
        // John stands left of Jane in the first keyframe and right of her
        // in the second, and his average place is the further left.
        let record = record(vec![
            keyframe(0.0, &[10.0, 50.0]),
            keyframe(5.0, &[60.0, 55.0]),
        ]);
        let observations = [
            seen(0.0, 0, "jo/john"),
            seen(0.0, 1, "ja/jane"),
            seen(5.0, 0, "jo/john"),
            seen(5.0, 1, "ja/jane"),
        ];
        let spans = spans(&record, &observations, &people(), &HashMap::new(), None);
        assert_eq!(spans.spans[0].people, vec!["jo/john", "ja/jane"]);
    }

    #[test]
    fn a_card_carries_the_name_the_part_the_portrait_and_the_dates() {
        let record = record(vec![keyframe(0.0, &[50.0, 20.0])]);
        let observations = [seen(0.0, 0, "ja/jane"), seen(0.0, 1, "jo/john")];
        let entries = HashMap::from([(
            ".contributors/ja/jane".to_string(),
            Entry {
                born: Some("1960-05-01".into()),
                died: Some("2020-01-02".into()),
                portrait: Some("headshot.jpg".into()),
            },
        )]);
        let spans = spans(
            &record,
            &observations,
            &people(),
            &entries,
            Some("2009-10-10".into()),
        );
        assert_eq!(spans.released.as_deref(), Some("2009-10-10"));
        assert_eq!(
            spans.people["ja/jane"],
            Card {
                name: "Jane Roe".into(),
                character: Some("The Pilot".into()),
                portrait: Some("ja/jane/headshot.jpg".into()),
                born: Some("1960-05-01".into()),
                died: Some("2020-01-02".into()),
            }
        );
        assert_eq!(
            spans.people["jo/john"],
            Card {
                name: "John Doe".into(),
                character: None,
                portrait: None,
                born: None,
                died: None,
            }
        );
    }

    #[test]
    fn the_release_date_is_the_premiere_of_the_nfo() {
        let nfo = "<movie>\n  <title>X</title>\n  <premiered>2009-10-10</premiered>\n</movie>";
        assert_eq!(premiered(nfo).as_deref(), Some("2009-10-10"));
        assert_eq!(premiered("<movie><year>2009</year></movie>"), None);
        assert_eq!(
            premiered("<movie><premiered>2009</premiered></movie>"),
            None
        );
    }
}
