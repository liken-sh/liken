//! Finds the cover art of the playing music item. The display tries the
//! item's `presentation.art` first, then the picture embedded in the media
//! file's tags, then a cover file in the file's folder. An album plays as one
//! EDL timeline, so the file tiers read the timeline's first segment, and the
//! whole album gets one cover.

use std::path::Path;

use lofty::config::ParseOptions;
use lofty::file::TaggedFileExt;
use lofty::probe::Probe;

use super::decode;

/// Where an item's art can come from, in the order the display tries them.
/// `presentation.art` comes first because it is how the library describes
/// the item, and the display prefers that over the file's tags. A picture
/// embedded in the file comes before a folder cover, because it belongs to
/// that track and the folder cover belongs to the whole album.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Tier {
    /// The item's `presentation.art`, a path in the pod or an https URL.
    Presentation(String),
    /// The picture in the tags of the named media file.
    Embedded(String),
    /// A cover file in the track's folder.
    Folder(String),
}

/// The file names of a folder cover, in the order the display tries them.
/// Rippers and library managers write one of these files in each album
/// folder.
const COVER_NAMES: [&str; 4] = ["cover.jpg", "Cover.jpg", "folder.jpg", "Folder.jpg"];

/// An item refers to an EDL timeline either as a file with the `.edl`
/// extension or as an `edl://` URL. In the URL form a semicolon replaces
/// each newline.
const EDL_SCHEME: &str = "edl://";
const EDL_URL_SEPARATOR: char = ';';
const EDL_FILE_EXTENSION: &str = ".edl";

/// The first line of an mpv EDL file. A file that starts with anything else
/// is not a timeline. The `edl://` URL form has no header line.
const EDL_HEADER: &str = "# mpv EDL v0";

/// Picks the art tier for one item from its `presentation.art` and the file
/// that mpv plays. Returns `None` when no tier has art.
pub fn resolve(art: Option<&str>, file: Option<&str>) -> Option<Tier> {
    if let Some(art) = art.filter(|art| !art.is_empty()) {
        return Some(Tier::Presentation(art.to_string()));
    }
    let path = album_source_path(file?)?;
    if embedded(&path).is_some() {
        return Some(Tier::Embedded(path));
    }
    folder_cover(&path).map(Tier::Folder)
}

/// Reads the image bytes for a tier.
pub fn read(tier: &Tier) -> Option<Vec<u8>> {
    match tier {
        Tier::Presentation(source) | Tier::Folder(source) => decode::open(source),
        Tier::Embedded(source) => embedded(source),
    }
}

/// The first picture in a media file's tags, or `None` when the file has no
/// tags or no picture.
fn embedded(path: &str) -> Option<Vec<u8>> {
    let tagged = Probe::open(path)
        .ok()?
        // The audio properties (sample rate, bit rate) are not drawn, so
        // skip parsing them.
        .options(ParseOptions::new().read_properties(false))
        .read()
        .ok()?;
    let tag = tagged.primary_tag().or_else(|| tagged.first_tag())?;
    let picture = tag.pictures().first()?.data();
    (!picture.is_empty()).then(|| picture.to_vec())
}

/// The local media file that the embedded and folder tiers read: the item
/// itself, or the first segment of an EDL timeline.
fn album_source_path(file: &str) -> Option<String> {
    let Some((text, dir)) = timeline(file) else {
        return local_media_path(file);
    };
    Some(segment_path(&dir, &first_segment(&text)?))
}

/// The timeline text of an item and the directory that relative segment
/// paths resolve against. The timeline is a `.edl` file that the player shim
/// wrote, or the text of an `edl://` URL. Returns `None` when the item is not
/// a timeline.
fn timeline(file: &str) -> Option<(String, String)> {
    if let Some(inline) = file.strip_prefix(EDL_SCHEME) {
        return Some((inline.replace(EDL_URL_SEPARATOR, "\n"), String::new()));
    }
    let path = local_media_path(file)?;
    if !Path::new(&path)
        .extension()
        .is_some_and(|extension| extension.eq_ignore_ascii_case(&EDL_FILE_EXTENSION[1..]))
    {
        return None;
    }
    let text = std::fs::read_to_string(&path).ok()?;
    if !text.trim().starts_with(EDL_HEADER) {
        return None;
    }
    let dir = Path::new(&path).parent()?.to_string_lossy().to_string();
    Some((text, dir))
}

