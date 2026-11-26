//! ASS and SSA: a whole script in a file, and the events of a Matroska
//! track, whose header is the track's `CodecPrivate` and whose blocks each
//! carry one event. The copy keeps the header and every field of each
//! `Dialogue` event as the script wrote them.

use super::{Copy, Dialogue};

/// The fields of an event in the order of `Copy::script`'s `Format` line.
const FIELDS: [&str; 10] = [
    "layer", "start", "end", "style", "name", "marginl", "marginr", "marginv", "effect", "text",
];

/// The header of a script up to its `[Events]` section, and whether it is an
/// SSA script.
pub fn header(script: &str) -> (String, bool) {
    let lower = script.to_ascii_lowercase();
    let end = lower.find("[events]").unwrap_or(script.len());
    let header = script[..end].to_string();
    let ssa = lower[..end].contains("[v4 styles]");
    (header, ssa)
}

/// An ASS time, `H:MM:SS.cc`, in milliseconds.
pub fn time(text: &str) -> Option<u64> {
    let (clock, fraction) = text.trim().split_once('.')?;
    let mut fields = clock.split(':').map(|field| field.parse::<u64>().ok());
    let (hours, minutes, seconds) = (fields.next()??, fields.next()??, fields.next()??);
    // The fraction counts by place value, so `.5` is half a second.
    let digits: String = fraction.chars().chain("00".chars()).take(2).collect();
    let centis: u64 = digits.parse().ok()?;
    Some(((hours * 60 + minutes) * 60 + seconds) * 1000 + centis * 10)
}

/// A whole script. A `Comment` event is not drawn, so it is dropped.
pub fn parse(script: &str) -> Copy {
    let (header, ssa) = header(script);
    let mut format: Vec<String> = FIELDS.iter().map(|name| name.to_string()).collect();
    let mut events = Vec::new();
    let mut in_events = false;
    for line in script.lines().map(str::trim) {
        if line.starts_with('[') {
            in_events = line.eq_ignore_ascii_case("[events]");
            continue;
        }
        if !in_events {
            continue;
        }
        if let Some(names) = line.strip_prefix("Format:") {
            format = names
                .split(',')
                .map(|name| name.trim().to_ascii_lowercase())
                .collect();
            continue;
        }
        let Some(values) = line.strip_prefix("Dialogue:") else {
            continue;
        };
        let values: Vec<&str> = values.trim_start().splitn(format.len(), ',').collect();
        let field = |name: &str| {
            format
                .iter()
                .position(|known| known == name)
                .and_then(|at| values.get(at).copied())
                .unwrap_or_default()
                .to_string()
        };
        let (Some(start), Some(end)) = (time(&field("start")), time(&field("end"))) else {
            continue;
        };
        let layer = match ssa {
            true => field("marked"),
            false => field("layer"),
        };
        events.push(Dialogue {
            start,
            end,
            layer: if layer.is_empty() {
                "0".to_string()
            } else {
                layer
            },
            style: field("style"),
            name: field("name"),
            margin_l: field("marginl"),
            margin_r: field("marginr"),
            margin_v: field("marginv"),
            effect: field("effect"),
            text: field("text"),
        });
    }
    Copy {
        header,
        ssa,
        converted: false,
        events,
    }
}

/// The `ReadOrder` and the event of one Matroska block. The block carries
/// `ReadOrder, Layer, Style, Name, MarginL, MarginR, MarginV, Effect, Text`,
/// and its times come from the block.
pub fn block(payload: &str, start: u64, end: u64) -> Option<(u64, Dialogue)> {
    let fields: Vec<&str> = payload.splitn(9, ',').collect();
    let [
        order,
        layer,
        style,
        name,
        margin_l,
        margin_r,
        margin_v,
        effect,
        text,
    ] = fields.as_slice()
    else {
        return None;
    };
    let event = Dialogue {
        start,
        end,
        layer: layer.to_string(),
        style: style.to_string(),
        name: name.to_string(),
        margin_l: margin_l.to_string(),
        margin_r: margin_r.to_string(),
        margin_v: margin_v.to_string(),
        effect: effect.to_string(),
        text: text.to_string(),
    };
    Some((order.trim().parse().ok()?, event))
}

#[cfg(test)]
mod tests {
    use super::*;

    const SCRIPT: &str = "[Script Info]\nScriptType: v4.00+\nPlayResY: 1080\n\n[V4+ Styles]\n\
        Style: Default,Arial,48,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,0,2,10,10,20,1\n\n\
        [Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n\
        Comment: 0,0:00:00.00,0:00:01.00,Default,,0,0,0,,not drawn\n\
        Dialogue: 0,0:00:02.50,0:00:04.00,Default,Someone,0,0,0,,The first, with a comma\\Nand the second\n";

    #[test]
    fn a_script_keeps_its_header_and_its_dialogue() {
        let copy = parse(SCRIPT);
        assert!(copy.header.ends_with("0,0,1,2,0,2,10,10,20,1\n\n"));
        assert!(!copy.ssa);
        assert_eq!(
            copy.events,
            [Dialogue {
                name: "Someone".to_string(),
                ..Dialogue::text(
                    2500,
                    4000,
                    "The first, with a comma\\Nand the second".to_string()
                )
            }]
        );
    }

    #[test]
    fn an_ssa_script_is_known_by_its_styles() {
        let script = "[Script Info]\nScriptType: v4.00\n\n[V4 Styles]\n\n[Events]\n\
            Format: Marked, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n\
            Dialogue: Marked=0,0:00:01.00,0:00:02.00,Default,,0000,0000,0000,,Old\n";
        let copy = parse(script);
        assert!(copy.ssa);
        assert_eq!(copy.events[0].layer, "Marked=0");
        assert_eq!(copy.events[0].margin_v, "0000");
    }

    #[test]
    fn a_fraction_counts_by_place_value() {
        let cases = [
            ("0:00:02.5", 2500),
            ("0:00:02.50", 2500),
            ("0:00:02.05", 2050),
            ("1:02:03.4", 3_723_400),
        ];
        for (text, want) in cases {
            assert_eq!(time(text), Some(want), "{text}");
        }
    }

    #[test]
    fn a_matroska_block_gives_one_event() {
        assert_eq!(
            block("12,0,Default,,0,0,0,,Two\\Nlines, here", 1000, 2000),
            Some((
                12,
                Dialogue::text(1000, 2000, "Two\\Nlines, here".to_string())
            ))
        );
        assert_eq!(block("not an event", 0, 1), None);
    }
}
