//! The music art pipeline. An album's cover lives inside its media files as
//! often as it lives in the Play's spec, so the display resolves the playing
//! item's art through tiers and reads a file's tags where the block is silent.
//! An album arrives as one EDL item, and its first segment carries the cover
//! for the whole timeline.

use std::path::Path;

use lofty::config::ParseOptions;
use lofty::file::TaggedFileExt;
use lofty::probe::Probe;

use super::decode;

/// The tiers an item's art resolves through, in the order the display tries
/// them. A reference the block states beats the file, because the block
/// carries what the operator resolved from the spec. The picture inside the
/// file beats a cover beside it, because a file's own art is more specific
/// than its folder's. An item that reaches the end of the list draws no art.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Tier {
    /// The block's own reference, a pod path or an https URL.
    Block(String),
    /// The picture inside the named media file's tags.
    Embedded(String),
    /// A cover file beside the track.
    Sibling(String),
}

/// The sibling cover file names, in the order the display tries them. Rippers
/// and library managers write one cover file into an album's folder, so one
/// file serves every track in it.
const COVER_NAMES: [&str; 4] = ["cover.jpg", "Cover.jpg", "folder.jpg", "Folder.jpg"];

/// The two ways an item names a timeline, and the separator that stands in for
/// a newline inside the URL form.
const EDL_SCHEME: &str = "edl://";
const EDL_URL_SEPARATOR: char = ';';
const EDL_FILE_EXTENSION: &str = ".edl";

/// The header mpv reads an EDL by. A file that opens with anything else is no
/// timeline. The `edl://` URL form carries no header, because the scheme has
/// already said what the text is.
const EDL_HEADER: &str = "# mpv EDL v0";

/// Settle one item's art from its presentation block and the file mpv plays.
/// An album is one EDL item, so the file tiers read the album's first segment,
/// and one album resolves one cover.
pub fn resolve(art: Option<&str>, file: Option<&str>) -> Option<Tier> {
    if let Some(art) = art.filter(|art| !art.is_empty()) {
        return Some(Tier::Block(art.to_string()));
    }
    let path = album_source_path(file?)?;
    if embedded(&path).is_some() {
        return Some(Tier::Embedded(path));
    }
    sibling_cover(&path).map(Tier::Sibling)
}

/// Read the art the tier named. The block's reference and the sibling cover
/// are files or URLs the logo path already opens, and the embedded picture
/// comes out of the media file itself.
pub fn read(tier: &Tier) -> Option<Vec<u8>> {
    match tier {
        Tier::Block(source) | Tier::Sibling(source) => decode::open(source),
        Tier::Embedded(source) => embedded(source),
    }
}

/// The picture inside one media file's tags. A file with no tags is not an
/// error here: it is an item whose art comes from the block, from a cover
/// beside it, or from nowhere.
fn embedded(path: &str) -> Option<Vec<u8>> {
    let tagged = Probe::open(path)
        .ok()?
        // The properties are the sample rate and the bit rate, which nothing
        // here draws, so the read stops at the tags.
        .options(ParseOptions::new().read_properties(false))
        .read()
        .ok()?;
    let tag = tagged.primary_tag().or_else(|| tagged.first_tag())?;
    let picture = tag.pictures().first()?.data();
    (!picture.is_empty()).then(|| picture.to_vec())
}

/// The file the two file tiers read. A plain media file is its own source. An
/// EDL is a timeline of tracks, so the album's first segment stands for the
/// whole of it.
fn album_source_path(file: &str) -> Option<String> {
    let Some((text, dir)) = timeline(file) else {
        return local_media_path(file);
    };
    Some(segment_path(&dir, &first_segment(&text)?))
}

/// The timeline an item names, and the directory a relative segment path is
/// measured from. mpv reads a timeline from a file the shim wrote or from an
/// `edl://` URL, where a semicolon stands in for the newline. An item that
/// names no timeline says so, and the caller reads it as a plain media file.
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

/// The file the timeline's first segment plays. The header, the comments, and
/// the `!` lines carry stream and chapter directives no reader here has a use
/// for, so they are skipped. A line the reader cannot read is skipped rather
/// than fatal, so one malformed segment costs the album one cover and not the
/// run.
fn first_segment(text: &str) -> Option<String> {
    text.lines()
        .map(|line| line.trim_end_matches('\r').trim())
        .filter(|line| !line.is_empty() && !line.starts_with('#') && !line.starts_with('!'))
        .find_map(segment_file)
}

