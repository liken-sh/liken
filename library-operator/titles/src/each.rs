// Runs `work` on each item on `threads` threads and returns the results in
// the items' order. `train` reads the readings of its labels with it, and
// `place` those of its videos: one reading's signals take no part of
// another's, and they are most of the time of both commands.

use std::thread;

pub fn each<I: Sync, T: Send>(
    items: &[I],
    threads: usize,
    work: impl Fn(&I) -> T + Sync,
) -> Vec<T> {
    let chunk = items.len().div_ceil(threads.max(1)).max(1);
    thread::scope(|scope| {
        let parts: Vec<_> = items
            .chunks(chunk)
            .map(|part| scope.spawn(|| part.iter().map(&work).collect::<Vec<T>>()))
            .collect();
        parts
            .into_iter()
            .flat_map(|part| part.join().expect("a reading thread panicked"))
            .collect()
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn each_keeps_the_order_of_the_items() {
        let items: Vec<usize> = (0..10).collect();
        assert_eq!(
            each(&items, 3, |item| item * 2),
            each(&items, 1, |item| item * 2)
        );
        assert_eq!(each(&items, 3, |item| item * 2)[9], 18);
    }

    #[test]
    fn each_takes_no_item_and_more_threads_than_items() {
        assert_eq!(each(&[] as &[usize], 4, |item| *item), Vec::<usize>::new());
        assert_eq!(each(&[7], 4, |item| *item), vec![7]);
    }
}
