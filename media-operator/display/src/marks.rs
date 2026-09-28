//! The marks: the spans in the file where the intro, the recap, the credits,
//! the scene after the credits, and the preview are. The library reads them from community databases, and
//! a database can return several candidate spans for one kind, from different
//! submissions or release versions of the file. The library forwards every
//! candidate, and this module merges them into the spans the skip control and
//! the up-next card act on.

use serde_json::Value;

/// What one span holds. A kind the block names that is not one of these is
/// ignored, so a library can send a new kind before the display acts on it.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Kind {
    Intro,
    Recap,
    Credits,
    /// The scene after the credits. IntroDB states it as a span of its own.
    /// TheIntroDB states no such kind, and gives two credits spans with the
    /// scene in the gap between them.
    PostCredits,
    Preview,
}

impl Kind {
    fn from_word(word: &str) -> Option<Self> {
        match word {
            "intro" => Some(Kind::Intro),
            "recap" => Some(Kind::Recap),
            "credits" => Some(Kind::Credits),
            "post-credits" => Some(Kind::PostCredits),
            "preview" => Some(Kind::Preview),
            _ => None,
        }
    }
}

/// One candidate as the block states it. An absent start is the start of the
/// file, and an absent end is the end of the file, which is known only once
/// mpv reports the duration.
#[derive(Debug, Clone, Copy, PartialEq)]
struct Candidate {
    kind: Kind,
    start: Option<f64>,
    end: Option<f64>,
}

/// One merged span, in seconds from the start of the file.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Span {
    pub kind: Kind,
    pub start: f64,
    pub end: f64,
}

impl Span {
    /// The end is outside the span, so a seek to the end leaves it.
    pub fn contains(&self, at: f64) -> bool {
        self.start <= at && at < self.end
    }
}

/// What the skip control offers: the span the playhead is inside, and the
/// second in the file a select seeks to.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Jump {
    pub span: Span,
    pub to: f64,
}

/// Every candidate the item's block carries, in the order it states them.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Marks {
    candidates: Vec<Candidate>,
}

impl Marks {
    /// Read the block's `marks` array. An entry with a kind this module does
    /// not know, or with a start or an end that is not a number of seconds, is
    /// dropped, and the other entries still count.
    pub fn parse(value: Option<&Value>) -> Self {
        let candidates = value
            .and_then(Value::as_array)
            .into_iter()
            .flatten()
            .filter_map(candidate)
            .collect();
        Self { candidates }
    }

    /// The merged spans of one kind, in order. The candidates that overlap
    /// form one group, and the group becomes one span from the median start
    /// and the median end of its members, so one submission that is off by a
    /// few seconds moves the span less than an average would. Groups that do
    /// not overlap stay apart, because a film can carry two credits spans: the
    /// main credits, then a scene after them and more credits.
    ///
    /// A candidate with no end waits for the duration, and a candidate that
    /// ends where it starts, or before, marks nothing.
    pub fn spans(&self, kind: Kind, duration: Option<f64>) -> Vec<Span> {
        let mut ranges: Vec<(f64, f64)> = self
            .candidates
            .iter()
            .filter(|candidate| candidate.kind == kind)
            .filter_map(|candidate| {
                let start = candidate.start.unwrap_or(0.0);
                let end = candidate.end.or(duration)?;
                (end > start).then_some((start, end))
            })
            .collect();
        ranges.sort_by(|a, b| a.0.total_cmp(&b.0));

        let mut groups: Vec<Vec<(f64, f64)>> = Vec::new();
        let mut reach = f64::NEG_INFINITY;
        for range in ranges {
            match groups.last_mut() {
                Some(group) if range.0 < reach => group.push(range),
                _ => groups.push(vec![range]),
            }
            reach = reach.max(range.1);
        }
        groups
            .into_iter()
            .map(|group| Span {
                kind,
                start: median(group.iter().map(|range| range.0).collect()),
                end: median(group.iter().map(|range| range.1).collect()),
            })
            .filter(|span| span.end > span.start)
            .collect()
    }