/// The file name one segment line opens with. The format quotes a value as a
/// percent sign, the length in bytes, another percent sign, then the text,
/// which is what lets a file name carry a comma. A line whose first field
/// carries a name is a line with no file in front, so it names no segment.
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

/// One `%<length>%<text>` run, and where the line goes on. A length that does
/// not parse, or one that runs past the end of the line, is a line the reader
/// cannot read.
fn quoted(line: &str) -> Option<(String, &str)> {
    let end = line[1..].find('%')? + 1;
    let length: usize = line[1..end].parse().ok()?;
    let text = line.get(end + 1..end + 1 + length)?;
    Some((text.to_string(), &line[end + 1 + length..]))
}

/// Where one segment's file lives. A relative path is measured from the
/// timeline's own directory, the way mpv measures it, and an absolute path or
/// a URL stands on its own.
fn segment_path(dir: &str, file: &str) -> String {
    if dir.is_empty() || Path::new(file).is_absolute() || file.contains("://") {
        return file.to_string();
    }
    Path::new(dir).join(file).to_string_lossy().to_string()
}

/// One playlist entry as a path the display can open. It reads nothing for a
/// URI it cannot read as a file, because the tags and the sibling cover both
/// need a local file.
fn local_media_path(entry: &str) -> Option<String> {
    let path = entry.strip_prefix("file://").unwrap_or(entry);
    (!path.is_empty() && !path.contains("://")).then(|| path.to_string())
}

