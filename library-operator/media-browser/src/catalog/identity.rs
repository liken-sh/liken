// The work a play is recorded against: the ids the providers know it by,
// and for an episode the two aired numbers. A path names a file, and a
// rename or a 4K upgrade is a new file and the same work, so the identity
// is the aliases and never the path.

use std::collections::BTreeMap;

/// One work's identity: its id at each provider, and for an episode its
/// season and episode numbers. The numbers are 0 for everything else.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Identity {
    /// The work's id at each provider, keyed by the provider name the
    /// catalog's alias carries.
    pub aliases: BTreeMap<String, String>,
    /// The aired season number, or 0 where the work is not an episode.
    pub season: i64,
    /// The aired episode number, or 0 where the work is not an episode.
    pub episode: i64,
}

impl Identity {
    /// Fold one catalog alias in under its provider. An alias is written
    /// `<kind>:<provider>:<rest>`; the rest is the id and keeps every colon
    /// it carries. A provider already held stands, so one read of an item's
    /// aliases yields one id per provider. A name that is not an alias is
    /// dropped.
    pub fn add(&mut self, alias: &str) {
        let mut parts = alias.splitn(3, ':');
        let (Some(_), Some(provider), Some(id)) = (parts.next(), parts.next(), parts.next()) else {
            return;
        };

        self.aliases
            .entry(provider.to_string())
            .or_insert_with(|| id.to_string());
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn identity(aliases: &[&str]) -> Identity {
        let mut identity = Identity::default();
        for alias in aliases {
            identity.add(alias);
        }
        identity
    }

    #[test]
    fn an_alias_is_the_providers_id_for_the_work() {
        assert_eq!(
            identity(&["movie:tmdb:603", "movie:imdb:tt0133093"]).aliases,
            BTreeMap::from([
                ("tmdb".to_string(), "603".to_string()),
                ("imdb".to_string(), "tt0133093".to_string()),
            ])
        );
    }

    #[test]
    fn the_path_a_folder_fell_back_to_is_an_alias_like_any_other() {
        assert_eq!(
            identity(&["movie:path:some-film-1999"]).aliases,
            BTreeMap::from([("path".to_string(), "some-film-1999".to_string())])
        );
    }

    #[test]
    fn an_id_keeps_every_colon_after_the_provider() {
        assert_eq!(
            identity(&["series:tvdb:81189:2"]).aliases,
            BTreeMap::from([("tvdb".to_string(), "81189:2".to_string())])
        );
    }

    #[test]
    fn the_first_id_a_provider_named_stands() {
        assert_eq!(
            identity(&["movie:tmdb:603", "movie:tmdb:604"]).aliases,
            BTreeMap::from([("tmdb".to_string(), "603".to_string())])
        );
    }

    #[test]
    fn a_name_that_is_no_alias_is_dropped() {
        assert_eq!(identity(&["some-film-1999"]).aliases, BTreeMap::new());
    }

    #[test]
    fn a_work_that_is_no_episode_carries_no_numbers() {
        let identity = identity(&["movie:tmdb:603"]);

        assert_eq!((identity.season, identity.episode), (0, 0));
    }
}
