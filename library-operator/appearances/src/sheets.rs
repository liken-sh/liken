// Contact sheets for a review: for each person, a grid of the faces the
// match named as that person, with the weakest similarity first, because
// a wrong name shows up at the bottom of the scores. One more sheet holds
// the largest faces the match named as nobody, which is where recall is
// lost. Each sheet has a text file beside it that gives each tile's time,
// similarity, and the next closest person's similarity, row by row.
//
// The crops come from the video again, one ffmpeg seek for each sample
// a sheet uses, because the detections record holds no pixels.

use std::collections::{BTreeMap, BTreeSet};
use std::fs;
use std::io::Read;
use std::path::Path;
use std::process::{Command, Stdio};

use crate::picture::Picture;
use crate::runtime::Error;

pub const TILE: usize = 128;
pub const COLUMNS: usize = 8;

// One face to put on a sheet: where it is and what to say about it.
#[derive(Clone, Debug, PartialEq)]
pub struct Tile {
    pub time: f64,
    pub x: f32,
    pub y: f32,
    pub w: f32,
    pub h: f32,
    pub similarity: f32,
    pub runner_up: Option<f32>,
    pub person: String,
}

// A tile's crop: the face's box with a quarter of its size added on each
// side, so the hair and the chin show, kept square.
fn crop(frame: &Picture, tile: &Tile) -> Picture {
    let side = tile.w.max(tile.h) * 1.5;
    let left = tile.x + tile.w / 2.0 - side / 2.0;
    let top = tile.y + tile.h / 2.0 - side / 2.0;
    let scale = TILE as f32 / side;
    let transform = crate::picture::Similarity {
        a: scale,
        b: 0.0,
        tx: -left * scale,
        ty: -top * scale,
    };
    frame.warp(&transform, TILE, TILE)
}

fn frame_at(video: &Path, time: f64, width: usize, height: usize) -> Result<Picture, Error> {
    let mut child = Command::new("ffmpeg")
        .args(["-hide_banner", "-nostdin", "-loglevel", "error"])
        .args(["-ss", &format!("{time:.6}"), "-i"])
        .arg(video)
        .args(["-frames:v", "1", "-vf", &format!("scale={width}:{height}")])
        .args(["-pix_fmt", "bgr24", "-f", "rawvideo", "-"])
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()?;
    let mut bgr = vec![0u8; width * height * 3];
    let read = child
        .stdout
        .take()
        .ok_or("ffmpeg has no stdout")?
        .read_exact(&mut bgr);
    let output = child.wait_with_output()?;
    if let Err(e) = read {
        return Err(format!(
            "ffmpeg wrote no frame at {time}: {e}: {}",
            String::from_utf8_lossy(&output.stderr)
        )
        .into());
    }
    Ok(Picture::new(width, height, bgr))
}

// Lays the tiles out in rows of COLUMNS, in order.
pub fn grid(tiles: &[Picture]) -> Picture {
    let rows = tiles.len().div_ceil(COLUMNS).max(1);
    let width = COLUMNS * TILE;
    let mut bgr = vec![0u8; width * rows * TILE * 3];
    for (index, tile) in tiles.iter().enumerate() {
        let (col, row) = (index % COLUMNS, index / COLUMNS);
        for y in 0..TILE {
            let target = ((row * TILE + y) * width + col * TILE) * 3;
            let source = y * TILE * 3;
            bgr[target..target + TILE * 3].copy_from_slice(&tile.bgr[source..source + TILE * 3]);
        }
    }
    Picture::new(width, rows * TILE, bgr)
}

fn save_png(picture: &Picture, path: &Path) -> Result<(), Error> {
    let mut rgb = picture.bgr.clone();
    for pixel in rgb.as_chunks_mut::<3>().0 {
        pixel.swap(0, 2);
    }
    image::save_buffer(
        path,
        &rgb,
        picture.width as u32,
        picture.height as u32,
        image::ExtendedColorType::Rgb8,
    )?;
    Ok(())
}

// Writes one sheet for each named group of tiles into `out`. The groups
// are already ordered and cut to the length a sheet should show.
pub fn write(
    video: &Path,
    width: usize,
    height: usize,
    groups: &BTreeMap<String, Vec<Tile>>,
    out: &Path,
) -> Result<(), Error> {
    fs::create_dir_all(out)?;
    let times: BTreeSet<u64> = groups
        .values()
        .flatten()
        .map(|t| t.time.to_bits())
        .collect();
    let mut frames = BTreeMap::new();
    for bits in times {
        frames.insert(bits, frame_at(video, f64::from_bits(bits), width, height)?);
    }
    for (name, tiles) in groups {
        let crops: Vec<Picture> = tiles
            .iter()
            .map(|t| crop(&frames[&t.time.to_bits()], t))
            .collect();
        save_png(&grid(&crops), &out.join(format!("{name}.png")))?;
        let mut index = String::new();
        for (i, t) in tiles.iter().enumerate() {
            let at = format!(
                "{}:{:02}:{:04.1}",
                (t.time / 3600.0) as u64,
                (t.time % 3600.0 / 60.0) as u64,
                t.time % 60.0
            );
            let runner_up = t
                .runner_up
                .map_or("-----".to_string(), |r| format!("{r:.3}"));
            index.push_str(&format!(
                "row {} col {}  {at}  {:.3}  next {runner_up}  {}  {}px\n",
                i / COLUMNS + 1,
                i % COLUMNS + 1,
                t.similarity,
                t.person,
                t.h.round()
            ));
        }
        fs::write(out.join(format!("{name}.txt")), index)?;
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn solid(value: u8) -> Picture {
        Picture::new(TILE, TILE, vec![value; TILE * TILE * 3])
    }

    #[test]
    fn grid_wraps_after_a_full_row() {
        let tiles: Vec<_> = (0..COLUMNS + 1).map(|i| solid(i as u8 + 1)).collect();
        let sheet = grid(&tiles);
        assert_eq!((sheet.width, sheet.height), (COLUMNS * TILE, 2 * TILE));
        // The ninth tile starts the second row.
        assert_eq!(sheet.bgr[TILE * sheet.width * 3], COLUMNS as u8 + 1);
    }

    #[test]
    fn crop_centers_the_face_in_its_tile() {
        let mut bgr = vec![0u8; 400 * 400 * 3];
        // A white face box from (100, 100) to (200, 200).
        for y in 100..200 {
            for x in 100..200 {
                bgr[(y * 400 + x) * 3..(y * 400 + x) * 3 + 3].copy_from_slice(&[255; 3]);
            }
        }
        let tile = Tile {
            time: 0.0,
            x: 100.0,
            y: 100.0,
            w: 100.0,
            h: 100.0,
            similarity: 0.5,
            runner_up: None,
            person: "A".into(),
        };
        let cropped = crop(&Picture::new(400, 400, bgr), &tile);
        let center = (TILE / 2 * TILE + TILE / 2) * 3;
        assert_eq!((cropped.bgr[center], cropped.bgr[0]), (255, 0));
    }
}
