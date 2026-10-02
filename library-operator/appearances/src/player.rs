// The player's file: who is on screen in each span of one video, in the
// shape the liken display draws on pause. match writes it beside the
// detections record, as .liken/appearances/<video>.spans.json, so the
// display reads finished spans and does no matching, merging, or ordering
// of its own.
//
// A span is a run of samples that name the same people. Each sample stands
// for half the gap to the sample before it and half the gap to the sample
// after it, and its stretch stops at the next keyframe: encoders place a
// keyframe at most cuts, so a stretch that crossed one would put the
// people of one shot over the next. The matcher misses faces, so samples
// that name nobody break a scene into pieces, and the people named last
// hold through them, within the limits HOLD and BLANK set. Two spans of the
// same people that meet join into one. A span lists its people left to
// right by where their faces sat across its samples, so the cards on screen
// read in the order the people stand in the picture.
//
// On the bench of nine films, 360 pause moments labeled by hand, these
// rules and the names of match (matches.rs) showed the right people with a
// precision of 0.87, and showed 84% of the clear faces.
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
use crate::record::{Record, SampleLine};

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

// The longest time, in seconds, that the people named last hold through
// samples with faces that name nobody, counted from the end of their last
// sample. A face turned away, in shadow, or too small to name breaks a
// scene into pieces, and a pause in a gap would show no cast. A longer hold
// carries people past the end of their scene. On the bench, a hold of 30
// seconds scored within a moment of this one, and no hold at all showed 4
// points less of the clear faces.
const HOLD: f64 = 10.0;

