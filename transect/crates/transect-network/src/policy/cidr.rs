//! Address blocks in CIDR notation, as a `NetworkPolicy`'s `ipBlock`
//! names them, and whether a block contains an address.

use std::net::IpAddr;

/// A block of addresses: its network, as the top `bits` of `network`,
/// in IPv4 or IPv6.  An IPv4 block never contains an IPv6 address, and
/// an IPv6 block never contains an IPv4 address.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Block {
    network: u128,
    bits: u32,
    v4: bool,
}

impl Block {
    /// Parses `10.0.0.0/8` or `fd00::/8`.  A bare address is a block of
    /// one.  Text the API server would reject parses to `None`, which
    /// matches nothing.
    pub fn parse(text: &str) -> Option<Self> {
        let (address, bits) = match text.split_once('/') {
            Some((address, bits)) => (address, Some(bits.parse::<u32>().ok()?)),
            None => (text, None),
        };
        let (network, width, v4) = match address.parse::<IpAddr>().ok()? {
            IpAddr::V4(v4) => (u128::from(u32::from(v4)), 32, true),
            IpAddr::V6(v6) => (u128::from(v6), 128, false),
        };
        let bits = bits.unwrap_or(width);
        (bits <= width).then(|| Self {
            network: mask(network, bits, width),
            bits,
            v4,
        })
    }

    pub fn contains(&self, address: IpAddr) -> bool {
        let (value, width, v4) = match address {
            IpAddr::V4(v4) => (u128::from(u32::from(v4)), 32, true),
            IpAddr::V6(v6) => (u128::from(v6), 128, false),
        };
        v4 == self.v4 && mask(value, self.bits, width) == self.network
    }
}

/// `value` with all but its top `bits` of `width` cleared.
fn mask(value: u128, bits: u32, width: u32) -> u128 {
    if bits == 0 {
        return 0;
    }
    let shift = width - bits;
    (value >> shift) << shift
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::inside("10.0.0.0/8", "10.7.1.7", true)]
    #[case::outside("10.0.0.0/8", "11.0.0.1", false)]
    #[case::host_bits_ignored("10.7.1.9/16", "10.7.200.1", true)]
    #[case::everything("0.0.0.0/0", "203.0.113.4", true)]
    #[case::one_address("192.0.2.1/32", "192.0.2.1", true)]
    #[case::bare_address("192.0.2.1", "192.0.2.2", false)]
    #[case::v6_inside("fd00::/8", "fd12::1", true)]
    #[case::v6_outside("fd00::/8", "fe80::1", false)]
    #[case::v4_block_and_v6_address("0.0.0.0/0", "::1", false)]
    #[case::v6_block_and_v4_address("::/0", "10.0.0.1", false)]
    fn holds_an_address(#[case] block: &str, #[case] address: &str, #[case] expected: bool) {
        let block = Block::parse(block).unwrap();
        assert_eq!(block.contains(address.parse().unwrap()), expected);
    }

    #[rstest]
    #[case::too_many_bits("10.0.0.0/33")]
    #[case::not_an_address("example.com/8")]
    #[case::not_a_number("10.0.0.0/x")]
    fn rejects_what_the_api_rejects(#[case] text: &str) {
        assert_eq!(Block::parse(text), None);
    }
}
