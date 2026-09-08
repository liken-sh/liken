-- The up-next offer: the work the Play names as the one that follows it.
-- The command sidecar sends the block as the next script-message. This module
-- draws it as a chip while the film plays and as a card past ninety percent,
-- and focus routes a select on it.
local theme = require("theme")
local utils = require("mp.utils")

local upnext = {}

-- The card's box in canvas pixels. It is 440 wide with its right edge on the
-- margin, and its bottom edge stays above the row where the scrubber draws
-- the time label, so the card never covers the scrubber.
local CARD_W = 440
local CARD_TOP = 380
local ART_H = 248
local PAD_X = 22
local PAD_Y = 16
local CARD_R = 14
-- The unfocused card's fill, dark enough to read the three lines over a
-- bright frame. The focused card takes the chooser panel.
local CARD_ALPHA = "&H54&"
-- The drop from each line of the card to the next, at the type size each
-- line draws in.
local REASON_PITCH = 40
local TITLE_PITCH = 54
local DETAIL_H = 42
local CARD_H = ART_H + PAD_Y + REASON_PITCH + TITLE_PITCH + DETAIL_H + PAD_Y

-- The chip's row, above the right end of the bar and above the time label.
local CHIP_Y = 806
local CHIP_H = 34
local CHIP_PAD_X = 22
local CHIP_PAD_Y = 6

-- The tab that remains at the screen edge after the card fades.
local SLIVER_W = 22

-- The percent the card rises at. It is the same line the library's progress
-- store counts a play as finished at, so the card rises when the current
-- work counts as watched.
local RISE_PERCENT = 90

-- The card draws at this fraction of its alpha while it waits for the next
-- work to start.
local WAIT_FADE = 0.6

-- The words the chip and the waiting card draw. The block carries the three
-- lines of the card and nothing for these two states, so they are spelled
-- here.
local CHIP_WORD = "UP NEXT"
local WAIT_WORD = "Starting"

-- The overlay id the art draws on. The logo draws on 1, the trickplay tile
-- on 2, and the album cover on 3, in overlay-add's own numbering.
local OVERLAY_ID = 4

local ART_REQUEST = "liken-art-request"
local ART_KIND = "next"

local redraw_cb = function() end
function upnext.set_redraw(fn)
  redraw_cb = fn
end

-- The block the sidecar sent, or nil when the Play names no next work.
local offer = nil
-- Whether the playhead has crossed the rise, whether a select has asked for
-- the next work, and the card's own fade while the OSD is down.
local risen = false
-- Whether a select on the chip has grown it into the card. The rise does the
-- same on its own. This state lasts only until the focus leaves the stop or
-- the OSD hides.
local expanded = false
local waiting = false
local fade = 0
local fade_target = 0
local fade_timer = nil
local hide_timer = nil
-- The last playlist position mpv reported. A change of item clears the
-- offer, and the first report is not a change.
local at_item = nil
-- The decoded art, the pixel box the display asked for, and the overlay
-- that is on screen.
local blob = nil
local want = nil
local placed = nil

local function right()
  return theme.canvas.w - theme.margin.x
end

local function card_x()
  return right() - CARD_W
end

-- libass reports no text width to a script, so the panel behind the chip and
-- the clip of a long line add up an estimate. A glyph counts as one of three
-- widths, as a fraction of the type size: a capital, a space, and everything
-- else. The numbers were measured off the brand face at the chip's own size,
-- and they fit a mixed-case line to a few pixels.
local UPPER_W = 0.46
local SPACE_W = 0.16
local OTHER_W = 0.385

-- The estimated width of one glyph, by the class its lead byte names. A
-- multibyte glyph counts as the other class.
local function glyph_w(b, size)
  if b == 32 then
    return size * SPACE_W
  end
  if b >= 65 and b <= 90 then
    return size * UPPER_W
  end
  return size * OTHER_W
end

-- The estimated width of one line, in canvas pixels. A continuation byte is
-- part of the glyph its lead byte started, so only a lead byte adds width.
local function text_w(s, size)
  local w = 0
  for i = 1, #s do
    local b = string.byte(s, i)
    if b < 0x80 or b >= 0xC0 then
      w = w + glyph_w(b, size)
    end
  end
  return w
end

