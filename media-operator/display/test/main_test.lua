-- What display/main.lua submits to mpv while a film plays, and the rounding in
-- display/scrubber.lua that holds the string still between two frames, against
-- the fake mp table in display/test/mp.lua.
--
-- mpv composites the whole OSD again on the video thread for every osd-overlay
-- command, in the frame path, so these tests count the commands rather than
-- read the pixels.

-- The modules beside this file and the display modules one directory up, so
-- `make test-lua` runs from any directory.
local here = arg[0]:match("^(.*)[/\\][^/\\]+$") or "."
package.path = here .. "/?.lua;" .. here .. "/../?.lua;" .. package.path

local harness = require("mp")

-- Every display module a test loads. main pulls in all of them, and each test
-- clears them, so no test reads another test's module state.
local DISPLAY = {
  "main", "theme", "focus", "scrubber", "strip", "images", "presentation",
  "header", "trickplay", "album", "clock", "volume", "upnext", "advances",
  "audio", "subtitles", "video", "offset", "chooser",
}

-- A hundred-minute film at a 1080 output, where one canvas pixel is one output
-- pixel. A second of this film moves the playhead by about a third of a pixel,
-- so a frame moves it by a hundredth of one.
local FILM = 6000
local START = 1199

local function load()
  for _, name in ipairs(DISPLAY) do
    package.loaded[name] = nil
  end
  package.loaded["mp.utils"] = {
    parse_json = function()
      return nil
    end,
  }
  _G.mp = harness.new({
    ["osd-dimensions"] = { w = 1920, h = 1080 },
    duration = FILM,
    ["time-pos"] = START,
    pause = false,
  })
  require("main")
  return _G.mp
end

-- main batches every redraw request into one zero-second timeout. This runs
-- the ones armed and leaves the idle-hide timer alone, so a test that asks for
-- no redraw fires nothing.
local function run_redraws(fake)
  for _, handle in ipairs(fake.timeouts) do
    if handle.seconds == 0 and not handle.fired and not handle.killed then
      handle.fired = true
      handle.fn()
    end
  end
end

-- Bring the OSD up and run the fade to full, so each test starts from the
-- standing layout a viewer looks at.
local function show(fake)
  fake.messages["summon"]()
  fake.settle()
  run_redraws(fake)
end

local function count(fake)
  return #fake.overlay.updates
end

local tests = {}

local function test(name, fn)
  tests[#tests + 1] = { name = name, fn = fn }
end

test("a frame of playback that moves nothing submits one update", function()
  local fake = load()
  show(fake)
  local before = count(fake)

  fake.push("time-pos", 1200.0)
  run_redraws(fake)
  fake.push("time-pos", 1200.0417)
  run_redraws(fake)

  assert(count(fake) - before == 1, "want 1 update, got " .. count(fake) - before)
end)

test("a whole second of playback submits a second update", function()
  local fake = load()
  show(fake)
  local before = count(fake)

  fake.push("time-pos", 1200.0)
  run_redraws(fake)
  fake.push("time-pos", 1200.0417)
  run_redraws(fake)
  fake.push("time-pos", 1200.6)
  run_redraws(fake)

  assert(count(fake) - before == 2, "want 2 updates, got " .. count(fake) - before)
end)

test("a redraw of the standing layout submits nothing", function()
  local fake = load()
  show(fake)
  local before = count(fake)

  -- A second summon asks for a redraw with the OSD already up, so the layout
  -- comes out as it went in.
  fake.messages["summon"]()
  run_redraws(fake)

  assert(count(fake) == before, "want no update, got " .. count(fake) - before)
end)

test("a frame of playback holds the drawn bar still", function()
  local fake = load()
  local scrubber = require("scrubber")
  show(fake)

  fake.properties["time-pos"] = 1200.0
  local first = scrubber.draw("fine")
  fake.properties["time-pos"] = 1200.0417
  local second = scrubber.draw("fine")

  assert(first == second, "the bar drew two strings for one output pixel:\n" .. first .. "\n" .. second)
end)

test("a pixel of playhead travel redraws the bar", function()
  local fake = load()
  local scrubber = require("scrubber")
  show(fake)

  fake.properties["time-pos"] = 1200.0
  local first = scrubber.draw("fine")
  -- Four seconds of this film carry the playhead a little over one pixel.
  fake.properties["time-pos"] = 1204.0
  local second = scrubber.draw("fine")

  assert(first ~= second, "the bar drew one string across a pixel of travel")
end)

test("the OSD hides by removing the overlay, not by sending it empty", function()
  local fake = load()
  show(fake)

  -- The hide timer is the one timeout armed for idle_hide seconds. Firing it
  -- starts the fade out, and settle runs the fade to clear.
  for _, handle in ipairs(fake.timeouts) do
    if handle.seconds == 4 and not handle.fired and not handle.killed then
      handle.fired = true
      handle.fn()
    end
  end
  fake.settle()
  run_redraws(fake)

  assert(fake.overlay.removes == 1, "want one remove, got " .. fake.overlay.removes)
  for _, data in ipairs(fake.overlay.updates) do
    assert(data ~= "", "an update carried empty data")
  end
end)

test("percent-pos alone asks for no redraw", function()
  local fake = load()
  show(fake)
  local before = count(fake)

  fake.push("percent-pos", 20.0)
  run_redraws(fake)

  assert(count(fake) == before, "want no update, got " .. count(fake) - before)
end)

for _, each in ipairs(tests) do
  each.fn()
  io.write("ok  ", each.name, "\n")
end
