//! Reads the two numbers of an ASS script's header that place its
//! dialogue: `PlayResY` and the `MarginV` of its dialogue style. mpv
//! returns the header as the `sub-ass-extradata` property.

/// libass's `PlayResY` for a script that states neither `PlayResX` nor
/// `PlayResY`.
const DEFAULT_PLAY_RES_Y: u32 = 288;

/// Returns the `PlayResY` that libass gives a script that states only
/// `PlayResX`. libass takes the height of a 4:3 frame of that width,
/// except that a width of 1280 gives 1024, which is what VSFilter does.
fn play_res_y_of_width(play_res_x: u32) -> u32 {
    if play_res_x == 1280 {
        return 1024;
    }
    let width = play_res_x - 1;
    (width - width / 4).max(1)
}

/// The `MarginV` of the default style that libass gives a script with no
/// style of its own.
const DEFAULT_MARGIN_V: i32 = 20;

/// The fields of a style in the order that libass reads them when a
/// section has no `Format` line.
const ASS_FORMAT: &str = "Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, \
    OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, \
    Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding";
const SSA_FORMAT: &str = "Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, \
    TertiaryColour, BackColour, Bold, Italic, BorderStyle, Outline, Shadow, Alignment, MarginL, \
    MarginR, MarginV, AlphaLevel, Encoding";

/// The alignment of a style that is centred at the bottom, as ASS numbers
/// it, like the keys of a numeric keypad.
const BOTTOM_CENTRE: i32 = 2;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Script {
    pub play_res_y: u32,
    /// The `MarginV` of the style that the script's dialogue uses.
    pub margin_v: i32,
}

/// The section of the header that a line is in.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Section {
    Info,
    /// `[V4+ Styles]` of an ASS script.
    AssStyles,
    /// `[V4 Styles]` of an SSA script, whose alignments are numbered
    /// differently.
    SsaStyles,
    Other,
}

/// The fields of one style that place its text.
#[derive(Debug, Clone)]
struct Style {
    name: String,
    /// The alignment as ASS numbers it.
    alignment: i32,
    margin_v: i32,
}

impl Script {
    /// Reads the header. A value that is missing or is not a number takes
    /// the default that libass uses for it.
    ///
    /// The script's dialogue can use any of its styles, and the header
    /// does not say which one most lines use. So the margin is that of the
    /// style named `Default`, which libass gives an event whose style it
    /// cannot find, or else of the first style centred at the bottom, or
    /// else of the first style.
    pub fn parse(header: &str) -> Self {
        let mut section = Section::Other;
        let mut format: Option<Vec<String>> = None;
        let mut play_res_x = None;
        let mut play_res_y = None;
        let mut styles = Vec::new();
        for line in header.lines().map(str::trim) {
            if line.starts_with('[') {
                section = match line.to_ascii_lowercase().as_str() {
                    "[script info]" => Section::Info,
                    "[v4+ styles]" | "[v4 styles+]" => Section::AssStyles,
                    "[v4 styles]" => Section::SsaStyles,
                    _ => Section::Other,
                };
                format = None;
                continue;
            }
            let Some((key, value)) = line.split_once(':') else {
                continue;
            };
            let value = value.trim();
            match (section, key.trim()) {
                (Section::Info, "PlayResX") => {
                    play_res_x = value.parse::<u32>().ok().filter(|columns| *columns > 0);
                }
                (Section::Info, "PlayResY") => {
                    play_res_y = value.parse::<u32>().ok().filter(|rows| *rows > 0);
                }
                (Section::AssStyles | Section::SsaStyles, "Format") => {
                    format = Some(fields(value));
                }
                (Section::AssStyles | Section::SsaStyles, "Style") => {
                    let standard = match section {
                        Section::SsaStyles => SSA_FORMAT,
                        _ => ASS_FORMAT,
                    };
                    let names = format.get_or_insert_with(|| fields(standard));
                    styles.push(style(names, value, section == Section::SsaStyles));
                }
                _ => {}
            }
        }
        let dialogue = styles
            .iter()
            .find(|style| style.name.eq_ignore_ascii_case("Default"))
            .or_else(|| styles.iter().find(|style| style.alignment == BOTTOM_CENTRE))
            .or_else(|| styles.first());
        Self {
            play_res_y: play_res_y
                .or_else(|| play_res_x.map(play_res_y_of_width))
                .unwrap_or(DEFAULT_PLAY_RES_Y),
            margin_v: dialogue.map_or(DEFAULT_MARGIN_V, |style| style.margin_v),
        }
    }
}