/// The album's own cover file beside the track.
fn sibling_cover(path: &str) -> Option<String> {
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

    /// One album folder of its own, emptied before each test writes into it.
    fn folder(name: &str) -> PathBuf {
        let dir = std::env::temp_dir().join(format!("media-osd-{name}.album"));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).expect("an album folder");
        dir
    }

    /// One media file with an ID3v2.3 tag: a title, and a picture when the
    /// test states one.
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

    /// One ID3 frame: the four-character name, the payload length, two flag
    /// bytes, then the payload.
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

    /// One cover file beside the tracks.
    fn cover(dir: &Path, name: &str) -> String {
        let path = dir.join(name);
        std::fs::write(&path, png(8, 8)).expect("a cover to read back");
        path.to_string_lossy().to_string()
    }

    /// One timeline, the way the player shim writes an album.
    fn timeline_file(dir: &Path, files: &[&str]) -> String {
        let mut text = format!("{EDL_HEADER}\n");
        for file in files {
            text.push_str(&format!("%{}%{file},title=%3%One\n", file.len()));
        }
        let path = dir.join("album.edl");
        std::fs::write(&path, text).expect("a timeline to read back");
        path.to_string_lossy().to_string()
    }

    /// The block's own reference beats the file and the cover beside it.
    #[test]
    fn the_blocks_art_beats_the_file_and_the_cover() {
        let dir = folder("block");
        cover(&dir, "cover.jpg");
        let file = media(&dir, "track.mp3", Some(png(8, 8)));
        assert_eq!(
            resolve(Some("https://art.example/cover.png"), Some(&file)),
            Some(Tier::Block("https://art.example/cover.png".to_string()))
        );
    }

    /// The picture inside the file beats a cover beside it.
    #[test]
    fn the_embedded_picture_beats_the_cover_beside_it() {
        let dir = folder("embedded");
        cover(&dir, "cover.jpg");
        let file = media(&dir, "track.mp3", Some(png(8, 8)));
        assert_eq!(
            resolve(None, Some(&file)),
            Some(Tier::Embedded(file.clone()))
        );
        assert_eq!(read(&Tier::Embedded(file)), Some(png(8, 8)));
    }

    /// A file that embeds no picture falls to the cover beside it, and a track
    /// with no art anywhere reaches the end of the list.
    #[test]
    fn a_file_with_no_picture_falls_to_the_cover_and_then_to_nothing() {
        let dir = folder("sibling");
        let beside = cover(&dir, "cover.jpg");
        let file = media(&dir, "track.mp3", None);
        assert_eq!(resolve(None, Some(&file)), Some(Tier::Sibling(beside)));

        let bare = folder("bare");
        let alone = media(&bare, "track.mp3", None);
        assert_eq!(resolve(None, Some(&alone)), None);
        assert_eq!(resolve(Some(""), Some(&alone)), None);
        assert_eq!(resolve(None, None), None);
    }

    /// A remote track reads no picture and no cover, because both tiers need a
    /// local file.
    #[test]
    fn a_remote_track_reads_no_picture_and_no_cover() {
        let dir = folder("remote");
        cover(&dir, "cover.jpg");
        assert_eq!(resolve(None, Some("https://media.example/track.mp3")), None);
    }

    /// A `file://` URI names the path it holds.
    #[test]
    fn a_file_uri_reads_the_picture_the_path_holds() {
        let dir = folder("uri");
        let file = media(&dir, "track.mp3", Some(png(8, 8)));
        assert_eq!(
            resolve(None, Some(&format!("file://{file}"))),
            Some(Tier::Embedded(file))
        );
    }

    /// An album is one EDL item, so its first segment carries the cover for
    /// the whole timeline.
    #[test]
    fn an_album_reads_the_picture_in_its_first_segment() {
        let dir = folder("album");
        let first = media(&dir, "one.mp3", Some(png(8, 8)));
        media(&dir, "two.mp3", None);
        let album = timeline_file(&dir, &["one.mp3", "two.mp3"]);
        assert_eq!(resolve(None, Some(&album)), Some(Tier::Embedded(first)));
    }

    /// An album whose first segment embeds no picture reads the cover beside
    /// that segment.
    #[test]
    fn an_album_with_no_picture_reads_the_cover_beside_its_first_segment() {
        let dir = folder("albumcover");
        let beside = cover(&dir, "folder.jpg");
        media(&dir, "one.mp3", None);
        let album = timeline_file(&dir, &["one.mp3"]);
        assert_eq!(resolve(None, Some(&album)), Some(Tier::Sibling(beside)));
    }

    /// The `edl://` form carries the timeline in the URL, with a semicolon
    /// where the newline would be.
    #[test]
    fn the_edl_url_form_reads_its_first_segment() {
        let dir = folder("inline");
        let first = media(&dir, "one.mp3", Some(png(8, 8)));
        let second = media(&dir, "two.mp3", None);
        let url = format!("edl://{first},title=Oh No;{second},title=Masterswarm");
        assert_eq!(resolve(None, Some(&url)), Some(Tier::Embedded(first)));
    }

    /// A relative segment path is measured from the timeline's own directory,
    /// and an absolute path or a URL stands on its own.
    #[test]
    fn a_segment_path_is_measured_from_the_timeline() {
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

    /// The first segment is the first line that names a file. The header, the
    /// comments, the directives, and a line the reader cannot read are
    /// skipped.
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

    /// A file that is not a timeline is read as the media file it names: one
    /// with another extension, and one whose text carries no header.
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

    /// The four cover names are tried in order, and the first that exists
    /// wins.
    #[test]
    fn the_sibling_cover_order_is_fixed() {
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
                sibling_cover(&dir.join("track.mp3").to_string_lossy()),
                want.map(|name| dir.join(name).to_string_lossy().to_string()),
                "{present:?}"
            );
        }
    }

    /// A candidate that is a directory is no cover.
    #[test]
    fn a_directory_named_like_a_cover_is_no_cover() {
        let dir = folder("directory");
        std::fs::create_dir(dir.join("cover.jpg")).expect("a directory to skip");
        let beside = cover(&dir, "folder.jpg");
        assert_eq!(
            sibling_cover(&dir.join("track.mp3").to_string_lossy()),
            Some(beside)
        );
    }

    /// The block's reference and a cover beside the track open the way a logo
    /// opens, and a file that is not there reads nothing.
    #[test]
    fn every_tier_reads_the_bytes_it_names() {
        let dir = folder("reads");
        let beside = cover(&dir, "cover.jpg");
        assert_eq!(read(&Tier::Sibling(beside.clone())), Some(png(8, 8)));
        assert_eq!(read(&Tier::Block(beside)), Some(png(8, 8)));
        assert_eq!(read(&Tier::Block("/art/nothing.png".to_string())), None);
        assert_eq!(read(&Tier::Embedded("/art/nothing.mp3".to_string())), None);
    }
}
