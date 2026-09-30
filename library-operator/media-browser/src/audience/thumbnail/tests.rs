// The `data:` URI a person's thumbnail arrives in: what opens, and what
// leaves the person with their letter.

use super::*;

#[test]
fn a_jpeg_uri_opens_to_its_bytes() {
    let thumbnail = Thumbnail::from_uri("data:image/jpeg;base64,/9j/4A==").expect("a thumbnail");

    assert_eq!(thumbnail.format(), ImageFormat::Jpeg);
    assert_eq!(thumbnail.bytes(), [0xff, 0xd8, 0xff, 0xe0]);
}

#[test]
fn a_png_uri_opens_to_its_bytes() {
    let thumbnail = Thumbnail::from_uri("data:image/png;base64,iVBORw==").expect("a thumbnail");

    assert_eq!(thumbnail.format(), ImageFormat::Png);
    assert_eq!(thumbnail.bytes(), [0x89, b'P', b'N', b'G']);
}

// Each of these is text a person's entry might carry that the browser
// cannot draw, so the person keeps their letter.
#[test]
fn a_uri_the_browser_cannot_draw_opens_to_nothing() {
    let cases = [
        ("empty", ""),
        ("not a data URI", "https://example.com/ada.jpg"),
        ("no comma", "data:image/jpeg;base64"),
        ("not base64", "data:image/jpeg,/9j/4A=="),
        ("broken base64", "data:image/jpeg;base64,/9j/4A=!"),
        ("no bytes", "data:image/jpeg;base64,"),
        (
            "a format the browser does not build",
            "data:image/webp;base64,UklGRg==",
        ),
        ("not an image", "data:text/plain;base64,aGVsbG8="),
    ];

    for (case, uri) in cases {
        assert_eq!(Thumbnail::from_uri(uri), None, "{case}: {uri}");
    }
}

#[test]
fn a_media_type_matches_in_any_case() {
    let thumbnail = Thumbnail::from_uri("data:Image/JPEG;base64,/9j/4A==").expect("a thumbnail");

    assert_eq!(thumbnail.format(), ImageFormat::Jpeg);
}
