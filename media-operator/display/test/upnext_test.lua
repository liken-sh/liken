-- The up-next offer: what display/upnext.lua draws for the block the sidecar
-- sends as the next script-message, and how display/focus.lua routes the
-- presses on it, against the fake mp table in display/test/mp.lua.

local here = arg[0]:match("^(.*)[/\\][^/\\]+$") or "."
package.path = here .. "/?.lua;" .. here .. "/../?.lua;" .. package.path

local harness = require("mp")

-- mpv decodes JSON in C, and the fake mp has no decoder. This one reads the
-- flat object of strings the sidecar sends and nothing more.
local function parse_json(text)
  local body = text:match("^%s*{(.*)}%s*$")
  if not body then
    return nil
  end
  local out = {}
  for key, value in body:gmatch('"([^"]+)"%s*:%s*"([^"]*)"') do
    out[key] = value
  end
  return out
end

-- Every display module a test loads. Each test clears them, so no test reads
-- another test's module state.
local DISPLAY = {
  "theme", "upnext", "focus", "scrubber", "strip", "images", "presentation",
  "audio", "subtitles", "video", "offset", "chooser",
}

local function load(properties)
  for _, name in ipairs(DISPLAY) do
    package.loaded[name] = nil
  end
  package.loaded["mp.utils"] = { parse_json = parse_json }
  _G.mp = harness.new(properties or { ["osd-dimensions"] = { w = 1920, h = 1080 } })
  return require("upnext"), _G.mp
end

local OFFER = '{"reason":"Next in Harbor Lights \194\183 S02",'
  .. '"title":"E05 \194\183 The Long Tide",'
  .. '"detail":"Harbor Lights \194\183 S02 \194\183 45 min"}'
local WITH_ART = '{"title":"E05 \194\183 The Long Tide","art":"/run/liken/art/next.bgra"}'

-- The canvas numbers the design gives: the right margin, the card box, and
-- the row the chip draws on.
local RIGHT = 1780
local CARD_X = 1340
local CARD_TOP = 380
local CARD_H = 416
local TEXT_X = 1362
local CHIP_Y = 806
local FLOOR = 820

local function has(text, want)
  assert(text and text:find(want, 1, true), "want " .. want .. " in:\n" .. tostring(text))
end

local function lacks(text, want)
  assert(not (text and text:find(want, 1, true)), "unwanted " .. want .. " in:\n" .. tostring(text))
end

-- The lowest row any part of a drawing reaches, so a test can hold the card
-- above the scrubber's time label.
local function lowest(text)
  local low = 0
  for y in text:gmatch("\\pos%([%d%.%-]+,([%d%.%-]+)%)") do
    low = math.max(low, tonumber(y))
  end
  return low
end