/// The comma-separated field names of a `Format` line, in lowercase.
fn fields(format: &str) -> Vec<String> {
    format
        .split(',')
        .map(|name| name.trim().to_ascii_lowercase())
        .collect()
}

/// Reads one `Style` line in the order of `names`. The last field takes
/// the rest of the line, as libass reads it. libass reads a number that
/// does not parse as 0, and drops the stars at the start of a name.
fn style(names: &[String], line: &str, ssa: bool) -> Style {
    let values: Vec<&str> = line
        .splitn(names.len().max(1), ',')
        .map(str::trim)
        .collect();
    let field = |name: &str| {
        names
            .iter()
            .position(|known| known == name)
            .and_then(|at| values.get(at).copied())
            .unwrap_or_default()
    };
    let number = |name: &str| field(name).parse::<i32>().unwrap_or(0);
    let alignment = number("alignment");
    Style {
        name: field("name").trim_start_matches('*').to_string(),
        alignment: if ssa { numpad(alignment) } else { alignment },
        margin_v: number("marginv"),
    }
}

/// Converts an SSA alignment to the ASS number. SSA gives the side as 1,
/// 2, or 3, and adds 4 for the top of the frame and 8 for the middle.
/// ASS numbers the positions like a numeric keypad, with the bottom row
/// at 1 to 3.
fn numpad(ssa: i32) -> i32 {
    let side = ssa & 3;
    let row = match ssa & 12 {
        4 => 6,
        8 => 3,
        _ => 0,
    };
    side + row
}

#[cfg(test)]
mod tests {
    use super::*;

    const FORMAT: &str = "Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, \
        OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, \
        Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding";

    /// A style line in the order of `FORMAT`, with its name, alignment, and
    /// `MarginV`.
    fn style(name: &str, alignment: u8, margin_v: i32) -> String {
        format!(
            "Style: {name},Arial,48,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,0,{alignment},10,10,{margin_v},1"
        )
    }

    fn script(info: &str, styles: &[String]) -> String {
        format!(
            "[Script Info]\nScriptType: v4.00+\n{info}\n\n[V4+ Styles]\n{FORMAT}\n{}\n\n[Events]\nFormat: Layer, Start, End, Style, Text\n",
            styles.join("\n")
        )
    }

    #[test]
    fn the_default_style_gives_the_margin() {
        let header = script(
            "PlayResX: 1920\nPlayResY: 804",
            &[style("Sign", 8, 40), style("Default", 2, 20)],
        );
        assert_eq!(
            Script::parse(&header),
            Script {
                play_res_y: 804,
                margin_v: 20
            }
        );
    }

    /// Each case is the styles of a script and the `MarginV` that the
    /// display reads from them.
    #[test]
    fn a_script_with_no_default_style_uses_its_first_bottom_centred_style() {
        let cases = [
            (
                "a bottom-centred style after a top one",
                vec![style("Top", 8, 50), style("Main", 2, 30)],
                30,
            ),
            (
                "no bottom-centred style",
                vec![style("Left", 1, 44), style("Top", 8, 50)],
                44,
            ),
            (
                "a default style named in another case",
                vec![style("Top", 8, 50), style("default", 8, 12)],
                12,
            ),
            (
                "a default style marked with a star",
                vec![style("Top", 8, 50), style("*Default", 8, 14)],
                14,
            ),
        ];
        for (name, styles, margin_v) in cases {
            let parsed = Script::parse(&script("PlayResY: 1080", &styles));
            assert_eq!(parsed.margin_v, margin_v, "{name}: {parsed:?}");
        }
    }

