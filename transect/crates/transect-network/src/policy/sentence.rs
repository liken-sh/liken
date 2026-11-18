//! Display text for a set of ports, as the policy card in `card.rs`
//! shows it: "any port", "port 5432", "ports 80 and 443",
//! "ports 8000–8010", "port 53/UDP". The protocol appears only when it
//! is not TCP, the default.

use std::fmt;

use super::ports::{DEFAULT_PROTOCOL, Ports, Span};

impl fmt::Display for Ports {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let spans = match self {
            Self::Any => return f.write_str("any port"),
            Self::Listed(spans) => spans,
        };
        let texts: Vec<String> = spans.iter().map(Span::text).collect();
        let single = matches!(spans.as_slice(), [span] if span.first == span.last);
        let noun = if single { "port" } else { "ports" };
        match texts.as_slice() {
            [] => f.write_str("no port"),
            [one] => write!(f, "{noun} {one}"),
            [one, two] => write!(f, "{noun} {one} and {two}"),
            [rest @ .., last] => write!(f, "{noun} {}, and {last}", rest.join(", ")),
        }
    }
}

impl Span {
    fn text(&self) -> String {
        let numbers = if self.first == self.last {
            self.first.to_string()
        } else {
            format!("{}–{}", self.first, self.last)
        };
        if self.protocol == DEFAULT_PROTOCOL {
            numbers
        } else {
            format!("{numbers}/{}", self.protocol)
        }
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::ports::fixtures::{listed, tcp};
    use super::*;

    #[rstest]
    #[case::any(Ports::Any, "any port")]
    #[case::one(listed(&[tcp(5432, 5432)]), "port 5432")]
    #[case::a_range(listed(&[tcp(8000, 8010)]), "ports 8000–8010")]
    #[case::two(listed(&[tcp(80, 80), tcp(443, 443)]), "ports 80 and 443")]
    #[case::three(listed(&[tcp(80, 80), tcp(443, 443), tcp(8080, 8080)]), "ports 80, 443, and 8080")]
    #[case::udp(listed(&[Span::new("UDP", 53, 53)]), "port 53/UDP")]
    fn formats_ports(#[case] ports: Ports, #[case] expected: &str) {
        assert_eq!(ports.to_string(), expected);
    }
}