/// The file of the timeline's first segment. Comment lines and mpv's `!`
/// header lines hold no file, so they are skipped. A line that does not parse
/// is skipped too, so one malformed segment does not stop the cover lookup.
fn first_segment(text: &str) -> Option<String> {
    text.lines()
        .map(|line| line.trim_end_matches('\r').trim())
        .filter(|line| !line.is_empty() && !line.starts_with('#') && !line.starts_with('!'))
        .find_map(segment_file)
}

/// The file name at the start of one segment line. mpv lets a value be
/// quoted as `%<length>%<text>`, so a file name can contain a comma. Only a
/// file name in the first position is read, so a line that starts with
/// `key=value` gives `None`.
fn segment_file(line: &str) -> Option<String> {
    let (file, rest) = match line.starts_with('%') {
        true => quoted(line)?,
        false => {
            let end = line.find([',', '=']).unwrap_or(line.len());
            (line[..end].to_string(), &line[end..])
        }
    };
    (!file.is_empty() && !rest.starts_with('=')).then_some(file)
}

/// Reads one `%<length>%<text>` value at the start of a line and returns the
/// text and the rest of the line. Returns `None` when the length does not
/// parse or runs past the end of the line.
fn quoted(line: &str) -> Option<(String, &str)> {
    let end = line[1..].find('%')? + 1;
    let length: usize = line[1..end].parse().ok()?;
    let text = line.get(end + 1..end + 1 + length)?;
    Some((text.to_string(), &line[end + 1 + length..]))
}

/// The location of a segment's file. Like mpv, this resolves a relative path
/// against the timeline file's directory, and keeps an absolute path or a URL
/// as it is.
fn segment_path(dir: &str, file: &str) -> String {
    if dir.is_empty() || Path::new(file).is_absolute() || file.contains("://") {
        return file.to_string();
    }
    Path::new(dir).join(file).to_string_lossy().to_string()
}

/// A playlist entry as a local path, or `None` for a URI of another scheme.
fn local_media_path(entry: &str) -> Option<String> {
    let path = entry.strip_prefix("file://").unwrap_or(entry);
    (!path.is_empty() && !path.contains("://")).then(|| path.to_string())
}

/// The first of `COVER_NAMES` that exists as a file in the track's folder.
fn folder_cover(path: &str) -> Option<String> {
    let dir = Path::new(path).parent()?;
    COVER_NAMES.iter().find_map(|name| {
        let candidate = dir.join(name);
        candidate
            .metadata()
            .ok()
            .filter(std::fs::Metadata::is_file)
            .map(|_| candidate.to_string_lossy().to_string())
    })
}

#[cfg(test)]
mod tests {
    use super::super::decode::tests::encoded;
    use super::*;
    use image::ImageFormat;
    use std::path::PathBuf;

