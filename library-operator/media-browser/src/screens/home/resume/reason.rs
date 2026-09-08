// The reasons a card of the continue-watching row carries: one per
// container that offers its leaf, stacked in the order resume, series,
// set, franchise, and spelled with the container's title. A press follows
// the first of them.

use crate::screens::facts;

// One reason a leaf is in the row. `Resume` carries the series title for
// an episode and nothing for a film, so every thread that resumes one leaf
// spells the same reason once. `Franchise` carries what a press needs to
// open the franchise page on the member.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Reason {
    Resume {
        series: String,
    },
    Series(String),
    Set(String),
    Franchise {
        library: String,
        id: String,
        position: i64,
        title: String,
    },
}

impl Reason {
    // The place of the reason in the stack.
    fn rank(&self) -> u8 {
        match self {
            Self::Resume { .. } => 0,
            Self::Series(_) => 1,
            Self::Set(_) => 2,
            Self::Franchise { .. } => 3,
        }
    }

    // The words one reason reads as: "Resume", "Resume · The Office", or
    // "Next in" and the container's title.
    fn spelled(&self) -> String {
        match self {
            Self::Resume { series } => facts::joined(&["Resume", series]),
            Self::Series(title) | Self::Set(title) | Self::Franchise { title, .. } => {
                format!("Next in {title}")
            }
        }
    }
}

// The reasons of one card in their order, each once.
pub fn stacked(reasons: &mut Vec<Reason>) {
    reasons.sort_by_key(Reason::rank);
    reasons.dedup();
}

// The second line of the card: every reason, with dots between.
pub fn line(reasons: &[Reason]) -> String {
    let spelled: Vec<String> = reasons.iter().map(Reason::spelled).collect();
    let parts: Vec<&str> = spelled.iter().map(String::as_str).collect();
    facts::joined(&parts)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn mcu() -> Reason {
        Reason::Franchise {
            library: "screening/orders".into(),
            id: "franchise:name:mcu".into(),
            position: 4,
            title: "the MCU".into(),
        }
    }

    #[test]
    fn a_series_thread_that_resumes_names_the_show_and_a_film_does_not() {
        assert_eq!(
            line(&[Reason::Resume {
                series: "The Office".into()
            }]),
            "Resume · The Office"
        );
        assert_eq!(
            line(&[Reason::Resume {
                series: String::new()
            }]),
            "Resume"
        );
    }

    #[test]
    fn the_reasons_stack_in_their_order_whatever_order_they_came_in() {
        let mut reasons = vec![
            mcu(),
            Reason::Set("Iron Man Collection".into()),
            Reason::Series("WandaVision".into()),
            Reason::Resume {
                series: "WandaVision".into(),
            },
        ];
        stacked(&mut reasons);
        assert_eq!(
            line(&reasons),
            "Resume · WandaVision · Next in WandaVision · Next in Iron Man Collection · \
             Next in the MCU"
        );
    }

    #[test]
    fn two_threads_that_give_one_reason_spell_it_once() {
        let mut reasons = vec![
            Reason::Resume {
                series: "WandaVision".into(),
            },
            mcu(),
            Reason::Resume {
                series: "WandaVision".into(),
            },
            mcu(),
        ];
        stacked(&mut reasons);
        assert_eq!(line(&reasons), "Resume · WandaVision · Next in the MCU");
    }

    #[test]
    fn two_franchises_are_two_reasons() {
        let mut reasons = vec![
            mcu(),
            Reason::Franchise {
                library: "screening/orders".into(),
                id: "franchise:name:multiverse".into(),
                position: 1,
                title: "the Multiverse".into(),
            },
        ];
        stacked(&mut reasons);
        assert_eq!(line(&reasons), "Next in the MCU · Next in the Multiverse");
    }
}
