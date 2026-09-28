# 48, Larger type on banners and headings

Built. The brand theme's base font size moved from 16px to 18px in
display-operator commits `9157a60` and `57aacb9` on 2026-08-25, and
display-operator's `0cfb4c9` set `scale=2` on every output whose mode
is 3840 wide or wider. Together, the two changes give banners,
headings, and franchise cards the larger type the plan asks for, at
the brand level, so every site and screen that takes the theme moved
together.

The wall captions, search results, and two-line cards kept their
size, because the step is in the brand theme's scale, not in the
individual elements.