    /// What the skip control offers at the playhead. Inside an intro or a
    /// recap it offers the end of that span. Inside the end credits it offers
    /// the scene after them, when the marks place one ahead of the playhead.
    pub fn skippable(&self, at: Option<f64>, duration: Option<f64>) -> Option<Jump> {
        let at = at?;
        let opening = [Kind::Intro, Kind::Recap]
            .into_iter()
            .flat_map(|kind| self.spans(kind, duration))
            .find(|span| span.contains(at))
            .map(|span| Jump { span, to: span.end });
        opening.or_else(|| self.to_the_scene(at, duration))
    }

    /// The jump from the end credits to the scene after them. The target is
    /// the start of the first `post-credits` span ahead of the playhead. With
    /// no such span, two or more credits spans in the second half place the
    /// scene in the gap after the first one, which is how TheIntroDB states
    /// it, so the target is the end of the first. A film with credits and no
    /// scene offers no jump, because the only thing past its credits is the
    /// end of the file, and the up-next card covers that.
    fn to_the_scene(&self, at: f64, duration: Option<f64>) -> Option<Jump> {
        let duration = duration.filter(|duration| *duration > 0.0)?;
        let credits: Vec<Span> = self
            .spans(Kind::Credits, Some(duration))
            .into_iter()
            .filter(|span| span.start >= duration / 2.0)
            .collect();
        let span = *credits.iter().find(|span| span.contains(at))?;
        let scene = self
            .spans(Kind::PostCredits, Some(duration))
            .into_iter()
            .map(|scene| scene.start)
            .find(|start| *start > at);
        let gap = (credits.len() >= 2).then(|| credits[0].end);
        let to = scene.or(gap).filter(|to| *to > at)?;
        Some(Jump { span, to })
    }

    /// Where the up-next card rises. Only the credits in the second half of
    /// the item count: a credits span in the first half is an opening title
    /// sequence, not the end of the story.
    ///
    /// With no scene after the credits, the card rises at the start of the
    /// first credits span. With a scene, it rises after the scene, at the
    /// later of the start of the last credits span and the end of the last
    /// scene, so taking the offer does not drop a viewer out before the
    /// scene. The skip control covers the credits before the scene. A scene
    /// is a `post-credits` span that starts after the first credits span
    /// starts, or a second credits span, which is how TheIntroDB states a
    /// scene in the gap.
    ///
    /// The rise after a scene is capped at the time rule's point, so the card
    /// never rises later than it does for an item with no marks. The offer is
    /// how a person moves on, and a scene with no end, which IntroDB often
    /// sends, would otherwise put the rise at the end of the file, where the
    /// card never shows.
    ///
    /// Nothing here means the card rises by the time that remains.
    pub fn credits(&self, duration: Option<f64>) -> Option<f64> {
        let duration = duration.filter(|duration| *duration > 0.0)?;
        let credits: Vec<Span> = self
            .spans(Kind::Credits, Some(duration))
            .into_iter()
            .filter(|span| span.start >= duration / 2.0)
            .collect();
        let (first, last) = (credits.first()?, credits.last()?);
        let scenes: Vec<Span> = self
            .spans(Kind::PostCredits, Some(duration))
            .into_iter()
            .filter(|scene| scene.start >= duration / 2.0 && scene.start > first.start)
            .collect();
        if scenes.is_empty() && credits.len() < 2 {
            return Some(first.start);
        }
        let after = scenes
            .iter()
            .map(|scene| scene.end)
            .fold(last.start, f64::max);
        Some(after.min(crate::upnext::time_rise(duration)))
    }
}

/// One entry of the array, or nothing for an entry this module does not read.
/// An absent or null start or end is the edge of the file.
fn candidate(entry: &Value) -> Option<Candidate> {
    let kind = Kind::from_word(entry.get("kind")?.as_str()?)?;
    let seconds = |name: &str| -> Option<Option<f64>> {
        match entry.get(name) {
            None | Some(Value::Null) => Some(None),
            Some(value) => value
                .as_f64()
                .filter(|seconds| seconds.is_finite() && *seconds >= 0.0)
                .map(Some),
        }
    };
    Some(Candidate {
        kind,
        start: seconds("start")?,
        end: seconds("end")?,
    })
}

