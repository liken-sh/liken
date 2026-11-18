# The H.264 level is too low for 60 fps

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`h264Codecs` in `routes.go` gives a clip up to 1920x1080 at 60 fps
level 4.1 (`avc1.640029`), and a larger or faster clip level 5.1
(`avc1.640033`). `h264Profile` passes the same level to `h264_vaapi`
with `-level`, and the `.mp4` route's description in the OpenAPI
document states the same split. By a reading of Table A-1 of the H.264
standard, level 4.1 allows 1080p at about 30 fps and level 5.1 allows
2160p at about 30 fps. So a clip at 1080p60 or at 2160p60 declares a
level that its frame rate exceeds. A strict decoder, or a browser that
checks the `codecs` parameter, can refuse the stream or decode it with
too little buffer.

## Evidence

Table A-1, as read and not measured:

| Level | MaxMBPS (macroblocks per second) | MaxFS (macroblocks per frame) |
| --- | --- | --- |
| 4.1 | 245760 | 8192 |
| 4.2 | 522240 | 8704 |
| 5.1 | 983040 | 36864 |
| 5.2 | 2073600 | 36864 |

A 1920x1080 frame is 8160 macroblocks, so level 4.1 allows
245760 / 8160 = 30.1 fps. A 3840x2160 frame is 32400 macroblocks, so
level 5.1 allows 983040 / 32400 = 30.3 fps.

## What would confirm it

Check the table in the current edition of ITU-T H.264. Then capture a
1080p60 clip and read the stream's level and rate with `ffprobe`, and
play it in a browser that reads `codecs`.

## What a fix has to decide

The thresholds become a choice among levels 4.1, 4.2, 5.1, and 5.2 by
frame size and rate. The change alters the `Content-Type` of existing
routes, the OpenAPI description, the reference pages `api.md` and
`routes.md`, and the tests that name `avc1.640029`, including
`media-operator/apicompose_test.go`.
