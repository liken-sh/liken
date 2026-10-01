// The people a title's faces can match: the actors that the credits fact
// lists in .liken/credits.yaml, each with the headshot the headshot fact
// put in their .contributors/ entry. The question the match answers is not
// who a face is among everyone alive, but which of these few people it is,
// if any, and that narrowness is what makes a small model enough.

use std::fs;
use std::path::{Path, PathBuf};

use serde::{Deserialize, Serialize};

use crate::picture::Picture;
use crate::record::Model;
use crate::runtime::{Detector, Embedder, Error};

#[derive(Deserialize)]
struct Credits {
    #[serde(default)]
    credits: Vec<Credit>,
}

#[derive(Clone, Debug, PartialEq, Deserialize)]
pub struct Credit {
    pub name: String,
    pub part: String,
    #[serde(default)]
    pub role: String,
    #[serde(default)]
    pub order: i64,
    #[serde(default)]
    pub contributor: String,
}

pub fn actors(credits_yaml: &str) -> Result<Vec<Credit>, Error> {
    let credits: Credits = serde_yaml_ng::from_str(credits_yaml)?;
    let mut actors: Vec<Credit> = credits
        .credits
        .into_iter()
        .filter(|c| c.part == "actor" && !c.contributor.is_empty())
        .collect();
    actors.sort_by_key(|c| c.order);
    Ok(actors)
}

// The library's root is the nearest folder at or above the title that
// holds .contributors/, because every credit names its entry by a path
// from there.
pub fn library_root(title: &Path) -> Option<PathBuf> {
    title
        .ancestors()
        .find(|dir| dir.join(".contributors").is_dir())
        .map(Path::to_path_buf)
}

#[derive(Clone, Copy, Debug, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum Headshot {
    // The headshot gave a face, and its vector is in the gallery.
    Found,
    // The entry has no headshot, so the person cannot match.
    Missing,
    // The headshot holds no face the detector found.
    NoFace,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Person {
    pub contributor: String,
    pub name: String,
    pub role: String,
    pub headshot: Headshot,
    // The SHA-256 of the headshot file, or None when the entry has none. A
    // reader compares it with the file to see that the headshot changed
    // and the gallery is stale.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub headshot_sha256: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub embedding: Option<String>,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Gallery {
    pub embedder: Model,
    pub people: Vec<Person>,
}

// The size the detector reads a headshot at. A headshot is a portrait
// with one large face, so 640 pixels finds it at any source size.
pub const HEADSHOT_SIDE: usize = 640;

fn headshot_file(entry: &Path) -> Option<PathBuf> {
    ["headshot.jpg", "headshot.png"]
        .iter()
        .map(|name| entry.join(name))
        .find(|path| path.is_file())
}

fn load(path: &Path) -> Result<Picture, Error> {
    let rgb = image::open(path)?.to_rgb8();
    let (width, height) = (rgb.width() as usize, rgb.height() as usize);
    let mut bgr = rgb.into_raw();
    for pixel in bgr.as_chunks_mut::<3>().0 {
        pixel.swap(0, 2);
    }
    Ok(Picture::new(width, height, bgr))
}

pub fn build(
    root: &Path,
    actors: &[Credit],
    embedder_model: &Model,
    detector: &mut Detector,
    embedder: &mut Embedder,
) -> Result<Gallery, Error> {
    let mut people = Vec::new();
    for actor in actors {
        let mut person = Person {
            contributor: actor.contributor.clone(),
            name: actor.name.clone(),
            role: actor.role.clone(),
            headshot: Headshot::Missing,
            headshot_sha256: None,
            embedding: None,
        };
        if let Some(file) = headshot_file(&root.join(&actor.contributor)) {
            person.headshot_sha256 = Some(crate::models::sha256(&file)?);
            let picture = load(&file)?;
            let fit = HEADSHOT_SIDE as f32 / picture.width.max(picture.height) as f32;
            let width = ((picture.width as f32 * fit) as usize).max(1);
            let height = ((picture.height as f32 * fit) as usize).max(1);
            let small = picture.resize(width, height);
            // The largest face is the subject. A headshot taken at an event
            // can hold a second person behind the first.
            let largest = detector
                .detect(&small)?
                .into_iter()
                .max_by(|a, b| (a.w * a.h).total_cmp(&(b.w * b.h)));
            person.headshot = Headshot::NoFace;
            if let Some(face) = largest {
                let vector = embedder.embed(&picture, &face.scaled(1.0 / fit))?;
                person.headshot = Headshot::Found;
                person.embedding = Some(crate::record::encode(&vector));
            }
        }
        people.push(person);
    }
    Ok(Gallery {
        embedder: embedder_model.clone(),
        people,
    })
}

// The credits file the credits fact writes in a title's own .liken/. An
// episode's folder holds none, because the credits fact credits the series,
// so the match of a season folder names the series' file with --credits.
pub fn credits_path(title: &Path) -> PathBuf {
    title.join(".liken").join("credits.yaml")
}

pub fn read_credits(path: &Path) -> Result<Vec<Credit>, Error> {
    let text = fs::read_to_string(path).map_err(|e| format!("{}: {e}", path.display()))?;
    actors(&text)
}

#[cfg(test)]
mod tests {
    use super::*;

    const CREDITS: &str = "
credits:
    - name: Ridley Scott
      part: director
      contributor: .contributors/ri/ridley-scott
    - name: Jessica Chastain
      part: actor
      role: Melissa Lewis
      order: 1
      contributor: .contributors/je/jessica-chastain
    - name: Matt Damon
      part: actor
      role: Mark Watney
      order: 0
      contributor: .contributors/ma/matt-damon
";

    #[test]
    fn actors_keeps_the_cast_in_billing_order() {
        let names: Vec<_> = actors(CREDITS)
            .unwrap()
            .into_iter()
            .map(|c| c.name)
            .collect();
        assert_eq!(names, ["Matt Damon", "Jessica Chastain"]);
    }

    #[test]
    fn library_root_finds_the_contributors_folder_above_the_title() {
        let root = std::env::temp_dir().join(format!("appearances-root-{}", std::process::id()));
        let title = root.join("Sci-Fi").join("Film [2015]");
        fs::create_dir_all(root.join(".contributors")).unwrap();
        fs::create_dir_all(&title).unwrap();
        assert_eq!(library_root(&title), Some(root));
    }
}