/// The middle value, or the mean of the two middle values when the count is
/// even. The caller never passes an empty list.
fn median(mut values: Vec<f64>) -> f64 {
    values.sort_by(f64::total_cmp);
    let middle = values.len() / 2;
    if values.len().is_multiple_of(2) {
        (values[middle - 1] + values[middle]) / 2.0
    } else {
        values[middle]
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn marks(value: Value) -> Marks {
        Marks::parse(Some(&value))
    }

    fn span(kind: Kind, start: f64, end: f64) -> Span {
        Span { kind, start, end }
    }

    /// The jump out of one span to its own end, which an intro or a recap
    /// offers.
    fn out(kind: Kind, start: f64, end: f64) -> Option<Jump> {
        Some(Jump {
            span: span(kind, start, end),
            to: end,
        })
    }

    /// The candidates one database returned for one episode: three intro
    /// submissions that agree within a few seconds, and one credits span.
    fn episode() -> Marks {
        marks(json!([
            {"kind":"intro","end":107.0,"source":"theintrodb"},
            {"kind":"intro","start":7.007,"end":106.482,"source":"theintrodb"},
            {"kind":"intro","start":8.0,"end":109.0,"source":"theintrodb"},
            {"kind":"credits","start":3253.0,"end":3316.0,"source":"theintrodb"},
        ]))
    }

    #[test]
    fn overlapping_candidates_merge_into_the_median_span() {
        let cases = [
            (
                "three that agree",
                episode(),
                Kind::Intro,
                vec![span(Kind::Intro, 7.007, 107.0)],
            ),
            (
                "an even count takes the mean of the middle two",
                marks(json!([
                    {"kind":"recap","start":0.0,"end":60.0},
                    {"kind":"recap","start":2.0,"end":62.0},
                ])),
                Kind::Recap,
                vec![span(Kind::Recap, 1.0, 61.0)],
            ),
            (
                "one candidate is its own span",
                episode(),
                Kind::Credits,
                vec![span(Kind::Credits, 3253.0, 3316.0)],
            ),
            (
                "two credits spans that do not overlap stay apart",
                marks(json!([
                    {"kind":"credits","start":5000.0,"end":5300.0},
                    {"kind":"credits","start":5420.0,"end":5500.0},
                    {"kind":"credits","start":5002.0,"end":5298.0},
                ])),
                Kind::Credits,
                vec![
                    span(Kind::Credits, 5001.0, 5299.0),
                    span(Kind::Credits, 5420.0, 5500.0),
                ],
            ),
            (
                "a span that only touches the next one stays apart",
                marks(json!([
                    {"kind":"intro","start":0.0,"end":60.0},
                    {"kind":"intro","start":60.0,"end":90.0},
                ])),
                Kind::Intro,
                vec![span(Kind::Intro, 0.0, 60.0), span(Kind::Intro, 60.0, 90.0)],
            ),
            ("no marks at all", Marks::default(), Kind::Intro, vec![]),
            (
                "a kind the block does not carry",
                episode(),
                Kind::Preview,
                vec![],
            ),
        ];
        for (name, marks, kind, want) in cases {
            assert_eq!(marks.spans(kind, Some(3316.0)), want, "{name}");
        }
    }

    /// An absent end is the end of the file, so it waits for the duration.
    #[test]
    fn an_absent_end_is_the_end_of_the_file() {
        let marks = marks(json!([{"kind":"credits","start":5000.0}]));
        assert_eq!(marks.spans(Kind::Credits, None), vec![]);
        assert_eq!(
            marks.spans(Kind::Credits, Some(5400.0)),
            vec![span(Kind::Credits, 5000.0, 5400.0)]
        );
    }

    /// An entry the display cannot read is dropped, and the rest still count.
    #[test]
    fn an_entry_the_display_cannot_read_is_dropped() {
        let cases = [
            (
                "an unknown kind",
                json!({"kind":"cold-open","start":0.0,"end":30.0}),
            ),
            ("no kind", json!({"start":0.0,"end":30.0})),
            (
                "a kind that is not a word",
                json!({"kind":7,"start":0.0,"end":30.0}),
            ),
            (
                "a start that is not a number",
                json!({"kind":"intro","start":"0:10","end":30.0}),
            ),
            ("a negative end", json!({"kind":"intro","end":-4.0})),
            (
                "an end before the start",
                json!({"kind":"intro","start":40.0,"end":30.0}),
            ),
            (
                "an empty span",
                json!({"kind":"intro","start":30.0,"end":30.0}),
            ),
            ("an entry that is not an object", json!("intro")),
        ];
        for (name, entry) in cases {
            let marks = marks(json!([entry, {"kind":"intro","start":100.0,"end":160.0}]));
            assert_eq!(
                marks.spans(Kind::Intro, Some(3000.0)),
                vec![span(Kind::Intro, 100.0, 160.0)],
                "{name}"
            );
        }
    }

    /// A null edge is an absent edge, the way every presentation field reads.
    #[test]
    fn a_null_edge_is_the_edge_of_the_file() {
        let marks = marks(json!([{"kind":"intro","start":null,"end":90.0}]));
        assert_eq!(
            marks.spans(Kind::Intro, None),
            vec![span(Kind::Intro, 0.0, 90.0)]
        );
    }

    #[test]
    fn a_block_with_no_array_carries_no_marks() {
        for value in [json!(null), json!({}), json!("marks"), json!(3)] {
            assert_eq!(Marks::parse(Some(&value)), Marks::default(), "{value}");
        }
        assert_eq!(Marks::parse(None), Marks::default());
    }

    #[test]
    fn the_skip_span_is_the_intro_or_recap_the_playhead_is_inside() {
        let marks = marks(json!([
            {"kind":"recap","start":0.0,"end":40.0},
            {"kind":"intro","start":60.0,"end":120.0},
            {"kind":"credits","start":3000.0,"end":3300.0},
            {"kind":"preview","start":3300.0,"end":3316.0},
        ]));
        let cases = [
            (Some(10.0), out(Kind::Recap, 0.0, 40.0)),
            (Some(40.0), None),
            (Some(59.9), None),
            (Some(60.0), out(Kind::Intro, 60.0, 120.0)),
            (Some(119.9), out(Kind::Intro, 60.0, 120.0)),
            (Some(120.0), None),
            (Some(3100.0), None),
            (Some(3310.0), None),
            (None, None),
        ];
        for (at, want) in cases {
            assert_eq!(marks.skippable(at, Some(3316.0)), want, "{at:?}");
        }
    }

    /// The end credits offer the scene after them, in either encoding, and
    /// only while the scene is still ahead of the playhead.
    #[test]
    fn the_end_credits_offer_the_scene_after_them() {
        // IntroDB: one credits span and a post-credits span of its own.
        let introdb = marks(json!([
            {"kind":"credits","start":5801.777,"end":6371.111},
            {"kind":"post-credits","start":6371.111,"end":6408.0},
            {"kind":"post-credits","start":6371.111,"end":6409.0},
        ]));
        // TheIntroDB: two credits spans with the scene in the gap.
        let theintrodb = marks(json!([
            {"kind":"credits","start":5801.777,"end":6371.111},
            {"kind":"credits","start":6408.0},
        ]));
        let credits = span(Kind::Credits, 5801.777, 6371.111);
        let tail = span(Kind::Credits, 6408.0, 6500.0);
        let scene = |span: Span, to: f64| Some(Jump { span, to });
        let cases = [
            (
                "IntroDB, inside the credits",
                &introdb,
                6000.0,
                scene(credits, 6371.111),
            ),
            ("IntroDB, inside the scene", &introdb, 6380.0, None),
            ("IntroDB, past the scene", &introdb, 6450.0, None),
            (
                "TheIntroDB, inside the credits",
                &theintrodb,
                5801.777,
                scene(credits, 6371.111),
            ),
            ("TheIntroDB, inside the scene", &theintrodb, 6380.0, None),
            (
                "TheIntroDB, inside the credits after the scene",
                &theintrodb,
                6450.0,
                None,
            ),
            ("before the credits", &theintrodb, 5000.0, None),
        ];
        for (name, marks, at, want) in cases {
            assert_eq!(marks.skippable(Some(at), Some(6500.0)), want, "{name}");
        }
        assert_eq!(theintrodb.spans(Kind::Credits, Some(6500.0))[1], tail);
    }

    /// Credits with no scene after them offer nothing, and so do credits in
    /// the first half or a film whose length mpv has not reported.
    #[test]
    fn credits_with_no_scene_offer_no_jump() {
        let cases = [
            (
                "one credits span and no scene",
                json!([{"kind":"credits","start":5800.0}]),
                Some(6500.0),
            ),
            (
                "two credits spans that overlap merge into one",
                json!([
                    {"kind":"credits","start":5800.0,"end":6400.0},
                    {"kind":"credits","start":5810.0}
                ]),
                Some(6500.0),
            ),
            (
                "an opening title sequence and the end credits",
                json!([
                    {"kind":"credits","start":5900.0,"end":6000.0},
                    {"kind":"credits","start":30.0,"end":200.0}
                ]),
                Some(6500.0),
            ),
            (
                "a scene before the credits",
                json!([
                    {"kind":"post-credits","start":100.0,"end":150.0},
                    {"kind":"credits","start":5800.0}
                ]),
                Some(6500.0),
            ),
            (
                "no duration yet",
                json!([
                    {"kind":"credits","start":5800.0,"end":6000.0},
                    {"kind":"post-credits","start":6000.0,"end":6100.0}
                ]),
                None,
            ),
        ];
        for (name, value, duration) in cases {
            assert_eq!(
                marks(value).skippable(Some(5950.0), duration),
                None,
                "{name}"
            );
        }
    }

    #[test]
    fn the_card_rises_at_the_credits_or_after_the_scene() {
        let cases = [
            ("one credits span", episode(), Some(3316.0), Some(3253.0)),
            (
                "TheIntroDB: the main credits, a scene, then more credits",
                marks(json!([
                    {"kind":"credits","start":5420.0,"end":5500.0},
                    {"kind":"credits","start":5000.0,"end":5300.0},
                ])),
                Some(6000.0),
                Some(5420.0),
            ),
            (
                "IntroDB: the credits, then a scene of its own",
                marks(json!([
                    {"kind":"credits","start":5801.777,"end":6371.111},
                    {"kind":"post-credits","start":6371.111,"end":6408.0},
                ])),
                Some(7000.0),
                Some(6408.0),
            ),
            (
                "IntroDB with more credits after the scene",
                marks(json!([
                    {"kind":"credits","start":5801.777,"end":6371.111},
                    {"kind":"post-credits","start":6371.111,"end":6408.0},
                    {"kind":"credits","start":6408.0},
                ])),
                Some(7000.0),
                Some(6408.0),
            ),
            (
                "a scene that ends at the end of the file rises by the time rule",
                marks(json!([
                    {"kind":"credits","start":5801.777,"end":6371.111},
                    {"kind":"post-credits","start":6371.111},
                ])),
                Some(6500.0),
                Some(6320.0),
            ),
            (
                "a scene inside the last three minutes rises by the time rule",
                marks(json!([
                    {"kind":"credits","start":5801.777,"end":6371.111},
                    {"kind":"credits","start":6400.0},
                ])),
                Some(6500.0),
                Some(6320.0),
            ),
            (
                "a post-credits span before the credits is no scene after them",
                marks(json!([
                    {"kind":"post-credits","start":3000.0,"end":3100.0},
                    {"kind":"credits","start":5801.777},
                ])),
                Some(6500.0),
                Some(5801.777),
            ),
            (
                "an opening title sequence in the first half moves nothing",
                marks(json!([
                    {"kind":"credits","start":30.0,"end":200.0},
                    {"kind":"credits","start":5000.0,"end":5300.0},
                ])),
                Some(5500.0),
                Some(5000.0),
            ),
            (
                "credits in the first half alone move nothing",
                marks(json!([{"kind":"credits","start":30.0,"end":200.0}])),
                Some(5500.0),
                None,
            ),
            (
                "credits that start at the middle count",
                marks(json!([{"kind":"credits","start":2750.0}])),
                Some(5500.0),
                Some(2750.0),
            ),
            (
                "no credits",
                marks(json!([{"kind":"intro","end":90.0}])),
                Some(5500.0),
                None,
            ),
            ("no duration yet", episode(), None, None),
            ("a zero duration", episode(), Some(0.0), None),
        ];
        for (name, marks, duration, want) in cases {
            assert_eq!(marks.credits(duration), want, "{name}");
        }
    }
}
