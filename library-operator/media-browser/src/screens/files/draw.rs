// The files section on the canvas: the heading, then the blocks in two
// columns, each a name over a rule, a picture line, and its groups of rows.
// The measures are the model's, so the page places the section by the
// height the model answers.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Color, Font, Point};

use super::{
    Block, COLUMN_GAP, COLUMNS, Files, GROUP_PAD, Group, HEADING, HEADING_GAP, MOST_WIDTH, NAME,
    OVER_PICTURE, ROW, ROW_GAP, ROW_SPACE, RULE, Row, SMALL, UNDER_NAME, UNDER_PICTURE,
};
use crate::look;
use crate::views::text;

// The column the dot draws in, before a row's language and a block's name.
const DOT_COLUMN: f32 = 18.0;
const DOT: f32 = 4.0;
// The column of a group's label, and the space after it.
const LABEL_COLUMN: f32 = 120.0;
const LABEL_GAP: f32 = 16.0;
// The column of a row's language.
const LANGUAGE_COLUMN: f32 = 130.0;
// The opacity of the rule between two groups, under the rule under a name,
// so the groups read as parts of one block.
const GROUP_RULE: f32 = 0.6;
// The opacity of a name or a language that does not play, and of the
// detail of a track that does, so the dotted row reads first.
const RESTING: f32 = 0.7;
const PLAYING_DETAIL: f32 = 0.86;

/// Draw the section with its heading's top left at `at`, in this width.
pub fn draw(frame: &mut canvas::Frame<Renderer>, files: &Files, at: Point, width: f32) {
    if files.blocks.is_empty() {
        return;
    }
    text::line(frame, HEADING, at, look::HEADING, look::muted(), width);
    let column = (width.min(MOST_WIDTH) - COLUMN_GAP) / COLUMNS as f32;
    let mut top = at.y + text::height(1, look::HEADING) + HEADING_GAP;
    for pair in files.blocks.chunks(COLUMNS) {
        for (index, block) in pair.iter().enumerate() {
            let left = at.x + index as f32 * (column + COLUMN_GAP);
            draw_block(frame, block, Point::new(left, top), column);
        }
        top += pair.iter().map(Block::height).fold(0.0, f32::max) + ROW_GAP;
    }
}

fn draw_block(frame: &mut canvas::Frame<Renderer>, block: &Block, at: Point, width: f32) {
    let right = at.x + width;
    let mut y = at.y;
    let name_at = match block.plays {
        true => {
            dot(frame, Point::new(at.x, y), NAME);
            at.x + DOT_COLUMN
        }
        false => at.x,
    };
    let name_color = match block.plays {
        true => look::text(),
        false => dimmed(look::text(), RESTING),
    };
    text::line(
        frame,
        &block.name,
        Point::new(name_at, y),
        NAME,
        name_color,
        right - name_at,
    );
    // The size sits on the name's baseline, at the right edge.
    right_aligned(
        frame,
        &block.size,
        right,
        y + (NAME - SMALL),
        regular(SMALL),
        look::faint(),
    );
    y += text::height(1, NAME) + UNDER_NAME;
    rule(frame, at.x, y, width, look::rule());
    y += RULE + OVER_PICTURE;
    text::line(
        frame,
        &block.picture,
        Point::new(at.x, y),
        SMALL,
        look::faint(),
        width,
    );
    y += text::height(1, SMALL) + UNDER_PICTURE;
    for group in &block.groups {
        draw_group(frame, group, Point::new(at.x, y), width);
        y += group.height();
    }
}

fn draw_group(frame: &mut canvas::Frame<Renderer>, group: &Group, at: Point, width: f32) {
    let mut y = at.y + GROUP_PAD;
    let lift = ROW - SMALL;
    text::line(
        frame,
        group.label,
        Point::new(at.x, y + lift),
        SMALL,
        look::muted(),
        LABEL_COLUMN,
    );
    let left = at.x + LABEL_COLUMN + LABEL_GAP;
    for row in &group.rows {
        draw_row(frame, row, Point::new(left, y), at.x + width - left);
        y += text::height(1, ROW) + ROW_SPACE;
    }
    let under = at.y + group.height() - RULE;
    rule(frame, at.x, under, width, dimmed(look::rule(), GROUP_RULE));
}

fn draw_row(frame: &mut canvas::Frame<Renderer>, row: &Row, at: Point, width: f32) {
    if row.plays {
        dot(frame, at, ROW);
    }
    let (language, detail) = match row.plays {
        true => (look::text(), dimmed(look::text(), PLAYING_DETAIL)),
        false => (dimmed(look::text(), RESTING), look::faint()),
    };
    let language_at = at.x + DOT_COLUMN;
    text::line(
        frame,
        &row.language,
        Point::new(language_at, at.y),
        ROW,
        language,
        LANGUAGE_COLUMN,
    );
    let detail_at = language_at + LANGUAGE_COLUMN;
    let right = at.x + width;
    text::line(
        frame,
        &row.detail,
        Point::new(detail_at, at.y),
        ROW,
        detail,
        right - detail_at - LABEL_COLUMN,
    );
    right_aligned(
        frame,
        row.role,
        right,
        at.y + (ROW - SMALL),
        (SMALL, look::ITALIC),
        look::muted(),
    );
}

// The accent's dot, centered on a line of this size whose top is at `at`.
fn dot(frame: &mut canvas::Frame<Renderer>, at: Point, size: f32) {
    let center = Point::new(at.x + DOT, at.y + text::height(1, size) / 2.0);
    frame.fill(&canvas::Path::circle(center, DOT), look::accent());
}

fn rule(frame: &mut canvas::Frame<Renderer>, x: f32, y: f32, width: f32, color: Color) {
    frame.fill_rectangle(
        Point::new(x, y),
        iced_winit::core::Size::new(width, RULE),
        color,
    );
}

// One line whose right edge is at `right`, measured by the shaper, so it
// ends on the column's edge whatever its glyphs.
fn right_aligned(
    frame: &mut canvas::Frame<Renderer>,
    content: &str,
    right: f32,
    y: f32,
    (size, font): (f32, Font),
    color: Color,
) {
    if content.is_empty() {
        return;
    }
    let width = text::measured(content, size);
    text::line_in(
        frame,
        content,
        Point::new(right - width, y),
        (size, font),
        color,
        width + size,
    );
}

fn regular(size: f32) -> (f32, Font) {
    (size, Font::with_name(look::FONT))
}

fn dimmed(color: Color, alpha: f32) -> Color {
    Color {
        a: color.a * alpha,
        ..color
    }
}
