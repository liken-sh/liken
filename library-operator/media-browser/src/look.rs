// The liken look in one place. Every view reads its colors from here, so a
// change to a color lands in one file.

use iced_winit::core::Color;

// The ground the client fills. It is the clear color of every frame and every
// capture.
pub const BACKGROUND: Color = rgb(0x00, 0x00, 0x00);

/// The palette, from the brand theme's tokens. `TEXT` is --ink, `FILL` is the
/// dark-scheme --link, `TRACK` is the light-scheme --link, and `MUTED` is
/// --ink-muted.
pub const TEXT: Color = rgb(0xE8, 0xE8, 0xE8);
pub const FILL: Color = rgb(0xB4, 0xC4, 0x9A);
pub const TRACK: Color = rgb(0x4A, 0x5D, 0x3A);
pub const MUTED: Color = rgb(0xA0, 0xA6, 0xAD);

const fn rgb(r: u8, g: u8, b: u8) -> Color {
    Color {
        r: r as f32 / 255.0,
        g: g as f32 / 255.0,
        b: b as f32 / 255.0,
        a: 1.0,
    }
}

/// The same color at another opacity, for an element that fades on its own.
pub const fn faded(color: Color, alpha: f32) -> Color {
    Color { a: alpha, ..color }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_ground_is_black() {
        assert_eq!(BACKGROUND, Color::from_rgb(0.0, 0.0, 0.0));
    }

    #[test]
    fn fading_keeps_the_color() {
        let half = faded(FILL, 0.5);
        assert_eq!((half.r, half.g, half.b), (FILL.r, FILL.g, FILL.b));
        assert_eq!(half.a, 0.5);
    }
}
