// The lines the browser prints for what a person does: a press and what
// it did, a play request and where it went, a choice of who is watching,
// and what the bus confirmed. Every such line goes through `Log`, so a
// test reads the lines the browser printed instead of the process's
// stderr. Machine-scale work, such as a frame, a read, or a decode,
// prints nothing here.
//
// A line names a title only by its catalog id, and `opaque` hashes the
// ids that carry a folder name or a title in them. The pod log leaves
// the house, and a catalog of what a household owns must not leave with
// it.

use std::cell::RefCell;
use std::rc::Rc;

use sha2::{Digest, Sha256};

/// Where the browser's lines go: stderr, with the program's name in
/// front, and a list a test reads as well where one is kept.
#[derive(Default)]
pub struct Log {
    kept: Option<Rc<RefCell<Vec<String>>>>,
}

impl Log {
    /// Print one line.
    pub fn line(&self, text: impl Into<String>) {
        let text = text.into();
        eprintln!("media-browser: {text}");
        if let Some(kept) = &self.kept {
            kept.borrow_mut().push(text);
        }
    }

    /// A log that also keeps every line in the list it answers, which a
    /// test reads.
    pub fn kept() -> (Self, Rc<RefCell<Vec<String>>>) {
        let kept = Rc::new(RefCell::new(Vec::new()));
        (
            Self {
                kept: Some(kept.clone()),
            },
            kept,
        )
    }
}

// The providers whose value is a folder name or a title and not an
// opaque number: `path` is the scanner's fallback when no provider names
// the title, and `name` is a set's or a franchise's own name.
const NAMED: [&str; 2] = ["path", "name"];

// How many hex characters of the hash stand in for the named part. Twelve
// keep two titles of one library apart and fit on a log line.
const HASHED: usize = 12;

/// The catalog id as a log line may carry it. An id of the form
/// `kind:provider:value` passes through, because the value is a
/// provider's number. An id whose provider is `path` or `name` carries a
/// folder name or a title, so the part after the provider becomes the
/// first twelve hex characters of its SHA-256. The operator hashes the
/// same way, so one id reads the same in both logs.
pub fn opaque(id: &str) -> String {
    let mut parts = id.splitn(3, ':');
    let (Some(kind), Some(provider), Some(tail)) = (parts.next(), parts.next(), parts.next())
    else {
        return id.to_string();
    };
    if !NAMED.contains(&provider) {
        return id.to_string();
    }
    format!("{kind}:{provider}:{}", hashed(tail))
}

/// The first twelve hex characters of the SHA-256 of a name, for a thing
/// the catalog names only by a folder or a title, such as a person's
/// entry.
pub fn hashed(name: &str) -> String {
    let digest = Sha256::digest(name.as_bytes());
    digest
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect::<String>()[..HASHED]
        .to_string()
}

#[cfg(test)]
mod tests {
    use super::*;

    // The tails are invented and neutral, so a test names no real title.
    const IDS: [(&str, &str); 6] = [
        ("movie:tmdb:1001", "movie:tmdb:1001"),
        ("episode:tvdb:2002:s01e02", "episode:tvdb:2002:s01e02"),
        ("movie:path:Film A (2001)", "movie:path:"),
        ("franchise:name:saga-a", "franchise:name:"),
        ("set:name:films", "set:name:"),
        ("movies:1", "movies:1"),
    ];

    #[test]
    fn an_id_keeps_a_provider_number_and_hashes_a_name() {
        for (id, kept) in IDS {
            let logged = opaque(id);
            assert!(logged.starts_with(kept), "{id} logged as {logged}");
            if logged != id {
                assert_eq!(logged.len(), kept.len() + HASHED, "{id}");
                assert!(!logged.contains(&id[kept.len()..]), "{id}");
            }
        }
    }

    #[test]
    fn one_name_hashes_to_one_value() {
        assert_eq!(hashed("Film A (2001)"), hashed("Film A (2001)"));
        assert_ne!(hashed("Film A (2001)"), hashed("Film B (2002)"));
        assert_eq!(hashed("Film A (2001)").len(), HASHED);
    }

    #[test]
    fn a_kept_log_holds_each_line_once() {
        let (log, kept) = Log::kept();
        log.line("one");
        log.line(String::from("two"));
        assert_eq!(*kept.borrow(), ["one", "two"]);
    }
}
