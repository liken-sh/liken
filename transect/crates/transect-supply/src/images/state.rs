//! Which images to read next, and what came of each read.
//!
//! transect waits before it tries a failed read again, and the wait grows
//! after each failure.  So an image that a registry no longer serves
//! costs one request an hour, not one per change of the cluster.  A
//! failure of the whole registry delays the reads of every image that the
//! registry serves, so a registry that is down costs one request per
//! backoff, not one per image.
//!
//! transect forgets the result for a digest that no pod runs, so the
//! state contains the images the cluster runs, not every image it ran.

use std::collections::hash_map::Entry as MapEntry;
use std::collections::{HashMap, HashSet};
use std::sync::Arc;
use std::time::{Duration, Instant};

use super::{Failure, Known, Lookup, Provenance, Want};

/// The wait after an image's first failure, which doubles after each
/// one after it, up to `MAX_WAIT`.
const IMAGE_WAIT: Duration = Duration::from_secs(60);
/// The wait after a registry's first failure.
const REGISTRY_WAIT: Duration = Duration::from_secs(300);
const MAX_WAIT: Duration = Duration::from_secs(3600);

#[derive(Debug, Default)]
pub struct State {
    digests: HashMap<String, Entry>,
    registries: HashMap<String, Down>,
}

#[derive(Debug)]
enum Entry {
    /// Being read, after `failures` failed reads in a row.
    Reading {
        failures: u32,
    },
    Read(Arc<Provenance>),
    Failed {
        /// The registry the image was read from.  A change of that
        /// registry's mirror makes the next read go to another address.
        registry: String,
        failure: Failure,
        failures: u32,
        retry: Instant,
    },
}

#[derive(Debug)]
struct Down {
    failure: Failure,
    failures: u32,
    retry: Instant,
}

/// The wait after `failures` failures in a row, from `first`.
fn backoff(first: Duration, failures: u32) -> Duration {
    let doublings = failures.saturating_sub(1).min(16);
    (first * 2u32.pow(doublings)).min(MAX_WAIT)
}

impl State {
    /// The images in `wanted` to read now, each once: those not read
    /// yet, and those whose wait after a failure is over, unless their
    /// registry's wait is not over.  This function marks each image that
    /// it returns as being read.
    pub fn due(&mut self, wanted: impl IntoIterator<Item = Want>, now: Instant) -> Vec<Want> {
        let mut due = Vec::new();
        for want in wanted {
            let held = self
                .registries
                .get(&want.image.registry)
                .is_some_and(|down| now < down.retry);
            if held {
                continue;
            }
            let ready = match self.digests.entry(want.image.digest.clone()) {
                MapEntry::Vacant(vacant) => {
                    vacant.insert(Entry::Reading { failures: 0 });
                    true
                }
                MapEntry::Occupied(mut occupied) => match occupied.get() {
                    Entry::Failed {
                        retry, failures, ..
                    } if now >= *retry => {
                        let failures = *failures;
                        occupied.insert(Entry::Reading { failures });
                        true
                    }
                    _ => false,
                },
            };
            if ready {
                due.push(want);
            }
        }
        due
    }

    /// Whether `want`'s registry went down while the read waited for its
    /// turn.  When it did, this function drops the read and forgets the
    /// image, so transect reads it again when the registry's wait is
    /// over.  A registry that is down then costs only the reads that were
    /// already in progress.
    pub fn drop_if_held(&mut self, want: &Want, now: Instant) -> bool {
        let held = self
            .registries
            .get(&want.image.registry)
            .is_some_and(|down| now < down.retry);
        if held {
            self.digests.remove(&want.image.digest);
        }
        held
    }

    /// Records the result of reading `want`.  It returns the registry's
    /// failure when this read is the one that found the registry down,
    /// so the caller writes a log line only at that moment.
    pub fn land(
        &mut self,
        want: &Want,
        result: Result<Provenance, Failure>,
        now: Instant,
    ) -> Option<Failure> {
        let digest = want.image.digest.clone();
        let registry = &want.image.registry;
        match result {
            Ok(provenance) => {
                self.registries.remove(registry);
                self.digests
                    .insert(digest, Entry::Read(Arc::new(provenance)));
                None
            }
            Err(failure) if failure.registry_wide() => {
                // Forget the image, so transect reads it again as soon as
                // its registry's wait is over.
                self.digests.remove(&digest);
                let held = self
                    .registries
                    .get(registry)
                    .is_some_and(|down| now < down.retry);
                if held {
                    return None;
                }
                let failures = self
                    .registries
                    .get(registry)
                    .map_or(0, |down| down.failures)
                    + 1;
                let down = Down {
                    failure: failure.clone(),
                    failures,
                    retry: now + backoff(REGISTRY_WAIT, failures),
                };
                self.registries.insert(registry.clone(), down);
                Some(failure)
            }
            Err(failure) => {
                self.registries.remove(registry);
                let failures = match self.digests.get(&digest) {
                    Some(Entry::Failed { failures, .. } | Entry::Reading { failures }) => {
                        failures + 1
                    }
                    _ => 1,
                };
                let retry = now + backoff(IMAGE_WAIT, failures);
                self.digests.insert(
                    digest,
                    Entry::Failed {
                        registry: registry.clone(),
                        failure,
                        failures,
                        retry,
                    },
                );
                None
            }
        }
    }