    /// The fields of a style are in the order of the section's `Format`
    /// line, whatever order the script chose.
    #[test]
    fn a_style_is_read_in_the_order_of_its_format_line() {
        let header = "[Script Info]\nPlayResY: 720\n\n[V4+ Styles]\n\
            Format: MarginV, Alignment, Name, Fontname\n\
            Style: 26, 8, Top, Arial\n\
            Style: 33, 2, Main, Arial\n";
        assert_eq!(
            Script::parse(header),
            Script {
                play_res_y: 720,
                margin_v: 33
            }
        );
    }

    /// An SSA script numbers its alignments differently: 6 is the top
    /// centre, where ASS has 8, and 10 is the middle centre.
    #[test]
    fn an_ssa_script_reads_its_own_alignment_numbers() {
        let header = "[Script Info]\nScriptType: v4.00\nPlayResY: 480\n\n[V4 Styles]\n\
            Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, TertiaryColour, BackColour, Bold, Italic, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, AlphaLevel, Encoding\n\
            Style: Top,Arial,24,16777215,65535,65535,0,0,0,1,2,0,6,10,10,40,0,0\n\
            Style: Middle,Arial,24,16777215,65535,65535,0,0,0,1,2,0,10,10,10,35,0,0\n\
            Style: Main,Arial,24,16777215,65535,65535,0,0,0,1,2,0,2,10,10,18,0,0\n";
        assert_eq!(
            Script::parse(header),
            Script {
                play_res_y: 480,
                margin_v: 18
            }
        );
    }

    /// libass reads a style section that has no `Format` line in the
    /// standard order of its kind.
    #[test]
    fn a_style_section_with_no_format_line_is_read_in_the_standard_order() {
        let header = format!(
            "[Script Info]\nPlayResY: 1080\n\n[V4+ Styles]\n{}\n",
            style("Main", 2, 27)
        );
        assert_eq!(Script::parse(&header).margin_v, 27);
    }

    /// libass works out the `PlayResY` of a script that states only
    /// `PlayResX`, so the display has to use the same number.
    #[test]
    fn a_header_with_only_a_width_takes_the_height_that_libass_gives_it() {
        let cases = [(640, 480), (1920, 1440), (1280, 1024), (1, 1)];
        for (play_res_x, play_res_y) in cases {
            let header = format!("[Script Info]\nPlayResX: {play_res_x}\n");
            assert_eq!(
                Script::parse(&header).play_res_y,
                play_res_y,
                "PlayResX {play_res_x}"
            );
        }
    }

    /// libass uses a `PlayResY` of 288 when the script states neither
    /// number, and its own default style, whose `MarginV` is 20, when the script has
    /// no style.
    #[test]
    fn a_header_that_states_nothing_reads_as_the_defaults_of_libass() {
        let cases = [
            ("an empty header", String::new()),
            (
                "no styles",
                "[Script Info]\nScriptType: v4.00+\n".to_string(),
            ),
            (
                "a PlayResY that is not a number",
                "[Script Info]\nPlayResY: tall\n[V4+ Styles]\n".to_string(),
            ),
            (
                "a PlayResY of zero",
                "[Script Info]\nPlayResY: 0\n".to_string(),
            ),
        ];
        for (name, header) in cases {
            assert_eq!(
                Script::parse(&header),
                Script {
                    play_res_y: 288,
                    margin_v: 20
                },
                "{name}"
            );
        }
    }

    /// A header written on Windows ends each line with a carriage return.
    #[test]
    fn a_header_with_carriage_returns_reads_the_same() {
        let header = script("PlayResY: 804", &[style("Default", 2, 20)]).replace('\n', "\r\n");
        assert_eq!(
            Script::parse(&header),
            Script {
                play_res_y: 804,
                margin_v: 20
            }
        );
    }
}