    /// A separate album folder for each test, emptied before the test writes
    /// into it.
    fn folder(name: &str) -> PathBuf {
        let dir = std::env::temp_dir().join(format!("media-display-{name}.album"));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).expect("an album folder");
        dir
    }

    /// A media file with an ID3v2.3 tag that holds a title, and a picture
    /// when the test passes one.
    fn media(dir: &Path, name: &str, picture: Option<Vec<u8>>) -> String {
        let mut body = frame("TIT2", &[&[0][..], b"Track"].concat());
        if let Some(picture) = picture {
            let payload = [&[0][..], b"image/png", &[0, 3, 0], &picture].concat();
            body.extend(frame("APIC", &payload));
        }
        let size = 10 + body.len();
        let mut file = b"ID3".to_vec();
        file.extend([3, 0, 0]);
        file.extend([
            (size >> 21) as u8 & 0x7f,
            (size >> 14) as u8 & 0x7f,
            (size >> 7) as u8 & 0x7f,
            size as u8 & 0x7f,
        ]);
        file.extend(body);
        file.extend([0xff; 8]);
        let path = dir.join(name);
        std::fs::write(&path, file).expect("a media file to read back");
        path.to_string_lossy().to_string()
    }

    /// One ID3v2.3 frame: the four-character name, the payload length, two
    /// flag bytes, then the payload.
    fn frame(name: &str, payload: &[u8]) -> Vec<u8> {
        let mut bytes = name.as_bytes().to_vec();
        bytes.extend((payload.len() as u32).to_be_bytes());
        bytes.extend([0, 0]);
        bytes.extend(payload);
        bytes
    }

    fn png(width: u32, height: u32) -> Vec<u8> {
        encoded(width, height, ImageFormat::Png, [200, 40, 40, 255])
    }

    /// A cover file in the album folder.
    fn cover(dir: &Path, name: &str) -> String {
        let path = dir.join(name);
        std::fs::write(&path, png(8, 8)).expect("a cover to read back");
        path.to_string_lossy().to_string()
    }

    /// An EDL file in the format the player shim writes for an album.
    fn timeline_file(dir: &Path, files: &[&str]) -> String {
        let mut text = format!("{EDL_HEADER}\n");
        for file in files {
            text.push_str(&format!("%{}%{file},title=%3%One\n", file.len()));
        }
        let path = dir.join("album.edl");
        std::fs::write(&path, text).expect("a timeline to read back");
        path.to_string_lossy().to_string()
    }

    #[test]
    fn the_presentation_art_beats_the_embedded_picture_and_the_folder_cover() {
        let dir = folder("presentation");
        cover(&dir, "cover.jpg");
        let file = media(&dir, "track.mp3", Some(png(8, 8)));
        assert_eq!(
            resolve(Some("https://art.example/cover.png"), Some(&file)),
            Some(Tier::Presentation(
                "https://art.example/cover.png".to_string()
            ))
        );
    }

    #[test]
    fn the_embedded_picture_beats_the_folder_cover() {
        let dir = folder("embedded");
        cover(&dir, "cover.jpg");
        let file = media(&dir, "track.mp3", Some(png(8, 8)));
        assert_eq!(
            resolve(None, Some(&file)),
            Some(Tier::Embedded(file.clone()))
        );
        assert_eq!(read(&Tier::Embedded(file)), Some(png(8, 8)));
    }

    #[test]
    fn a_file_with_no_picture_falls_back_to_the_folder_cover_and_then_to_none() {
        let dir = folder("folder");
        let beside = cover(&dir, "cover.jpg");
        let file = media(&dir, "track.mp3", None);
        assert_eq!(resolve(None, Some(&file)), Some(Tier::Folder(beside)));

        let bare = folder("bare");
        let alone = media(&bare, "track.mp3", None);
        assert_eq!(resolve(None, Some(&alone)), None);
        assert_eq!(resolve(Some(""), Some(&alone)), None);
        assert_eq!(resolve(None, None), None);
    }

    #[test]
    fn a_remote_track_reads_no_picture_and_no_cover() {
        let dir = folder("remote");
        cover(&dir, "cover.jpg");
        assert_eq!(resolve(None, Some("https://media.example/track.mp3")), None);
    }

    #[test]
    fn a_file_uri_reads_the_picture_at_its_path() {
        let dir = folder("uri");
        let file = media(&dir, "track.mp3", Some(png(8, 8)));
        assert_eq!(
            resolve(None, Some(&format!("file://{file}"))),
            Some(Tier::Embedded(file))
        );
    }

    #[test]
    fn an_album_reads_the_picture_in_its_first_segment() {
        let dir = folder("album");
        let first = media(&dir, "one.mp3", Some(png(8, 8)));
        media(&dir, "two.mp3", None);
        let album = timeline_file(&dir, &["one.mp3", "two.mp3"]);
        assert_eq!(resolve(None, Some(&album)), Some(Tier::Embedded(first)));
    }

    #[test]
    fn an_album_with_no_picture_reads_the_folder_cover_of_its_first_segment() {
        let dir = folder("albumcover");
        let beside = cover(&dir, "folder.jpg");
        media(&dir, "one.mp3", None);
        let album = timeline_file(&dir, &["one.mp3"]);
        assert_eq!(resolve(None, Some(&album)), Some(Tier::Folder(beside)));
    }

    #[test]
    fn the_edl_url_form_reads_its_first_segment() {
        let dir = folder("inline");
        let first = media(&dir, "one.mp3", Some(png(8, 8)));
        let second = media(&dir, "two.mp3", None);
        let url = format!("edl://{first},title=Track One;{second},title=Track Two");
        assert_eq!(resolve(None, Some(&url)), Some(Tier::Embedded(first)));
    }

    #[test]
    fn a_relative_segment_path_resolves_against_the_timeline_directory() {
        assert_eq!(
            segment_path("/media/album", "one.mp3"),
            "/media/album/one.mp3"
        );
        assert_eq!(
            segment_path("/media/album", "/other/one.mp3"),
            "/other/one.mp3"
        );
        assert_eq!(segment_path("", "one.mp3"), "one.mp3");
        assert_eq!(
            segment_path("/media/album", "https://one.example/one.mp3"),
            "https://one.example/one.mp3"
        );
    }

    #[test]
    fn the_first_segment_is_the_first_line_that_names_a_file() {
        assert_eq!(
            first_segment("# mpv EDL v0\n!no_chapters\n\n%7%one.mp3,title=%3%One\n"),
            Some("one.mp3".to_string())
        );
        assert_eq!(
            first_segment("# mpv EDL v0\none.mp3,title=One\n"),
            Some("one.mp3".to_string())
        );
        assert_eq!(
            first_segment("# mpv EDL v0\r\n%7%one.mp3\r\n"),
            Some("one.mp3".to_string())
        );
        for text in [
            "# mpv EDL v0\n",
            "# mpv EDL v0\ntitle=One\n",
            "# mpv EDL v0\n%wide%one.mp3\n",
            "# mpv EDL v0\n%70%one.mp3\n",
            "# mpv EDL v0\n%7one.mp3\n",
        ] {
            assert_eq!(first_segment(text), None, "{text}");
        }
    }

    #[test]
    fn a_file_that_is_not_a_timeline_reads_as_a_media_file() {
        let dir = folder("notimeline");
        let file = media(&dir, "track.mp3", Some(png(8, 8)));
        assert_eq!(timeline(&file), None);

        let bare = dir.join("album.edl");
        std::fs::write(&bare, "one.mp3\n").expect("a file with no header");
        assert_eq!(timeline(&bare.to_string_lossy()), None);
        assert_eq!(timeline("/media/nothing.edl"), None);
    }

    #[test]
    fn the_folder_cover_names_are_tried_in_order() {
        for (present, want) in [
            (&COVER_NAMES[..], Some("cover.jpg")),
            (&COVER_NAMES[1..], Some("Cover.jpg")),
            (&COVER_NAMES[2..], Some("folder.jpg")),
            (&COVER_NAMES[3..], Some("Folder.jpg")),
            (&COVER_NAMES[4..], None),
        ] {
            let dir = folder("covers");
            for name in present {
                cover(&dir, name);
            }
            assert_eq!(
                folder_cover(&dir.join("track.mp3").to_string_lossy()),
                want.map(|name| dir.join(name).to_string_lossy().to_string()),
                "{present:?}"
            );
        }
    }

    #[test]
    fn a_directory_named_like_a_cover_is_no_cover() {
        let dir = folder("directory");
        std::fs::create_dir(dir.join("cover.jpg")).expect("a directory to skip");
        let beside = cover(&dir, "folder.jpg");
        assert_eq!(
            folder_cover(&dir.join("track.mp3").to_string_lossy()),
            Some(beside)
        );
    }

    #[test]
    fn every_tier_reads_the_bytes_it_names() {
        let dir = folder("reads");
        let beside = cover(&dir, "cover.jpg");
        assert_eq!(read(&Tier::Folder(beside.clone())), Some(png(8, 8)));
        assert_eq!(read(&Tier::Presentation(beside)), Some(png(8, 8)));
        assert_eq!(
            read(&Tier::Presentation("/art/nothing.png".to_string())),
            None
        );
        assert_eq!(read(&Tier::Embedded("/art/nothing.mp3".to_string())), None);
    }
}