// The run of consecutive samples with no face at all that ends a span. A
// wide shot of a landscape or a street ends the scene's people, and one
// sample with no face, a cut to a hand or a door for a second, does not.
// On the bench, the rules with a run of 4 seconds here and a hold of 30
// seconds let spans run through inserts and wide shots. These rules raised
// precision from 0.80 to 0.87, and the share of clear faces shown from 0.81
// to 0.84.
const BLANK: usize = 2;

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
    let samples = &record.samples;
    let width = record.header.width.max(1) as f32;
    let index: HashMap<u64, usize> = samples
        .iter()
        .enumerate()
        .map(|(at, sample)| (sample.time.to_bits(), at))
        .collect();
    let mut named: Vec<Vec<(&str, f32)>> = vec![Vec::new(); samples.len()];
    for observation in observations {
        let Some(&at) = index.get(&observation.time.to_bits()) else {
            continue;
        };
        let Some(face) = samples[at].faces.get(observation.face) else {
            continue;
        };
        let centre = (face.face.x + face.face.w / 2.0) / width;
        named[at].push((observation.contributor.as_str(), centre));
    }
    let stretches = stretches(samples, record.header.duration);
    let blank = blank(samples);

    // One span as it grows: its stretch, its sorted set of people, the end
    // of its last sample that named them, and the sum and count of each
    // person's face centre.
    struct Run<'a> {
        start: f64,
        end: f64,
        set: Vec<&'a str>,
        seen: f64,
        places: HashMap<&'a str, (f32, u32)>,
    }
    let mut runs: Vec<Run> = Vec::new();
    let mut open: Option<Run> = None;
    for (at, faces) in named.into_iter().enumerate() {
        let (start, end) = stretches[at];
        let mut set: Vec<&str> = faces.iter().map(|(who, _)| *who).collect();
        set.sort();
        set.dedup();
        if set.is_empty() {
            match open.take() {
                Some(mut run) if !blank[at] && end - run.seen < HOLD => {
                    run.end = end;
                    open = Some(run);
                }
                Some(run) => runs.push(run),
                None => {}
            }
            continue;
        }
        let run = match open.take() {
            Some(mut run) if run.set == set => {
                run.end = end;
                run
            }
            closed => {
                runs.extend(closed);
                Run {
                    start,
                    end,
                    set,
                    seen: end,
                    places: HashMap::new(),
                }
            }
        };
        let run = open.insert(run);
        run.seen = end;
        for (who, centre) in faces {
            let place = run.places.entry(who).or_default();
            place.0 += centre;
            place.1 += 1;
        }
    }
    runs.extend(open);

    let mut cards = BTreeMap::new();
    let mut spans = Vec::new();
    for run in runs {
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

// The stretch each sample stands for: from halfway to the sample before it
// to halfway to the sample after it. A keyframe starts a stretch of its
// own, so a stretch never crosses a keyframe after its sample. The first
// sample starts at its own time, and the last ends at the video's end.
fn stretches(samples: &[SampleLine], duration: f64) -> Vec<(f64, f64)> {
    let bounds: Vec<f64> = samples
        .windows(2)
        .map(|pair| {
            if pair[1].keyframe {
                pair[1].time
            } else {
                (pair[0].time + pair[1].time) / 2.0
            }
        })
        .collect();
    (0..samples.len())
        .map(|at| {
            let start = if at == 0 {
                samples[0].time
            } else {
                bounds[at - 1]
            };
            (start, bounds.get(at).copied().unwrap_or(duration))
        })
        .collect()
}

// Whether each sample is in a run of BLANK or more samples with no face.
fn blank(samples: &[SampleLine]) -> Vec<bool> {
    let mut blank = vec![false; samples.len()];
    let mut at = 0;
    while at < samples.len() {
        let length = samples[at..]
            .iter()
            .take_while(|sample| sample.faces.is_empty())
            .count();
        if length >= BLANK {
            blank[at..at + length].fill(true);
        }
        at += length.max(1);
    }
    blank
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
    use crate::matches::By;
    use crate::record::{self, Detection, Header, Model};
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

    fn sample(time: f64, places: &[f32]) -> SampleLine {
        SampleLine {
            time,
            keyframe: false,
            faces: places.iter().map(|x| face_at(*x)).collect(),
        }
    }

    fn record(samples: Vec<SampleLine>) -> Record {
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
            samples,
        }
    }

    fn seen(time: f64, face: usize, who: &str) -> Observation {
        Observation {
            time,
            face,
            contributor: format!(".contributors/{who}"),
            by: By::Headshot,
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

    // One sample of a case: its time, whether it is a keyframe, and its
    // faces, each named for a person or for nobody.
    type Shot = (f64, bool, &'static [Option<&'static str>]);

    const JANE: &[Option<&str>] = &[Some("ja/jane")];
    const JOHN: &[Option<&str>] = &[Some("jo/john")];
    const NOBODY: &[Option<&str>] = &[None];
    const NO_FACE: &[Option<&str>] = &[];

    fn spans_of(shots: &[Shot], duration: f64) -> Vec<Span> {
        let samples = shots
            .iter()
            .map(|(time, keyframe, faces)| SampleLine {
                time: *time,
                keyframe: *keyframe,
                faces: faces.iter().map(|_| face_at(50.0)).collect(),
            })
            .collect();
        let mut record = record(samples);
        record.header.duration = duration;
        let observations: Vec<Observation> = shots
            .iter()
            .flat_map(|(time, _, faces)| {
                faces
                    .iter()
                    .enumerate()
                    .filter_map(|(face, who)| who.map(|who| seen(*time, face, who)))
            })
            .collect();
        spans(&record, &observations, &people(), &HashMap::new(), None).spans
    }

    // Jane named at 0 s and faces nobody named every second after it.
    fn jane_then_nobody(until: usize) -> Vec<Shot> {
        std::iter::once((0.0, true, JANE))
            .chain((1..until).map(|time| (time as f64, false, NOBODY)))
            .collect()
    }

    #[test]
    fn the_span_rules() {
        let cases: Vec<(&str, Vec<Shot>, f64, Vec<Span>)> = vec![
            (
                "each sample stands for half the gap on each side",
                vec![(0.0, true, JANE), (1.0, false, JANE), (2.0, false, JOHN)],
                3.0,
                vec![span(0.0, 1.5, &["ja/jane"]), span(1.5, 3.0, &["jo/john"])],
            ),
            (
                "a stretch stops at the next keyframe",
                vec![
                    (0.0, true, JANE),
                    (1.0, false, JANE),
                    (1.4, true, JOHN),
                    (2.4, false, JOHN),
                ],
                3.0,
                vec![span(0.0, 1.4, &["ja/jane"]), span(1.4, 3.0, &["jo/john"])],
            ),
            (
                "two samples with no face end the span",
                vec![
                    (0.0, true, JANE),
                    (1.0, false, NO_FACE),
                    (2.0, false, NO_FACE),
                    (3.0, false, JANE),
                ],
                4.0,
                vec![span(0.0, 0.5, &["ja/jane"]), span(2.5, 4.0, &["ja/jane"])],
            ),
            (
                "one sample with no face keeps the span",
                vec![(0.0, true, JANE), (1.0, false, NO_FACE), (2.0, false, JANE)],
                3.0,
                vec![span(0.0, 3.0, &["ja/jane"])],
            ),
            (
                "faces nobody named hold the people named last until others are named",
                vec![(0.0, true, JANE), (1.0, false, NOBODY), (2.0, false, JOHN)],
                3.0,
                vec![span(0.0, 1.5, &["ja/jane"]), span(1.5, 3.0, &["jo/john"])],
            ),
            (
                "the same people after a hold join into one span",
                vec![
                    (0.0, true, JANE),
                    (1.0, false, NOBODY),
                    (2.0, false, NOBODY),
                    (3.0, false, JANE),
                ],
                4.0,
                vec![span(0.0, 4.0, &["ja/jane"])],
            ),
            // Jane's last sample ends at 0.5 s, and the hold takes each
            // stretch that ends before 10.5 s.
            (
                "the hold ends 10 seconds after the last naming",
                jane_then_nobody(15),
                15.0,
                vec![span(0.0, 9.5, &["ja/jane"])],
            ),
            (
                "the hold runs to the end of the video",
                jane_then_nobody(2),
                2.0,
                vec![span(0.0, 2.0, &["ja/jane"])],
            ),
            (
                "faces before the first naming are no span",
                vec![(0.0, true, NOBODY), (1.0, false, JANE)],
                2.0,
                vec![span(0.5, 2.0, &["ja/jane"])],
            ),
        ];
        for (case, shots, duration, expected) in cases {
            assert_eq!(spans_of(&shots, duration), expected, "{case}");
        }
    }

    #[test]
    fn a_span_lists_its_people_left_to_right_by_their_average_place() {
        // John stands left of Jane in the first sample and right of her
        // in the second, and his average place is the further left.
        let record = record(vec![sample(0.0, &[10.0, 50.0]), sample(5.0, &[60.0, 55.0])]);
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
        let record = record(vec![sample(0.0, &[50.0, 20.0])]);
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