    pub fn known(&self) -> Known {
        let digests = self.digests.iter().map(|(digest, entry)| {
            let lookup = match entry {
                Entry::Reading { .. } => Lookup::Reading,
                Entry::Read(provenance) => Lookup::Read(provenance.clone()),
                Entry::Failed { failure, .. } => Lookup::Failed(failure.clone()),
            };
            (digest.clone(), lookup)
        });
        let registries = self
            .registries
            .iter()
            .map(|(name, down)| (name.clone(), down.failure.clone()));
        Known {
            digests: digests.collect(),
            registries: registries.collect(),
            ..Known::default()
        }
    }

    /// Forgets every failure of `registry` and of the images it serves,
    /// so transect reads them again now, for example when the registry's
    /// mirror changes.  Labels that transect has read stay, because a
    /// digest names the same content wherever transect reads it from.
    pub fn forget(&mut self, registry: &str) {
        self.registries.remove(registry);
        self.digests.retain(|_, entry| {
            !matches!(entry, Entry::Failed { registry: failed, .. } if failed == registry)
        });
    }

    /// Forgets every digest that is not in `wanted`, except a digest that
    /// is being read.  That read's result is recorded, and the next call
    /// forgets it.
    pub fn keep_only<'a>(&mut self, wanted: impl IntoIterator<Item = &'a str>) {
        let wanted: HashSet<&str> = wanted.into_iter().collect();
        self.digests.retain(|digest, entry| {
            matches!(entry, Entry::Reading { .. }) || wanted.contains(digest.as_str())
        });
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::reference::fixtures::digest;
    use super::super::{ImageRef, Platform};
    use super::*;

    fn want(registry: &str, c: char) -> Want {
        Want {
            image: ImageRef {
                registry: registry.into(),
                repository: "team/app".into(),
                digest: digest(c),
            },
            platform: Platform::fallback(),
        }
    }

    fn digests(wants: &[Want]) -> Vec<String> {
        wants.iter().map(|want| want.image.digest.clone()).collect()
    }

    const MINUTE: Duration = Duration::from_secs(60);

    #[rstest]
    fn reads_each_image_once() {
        let mut state = State::default();
        let now = Instant::now();
        let first = state.due(
            [want("registry.example", 'a'), want("registry.example", 'a')],
            now,
        );
        let again = state.due([want("registry.example", 'a')], now);
        assert_eq!(
            (digests(&first), digests(&again)),
            (vec![digest('a')], vec![])
        );
    }

    #[rstest]
    #[case::before_the_wait_ends(1, MINUTE / 2, false)]
    #[case::after_the_first_wait(1, MINUTE, true)]
    #[case::the_wait_doubles(2, MINUTE, false)]
    #[case::after_the_second_wait(2, MINUTE * 2, true)]
    fn tries_a_failed_image_again_after_its_wait(
        #[case] failures: usize,
        #[case] after: Duration,
        #[case] tried: bool,
    ) {
        let mut state = State::default();
        let mut now = Instant::now();
        let image = want("registry.example", 'a');
        for _ in 0..failures {
            now += MAX_WAIT;
            state.due([image.clone()], now);
            state.land(&image, Err(Failure::NotFound), now);
        }
        let due = state.due([image], now + after);
        assert_eq!(due.len() == 1, tried);
    }

    #[rstest]
    fn a_registry_down_holds_back_every_image_it_serves() {
        let mut state = State::default();
        let now = Instant::now();
        let (a, b) = (want("registry.example", 'a'), want("registry.example", 'b'));
        let other = want("other.example", 'c');
        state.due([a.clone()], now);
        state.land(&a, Err(Failure::Unreachable), now);
        let held = state.due([a.clone(), b.clone(), other], now + MINUTE);
        let later = state.due([a, b], now + REGISTRY_WAIT);
        assert_eq!(
            (digests(&held), digests(&later)),
            (vec![digest('c')], vec![digest('a'), digest('b')])
        );
    }

    #[rstest]
    fn drops_a_waiting_read_once_its_registry_is_down() {
        let mut state = State::default();
        let now = Instant::now();
        let (a, b) = (want("registry.example", 'a'), want("registry.example", 'b'));
        state.due([a.clone(), b.clone()], now);
        let before = state.drop_if_held(&b, now);
        state.land(&a, Err(Failure::Unreachable), now);
        let after = state.drop_if_held(&b, now);
        let known = state.known().lookup(&b.image);
        assert_eq!(
            (before, after, known),
            (false, true, Lookup::Failed(Failure::Unreachable))
        );
    }

    #[rstest]
    fn reports_a_registry_down_once() {
        let mut state = State::default();
        let now = Instant::now();
        let (a, b) = (want("registry.example", 'a'), want("registry.example", 'b'));
        state.due([a.clone(), b.clone()], now);
        let first = state.land(&a, Err(Failure::NotARegistry), now);
        let second = state.land(&b, Err(Failure::NotARegistry), now);
        assert_eq!((first, second), (Some(Failure::NotARegistry), None));
    }

    #[rstest]
    fn knows_what_each_read_gave() {
        let mut state = State::default();
        let now = Instant::now();
        let (read, failed, waiting) = (
            want("registry.example", 'a'),
            want("registry.example", 'b'),
            want("registry.example", 'c'),
        );
        let down = want("down.example", 'd');
        state.due(
            [read.clone(), failed.clone(), waiting.clone(), down.clone()],
            now,
        );
        state.land(&read, Ok(Provenance::default()), now);
        state.land(&failed, Err(Failure::NotFound), now);
        state.land(&down, Err(Failure::Unreachable), now);
        let known = state.known();
        let found = [&read, &failed, &waiting, &down].map(|want| known.lookup(&want.image));
        assert_eq!(
            found,
            [
                Lookup::Read(Arc::default()),
                Lookup::Failed(Failure::NotFound),
                Lookup::Reading,
                Lookup::Failed(Failure::Unreachable),
            ]
        );
    }

    #[rstest]
    #[case::an_image_it_failed(want("registry.example", 'a'), true)]
    #[case::an_image_of_another_registry(want("other.example", 'b'), false)]
    fn a_mirror_change_reads_its_registrys_failed_images_again(
        #[case] image: Want,
        #[case] tried: bool,
    ) {
        let mut state = State::default();
        let now = Instant::now();
        state.due([image.clone()], now);
        state.land(&image, Err(Failure::NeedsCredentials), now);
        state.forget("registry.example");
        let due = state.due([image], now + MINUTE / 2);
        assert_eq!(due.len() == 1, tried);
    }

    #[rstest]
    fn a_mirror_change_keeps_what_was_read() {
        let mut state = State::default();
        let now = Instant::now();
        let image = want("registry.example", 'a');
        state.due([image.clone()], now);
        state.land(&image, Ok(Provenance::default()), now);
        state.forget("registry.example");
        let found = (
            state.due([image.clone()], now),
            state.known().lookup(&image.image),
        );
        assert_eq!(found, (vec![], Lookup::Read(Arc::default())));
    }

    #[rstest]
    fn forgets_what_no_pod_runs_any_more() {
        let mut state = State::default();
        let now = Instant::now();
        let (read, failed, gone, reading) = (
            want("registry.example", 'a'),
            want("registry.example", 'b'),
            want("registry.example", 'c'),
            want("registry.example", 'd'),
        );
        state.due(
            [read.clone(), failed.clone(), gone.clone(), reading.clone()],
            now,
        );
        state.land(&read, Ok(Provenance::default()), now);
        state.land(&failed, Err(Failure::NotFound), now);
        state.land(&gone, Ok(Provenance::default()), now);
        let kept = [digest('a'), digest('b')];
        state.keep_only(kept.iter().map(String::as_str));
        let mut held: Vec<String> = state.digests.keys().cloned().collect();
        held.sort();
        assert_eq!(held, vec![digest('a'), digest('b'), digest('d')]);
    }

    #[rstest]
    #[case::first(1, MINUTE)]
    #[case::second(2, MINUTE * 2)]
    #[case::capped(30, MAX_WAIT)]
    fn waits_longer_after_each_failure(#[case] failures: u32, #[case] wait: Duration) {
        assert_eq!(backoff(IMAGE_WAIT, failures), wait);
    }
}