-- clip drops the glyphs a line has no room for and marks the cut with an
-- ellipsis, so a long title stays inside the card.
local function clip(s, size, max_w)
  if text_w(s, size) <= max_w then
    return s
  end
  local room = max_w - size * OTHER_W
  local w = 0
  local out = {}
  for i = 1, #s do
    local b = string.byte(s, i)
    if b < 0x80 or b >= 0xC0 then
      local glyph = glyph_w(b, size)
      if w + glyph > room then
        break
      end
      w = w + glyph
    end
    out[#out + 1] = string.sub(s, i, i)
  end
  return table.concat(out) .. "\226\128\166"
end

-- The fade the card runs while the OSD is down, at the same rates the OSD
-- and the volume indicator fade at, so the three read the same.
local function fade_step()
  local rate_ms = theme.fade_out_ms
  if fade_target > fade then
    rate_ms = theme.fade_in_ms
  end
  local step = theme.fade_tick * 1000 / rate_ms
  if fade_target > fade then
    fade = math.min(fade_target, fade + step)
  else
    fade = math.max(fade_target, fade - step)
  end
  redraw_cb()
  if fade == fade_target and fade_timer then
    fade_timer:kill()
    fade_timer = nil
  end
end

local function start_fade(target)
  fade_target = target
  if fade == fade_target then
    return
  end
  if not fade_timer then
    fade_timer = mp.add_periodic_timer(theme.fade_tick, fade_step)
  end
end

local function cancel_hide()
  if hide_timer then
    hide_timer:kill()
    hide_timer = nil
  end
end

local function clear_overlay()
  if placed then
    mp.command_native({ "overlay-remove", OVERLAY_ID })
    placed = nil
  end
end

-- A new offer, or the loss of one, drops every state the last one had.
local function reset()
  risen = false
  expanded = false
  waiting = false
  fade = 0
  fade_target = 0
  if fade_timer then
    fade_timer:kill()
    fade_timer = nil
  end
  cancel_hide()
  blob = nil
  want = nil
  clear_overlay()
end

-- receive takes the block as one JSON string. A block with none of the three
-- lines and no art is not an offer, so an empty object clears the current
-- one.
function upnext.receive(text)
  local parsed = nil
  if text and text ~= "" then
    parsed = utils.parse_json(text)
  end
  reset()
  offer = nil
  if type(parsed) == "table" then
    if parsed.reason or parsed.title or parsed.detail or parsed.art then
      offer = parsed
    end
  end
  redraw_cb()
end

-- The offer is for the item the Play started on, so a move to another item
-- drops it. mpv reports the position once when the script observes it, and
-- that first report names the item already playing, not a move.
function upnext.on_playlist_pos(value)
  if type(value) ~= "number" then
    return
  end
  if at_item ~= nil and value ~= at_item and offer then
    reset()
    offer = nil
    redraw_cb()
  end
  at_item = value
end

-- mpv sends percent-pos on every change, so the card rises from the property
-- and the display runs no timer to watch for the crossing. The rise holds
-- after it happens, so a seek back does not take the card down.
function upnext.on_percent(value)
  if type(value) ~= "number" or not offer or risen then
    return
  end
  if value < RISE_PERCENT then
    return
  end
  risen = true
  start_fade(1)
  cancel_hide()
  hide_timer = mp.add_timeout(theme.idle_hide, function()
    hide_timer = nil
    start_fade(0)
  end)
  redraw_cb()
end

-- The stop is present only while the Play carries an offer.
function upnext.available()
  return offer ~= nil
end

function upnext.waiting()
  return waiting
end

-- The card is what a select acts on, so the display grows the chip into it
-- first and takes the offer on the press after that.
function upnext.showing_card()
  return risen or expanded
end

function upnext.expand()
  expanded = true
  redraw_cb()
end

-- The offer falls back to the chip when the focus leaves the stop or the OSD
-- hides, so a summon shows the small form again until the rise.
function upnext.collapse()
  if not expanded then
    return
  end
  expanded = false
  redraw_cb()
end

-- take records that focus broadcast the ask. The card then waits for the
-- operator to end this Play and start the next one. Nothing here ends the
-- wait: the film ends, or a back press ends the run.
function upnext.take()
  if not offer then
    return
  end
  waiting = true
  cancel_hide()
  redraw_cb()
end

local function chip(focused)
  local label = CHIP_WORD .. "  " .. (offer.title or "")
  local parts = {}
  local color = theme.color.muted
  if focused then
    local w = text_w(label, theme.type.tiny) + 2 * CHIP_PAD_X
    parts[#parts + 1] = theme.panel(
      right() + CHIP_PAD_X - w, CHIP_Y - CHIP_PAD_Y, w, CHIP_H + 2 * CHIP_PAD_Y
    )
    color = theme.color.text
  end
  parts[#parts + 1] = theme.text(right(), CHIP_Y, label, theme.type.tiny, color, 9)
  return table.concat(parts, "\n")
end

-- The card: the art over the three lines the block spells. detail is the
-- third line, because the wait replaces it with a word of its own.
local function card(focused, detail, detail_color)
  local x = card_x()
  local text_w = CARD_W - 2 * PAD_X
  local parts = {}
  if focused then
    parts[#parts + 1] = theme.panel(x, CARD_TOP, CARD_W, CARD_H)
  else
    parts[#parts + 1] = theme.rounded_rect(
      x, CARD_TOP, CARD_W, CARD_H, CARD_R, theme.color.shadow, CARD_ALPHA
    )
  end
  parts[#parts + 1] = theme.rect(x, CARD_TOP, CARD_W, ART_H, theme.color.shadow, theme.alpha.panel)
  local y = CARD_TOP + ART_H + PAD_Y
  if offer.reason then
    parts[#parts + 1] = theme.text(
      x + PAD_X, y, clip(string.upper(offer.reason), theme.type.tiny, text_w),
      theme.type.tiny, theme.color.muted, 7
    )
  end
  y = y + REASON_PITCH
  if offer.title then
    parts[#parts + 1] = theme.text(
      x + PAD_X, y, clip(offer.title, theme.type.label, text_w),
      theme.type.label, theme.color.text, 7
    )
  end
  y = y + TITLE_PITCH
  if detail then
    parts[#parts + 1] = theme.text(
      x + PAD_X, y, clip(detail, theme.type.small, text_w),
      theme.type.small, detail_color, 7
    )
  end
  return table.concat(parts, "\n")
end

local function sliver()
  return theme.rect(
    theme.canvas.w - SLIVER_W, CARD_TOP, SLIVER_W, CARD_H,
    theme.color.fill, theme.alpha.subdued
  )
end

-- draw returns the offer as part of the OSD, the chip before the rise and the
-- card after it, at the OSD's own fade. The waiting card is not part of the
-- OSD, so it does not fade out with it.
function upnext.draw(focused)
  if not offer or waiting then
    return nil
  end
  if risen or expanded then
    return card(focused, offer.detail, theme.color.muted)
  end
  return chip(focused)
end

-- draw_outside returns what the offer draws over the bare video, on a clock
-- of its own: the risen card for its few seconds, then the sliver, and the
-- dimmed card for the whole wait. Before the rise, a hidden OSD draws
-- nothing for the offer.
function upnext.draw_outside(osd_visible)
  if not offer then
    return nil
  end
  local outer = theme.fade
  local out = nil
  if waiting then
    theme.set_fade(WAIT_FADE)
    out = card(false, WAIT_WORD, theme.color.fill)
  elseif osd_visible or not risen then
    return nil
  elseif fade > 0 then
    theme.set_fade(fade)
    out = card(false, offer.detail, theme.color.muted)
  else
    theme.set_fade(1)
    out = sliver()
  end
  theme.set_fade(outer)
  return out
end

-- The art is on screen only while the card is: while the OSD is up after
-- the rise, while the card shows itself, and for the whole wait.
local function card_on_screen(visible)
  if waiting then
    return true
  end
  if visible and (risen or expanded) then
    return true
  end
  if not risen then
    return false
  end
  return fade > 0
end

-- request asks the bridge for the art at the pixel size the art box takes on
-- this screen, once per size. The bridge fits the picture inside the box and
-- keeps its ratio, so a poster comes back letterboxed.
local function request(m)
  local w = math.floor(CARD_W * m.sx + 0.5)
  local h = math.floor(ART_H * m.sy + 0.5)
  if w <= 0 or h <= 0 then
    return
  end
  local key = w .. "x" .. h
  if key == want then
    return
  end
  want = key
  mp.command_native({ "script-message", ART_REQUEST, ART_KIND, tostring(w), tostring(h) })
end

-- on_art receives one decoded picture from the bridge. An empty path is the
-- bridge's answer for an offer whose art it could not read.
function upnext.on_art(kind, path, w, h, stride)
  if kind ~= ART_KIND or not offer then
    return
  end
  if not path or path == "" then
    blob = nil
    clear_overlay()
    redraw_cb()
    return
  end
  blob = { path = path, w = tonumber(w), h = tonumber(h), stride = tonumber(stride) }
  redraw_cb()
end

-- on_resize asks for the art again at the new pixel size. The old bitmap
-- stays on screen until the new one arrives.
function upnext.on_resize()
  want = nil
end

-- sync places the art in the middle of the card's art box, in real pixels,
-- because overlay-add does no scaling of its own.
function upnext.sync(visible)
  if not offer or not offer.art then
    clear_overlay()
    return
  end
  local m = theme.osd_scale()
  if not m then
    return
  end
  request(m)
  if not blob or not card_on_screen(visible) then
    clear_overlay()
    return
  end
  local x = math.floor((card_x() + CARD_W / 2) * m.sx + 0.5) - math.floor(blob.w / 2)
  local y = math.floor((CARD_TOP + ART_H / 2) * m.sy + 0.5) - math.floor(blob.h / 2)
  x = math.max(0, x)
  y = math.max(0, y)
  local sig = table.concat({ blob.path, x, y, blob.w, blob.h, blob.stride }, ":")
  if sig ~= placed then
    mp.command_native({ "overlay-add", OVERLAY_ID, x, y, blob.path, 0, "bgra", blob.w, blob.h, blob.stride })
    placed = sig
  end
end

return upnext
