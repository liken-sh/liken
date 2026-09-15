// The column of circles between the metro strip and the cards, which
// says where in the story the room and each person in it stand. A
// circle is the initial the audience screen draws for a person, because
// the browser holds no picture of anyone.

use iced_winit::core::Rectangle;

use super::progress::Marks;
use super::wall::{self, GAP, Heading};
use crate::views::area;
use crate::views::clock::strip::{CIRCLE, circle_in, circles_width};

/// The width the column takes: the widest stack it draws and the gap
/// before the cards, and nothing at all where no marker stands, the way
/// the time column takes room only where a row carries a time. The
/// room's stack is one circle per person present, and a solo circle is
/// one circle.
pub fn width(marks: &Marks) -> f32 {
    let stack = match marks.room {
        Some(_) => marks.letters.len(),
        None => 0,
    };
    let alone = usize::from(!marks.solo.is_empty());
    match stack.max(alone) {
        0 => 0.0,
        count => circles_width(count) + GAP,
    }
}

/// The box this many circles draw in on one row: at the left of the
/// column, on the middle line of the row's card.
pub fn row_box(column: Rectangle, cell: Rectangle, count: usize) -> Rectangle {
    area(
        column.x,
        cell.center_y() - CIRCLE / 2.0,
        circles_width(count),
        CIRCLE,
    )
}

/// Every circle the column draws in this frame, as the letter on it and
/// the box it draws in: the room's stack on the row its thread stands
/// on, and one circle on the row of every person whose own thread stands
/// on another row. A stack and a solo circle never share a row, because
/// a person on the room's row draws no solo circle.
pub fn drawn(
    column: Rectangle,
    marks: &Marks,
    headings: &[Heading],
    tops: &[f32],
    down: f32,
) -> Vec<(String, Rectangle)> {
    let at = |row: usize, count: usize| {
        row_box(
            column,
            wall::cell_box(column, row, headings, tops, down),
            count,
        )
    };
    let stack = marks.room.into_iter().flat_map(|row| {
        let stack = at(row, marks.letters.len());
        marks
            .letters
            .iter()
            .enumerate()
            .map(move |(index, letter)| (letter.clone(), circle_in(stack, index)))
    });
    let alone = marks
        .solo
        .iter()
        .map(|(letter, row)| (letter.clone(), circle_in(at(*row, 1), 0)));
    stack.chain(alone).collect()
}

#[cfg(test)]
mod tests;