local function messages(fake, name)
  local out = {}
  for _, command in ipairs(fake.commands) do
    if command[1] == "script-message" and command[2] == name then
      out[#out + 1] = command
    end
  end
  return out
end

local function overlays(fake)
  local out = {}
  for _, command in ipairs(fake.commands) do
    if command[1] == "overlay-add" then
      out[#out + 1] = command
    end
  end
  return out
end

-- focus and the modules it routes to, loaded against the same fake mp table
-- the offer module uses.
local function load_focus(offer)
  local upnext, fake = load({ ["osd-dimensions"] = { w = 1920, h = 1080 }, duration = 100 })
  if offer then
    upnext.receive(offer)
  end
  return require("focus"), upnext, fake
end

-- The presses that take a risen offer: the rise, a summon, up to the stop,
-- and one select on the card.
local function take(focus, upnext)
  upnext.on_percent(91)
  focus.summon()
  focus.nav("up")
  focus.nav("select")
end

local tests = {}

local function test(name, fn)
  tests[#tests + 1] = { name = name, fn = fn }
end

test("no offer draws nothing", function()
  local upnext = load()

  assert(upnext.draw(false) == nil, "an offerless display drew inside the OSD")
  assert(upnext.draw_outside(false) == nil, "an offerless display drew over the video")
  assert(upnext.available() == false, "an offerless display offered a stop")
end)

test("an empty offer draws nothing", function()
  local upnext = load()

  upnext.receive("{}")

  assert(upnext.draw(false) == nil, "an empty offer drew inside the OSD")
  assert(upnext.available() == false, "an empty offer offered a stop")
end)

test("no offer has no stop and never sends liken-next", function()
  local focus, _, fake = load_focus(nil)

  focus.summon()
  focus.nav("up")
  focus.nav("select")

  assert(focus.focused_stop() == "fine", "up reached a stop above fine with no offer")
  assert(#messages(fake, "liken-next") == 0, "a select sent liken-next with no offer")
end)

test("the chip reads UP NEXT and the title, right aligned", function()
  local upnext = load()

  upnext.receive(OFFER)
  local ass = upnext.draw(false)

  has(ass, "UP NEXT  E05 \194\183 The Long Tide")
  has(ass, string.format("\\an9\\pos(%d.00,%d.00)", RIGHT, CHIP_Y))
  has(ass, "\\fs28")
  has(ass, "\\1c&HADA6A0&")
end)

test("the chip clears the time label row", function()
  local upnext = load()

  upnext.receive(OFFER)

  assert(lowest(upnext.draw(false)) + 34 <= 846, "the chip covers the time label")
end)

test("the focused chip takes the panel", function()
  local upnext = load()

  upnext.receive(OFFER)
  local rest = upnext.draw(false)
  local focused = upnext.draw(true)

  lacks(rest, "\\bord2\\3c&H9AC4B4&")
  has(focused, "\\bord2\\3c&H9AC4B4&")
  has(focused, "\\1c&HE8E8E8&")
end)

test("the card rises at ninety percent with the three lines", function()
  local upnext = load()

  upnext.receive(OFFER)
  upnext.on_percent(89)
  local before = upnext.draw(false)
  upnext.on_percent(90)
  local after = upnext.draw(false)

  lacks(before, "NEXT IN HARBOR LIGHTS \194\183 S02")
  has(after, string.format("\\pos(%d.00,644.00)", TEXT_X))
  has(after, "NEXT IN HARBOR LIGHTS \194\183 S02")
  has(after, string.format("\\pos(%d.00,684.00)", TEXT_X))
  has(after, "E05 \194\183 The Long Tide")
  has(after, string.format("\\pos(%d.00,738.00)", TEXT_X))
  has(after, "Harbor Lights \194\183 S02 \194\183 45 min")
end)

test("the card holds the comps' box", function()
  local upnext = load()

  upnext.receive(OFFER)
  upnext.on_percent(91)
  local ass = upnext.draw(false)

  has(ass, string.format("\\pos(%d.00,%d.00)", CARD_X, CARD_TOP))
  assert(CARD_X + 440 == RIGHT, "the card's right edge left the margin")
  assert(lowest(ass) + 34 <= FLOOR, "the card reaches below the scrubber")
end)

test("the unfocused card fills dark and the focused card takes the border", function()
  local upnext = load()

  upnext.receive(OFFER)
  upnext.on_percent(91)

  lacks(upnext.draw(false), "\\bord2\\3c&H9AC4B4&")
  has(upnext.draw(false), "\\1c&H000000&\\1a&H54&")
  has(upnext.draw(true), "\\bord2\\3c&H9AC4B4&")
end)

test("the card shows itself with the OSD down and leaves a sliver", function()
  local upnext, fake = load()

  upnext.receive(OFFER)
  assert(upnext.draw_outside(false) == nil, "the card showed before ninety percent")
  upnext.on_percent(91)
  fake.settle()
  local risen = upnext.draw_outside(false)
  assert(upnext.draw_outside(true) == nil, "the card drew twice while the OSD was up")
  fake.fire_timeout()
  fake.settle()
  local sliver = upnext.draw_outside(false)

  has(risen, "E05 \194\183 The Long Tide")
  has(risen, "\\1c&H000000&\\1a&H54&")
  lacks(sliver, "E05 \194\183 The Long Tide")
  has(sliver, "\\pos(1898.00,380.00)")
  has(sliver, string.format("m 0 0 l 22.00 0 l 22.00 %d.00 l 0 %d.00", CARD_H, CARD_H))
  has(sliver, "\\1c&H9AC4B4&")
  has(sliver, "\\1a&H80&")
  assert(CARD_TOP + CARD_H <= FLOOR, "the card reaches below the scrubber")
end)

test("the art request goes once at the art box and the reply is placed", function()
  local upnext, fake = load()

  upnext.receive(WITH_ART)
  upnext.sync(false)
  upnext.sync(false)
  local asked = messages(fake, "liken-art-request")
  upnext.on_art("next", "/run/liken/art/next.bgra", "300", "248", "1200")
  upnext.on_percent(91)
  upnext.sync(true)
  local placed = overlays(fake)

  assert(#asked == 1, string.format("sent %d art requests, want 1", #asked))
  assert(asked[1][3] == "next", "the request named another kind")
  assert(asked[1][4] == "440" and asked[1][5] == "248", "the request left the art box")
  assert(#placed == 1, string.format("placed %d overlays, want 1", #placed))
  assert(placed[1][3] == 1410 and placed[1][4] == 380, "the bitmap is off the art box's center")
end)

test("a replay replaces the offer and a new item clears it", function()
  local upnext = load()

  upnext.receive(OFFER)
  upnext.receive('{"title":"E06 The Turning"}')
  has(upnext.draw(false), "E06 The Turning")

  upnext.on_playlist_pos(0)
  assert(upnext.available(), "the first playlist position cleared the offer")
  upnext.on_playlist_pos(1)
  assert(upnext.available() == false, "a new item held the offer")
  assert(upnext.draw(false) == nil, "a new item left the offer on screen")
end)

test("up reaches the offer and down returns, and a summon lands on fine", function()
  local focus = load_focus(OFFER)

  focus.summon()
  local landed = focus.focused_stop()
  focus.nav("up")
  local up = focus.focused_stop()
  focus.nav("down")

  assert(landed == "fine", "a summon landed on " .. tostring(landed))
  assert(up == "next", "up landed on " .. tostring(up))
  assert(focus.focused_stop() == "fine", "down left the offer")
end)

test("left and right on the offer do nothing", function()
  local focus = load_focus(OFFER)
  local scrubber = require("scrubber")

  focus.summon()
  focus.nav("up")
  focus.nav("left")
  focus.nav("right")

  assert(focus.focused_stop() == "next", "a horizontal press left the offer")
  assert(scrubber.scanning() == false, "a horizontal press started a scan")
end)

test("back on the offer dismisses the OSD", function()
  local focus, _, fake = load_focus(OFFER)

  focus.summon()
  focus.nav("up")
  focus.nav("back")

  assert(focus.visible() == false, "back left the OSD up")
  assert(#messages(fake, "liken-exit") == 0, "back ended the run from the offer")
end)

test("select on the risen card sends liken-next and waits", function()
  local focus, upnext, fake = load_focus(OFFER)

  take(focus, upnext)

  assert(#messages(fake, "liken-next") == 1, "select sent no liken-next")
  assert(upnext.waiting(), "select did not enter waiting")
end)

test("select on the chip expands it and sends nothing", function()
  local focus, upnext, fake = load_focus(OFFER)

  focus.summon()
  focus.nav("up")
  local before = upnext.draw(true)
  focus.nav("select")
  local after = upnext.draw(true)

  has(before, "UP NEXT  E05 \194\183 The Long Tide")
  has(after, "NEXT IN HARBOR LIGHTS \194\183 S02")
  has(after, "Harbor Lights \194\183 S02 \194\183 45 min")
  assert(#messages(fake, "liken-next") == 0, "a select on the chip took the offer")
  assert(upnext.waiting() == false, "a select on the chip entered waiting")
  assert(focus.focused_stop() == "next", "the expansion moved the focus")
end)

test("a second select on the expanded card takes the offer", function()
  local focus, upnext, fake = load_focus(OFFER)

  focus.summon()
  focus.nav("up")
  focus.nav("select")
  focus.nav("select")

  assert(#messages(fake, "liken-next") == 1, "the second select sent no liken-next")
  assert(upnext.waiting(), "the second select did not enter waiting")
end)

test("down after an expansion returns to the chip", function()
  local focus, upnext = load_focus(OFFER)

  focus.summon()
  focus.nav("up")
  focus.nav("select")
  focus.nav("down")
  focus.nav("up")

  has(upnext.draw(true), "UP NEXT  E05 \194\183 The Long Tide")
end)

test("a hidden OSD returns to the chip", function()
  local focus, upnext = load_focus(OFFER)

  focus.summon()
  focus.nav("up")
  focus.nav("select")
  focus.dismiss()
  focus.summon()
  focus.nav("up")

  has(upnext.draw(true), "UP NEXT  E05 \194\183 The Long Tide")
end)

test("an expansion never draws over the bare video", function()
  local focus, upnext = load_focus(OFFER)

  focus.summon()
  focus.nav("up")
  focus.nav("select")

  assert(upnext.draw_outside(false) == nil, "the expanded card drew with the OSD down")
end)

test("the waiting card dims and reads Starting", function()
  local focus, upnext = load_focus(OFFER)

  take(focus, upnext)
  local ass = upnext.draw_outside(true)

  assert(upnext.draw(true) == nil, "the waiting card drew inside the OSD as well")
  has(ass, "Starting")
  lacks(ass, "Harbor Lights \194\183 S02 \194\183 45 min")
  has(ass, "E05 \194\183 The Long Tide")
  has(ass, "\\1c&H9AC4B4&")
end)

test("waiting swallows every press but back", function()
  local focus, upnext, fake = load_focus(OFFER)

  take(focus, upnext)
  local before = #fake.commands
  for _, action in ipairs({ "up", "down", "left", "right", "select" }) do
    focus.nav(action)
  end
  local after = #fake.commands
  focus.nav("back")

  assert(before == after, string.format("waiting ran %d commands", after - before))
  assert(focus.focused_stop() == "next", "a swallowed press moved the focus")
  assert(#messages(fake, "liken-exit") == 1, "back sent no liken-exit while waiting")
end)

test("waiting sends liken-exit from a hidden OSD as well", function()
  local focus, upnext, fake = load_focus(OFFER)

  take(focus, upnext)
  focus.dismiss()
  focus.nav("back")

  assert(#messages(fake, "liken-exit") == 1, "back sent no liken-exit while waiting")
end)

for _, each in ipairs(tests) do
  each.fn()
  io.write("ok  ", each.name, "\n")
end
