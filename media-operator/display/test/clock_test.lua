-- The clock: where display/clock.lua puts the reading and the end of the
-- film, against the fake mp table in display/test/mp.lua. The reading holds
-- one box on the canvas, because the idle screen and the library browser draw
-- their own clock in that box and the three screens follow each other on one
-- panel.

local here = arg[0]:match("^(.*)[/\\][^/\\]+$") or "."
package.path = here .. "/?.lua;" .. here .. "/../?.lua;" .. package.path

local harness = require("mp")

-- Every display module a test loads. Each test clears them, so no test reads
-- another test's module state.
local DISPLAY = { "theme", "clock" }

local function load(properties)
  for _, name in ipairs(DISPLAY) do
    package.loaded[name] = nil
  end
  _G.mp = harness.new(properties or { ["osd-dimensions"] = { w = 1920, h = 1080 } })
  return require("clock"), require("theme")
end

-- The canvas numbers the design gives: the right margin the reading ends on,
-- the top margin it hangs from, and the row the end time draws on, one line
-- pitch under it.
local MARGIN = 96
local RIGHT = 1920 - MARGIN
local TOP_Y = 90
local ENDS_Y = 136

local function has(text, want)
  assert(text and text:find(want, 1, true), "want " .. want .. " in:\n" .. tostring(text))
end

local function lacks(text, want)
  assert(not (text and text:find(want, 1, true)), "unwanted " .. want .. " in:\n" .. tostring(text))
end

local tests = {}

local function test(name, fn)
  tests[#tests + 1] = { name = name, fn = fn }
end

test("the reading hangs from the top margin at the right", function()
  local clock = load()

  local drawn = clock.draw()

  has(drawn, "\\an9")
  has(drawn, string.format("\\pos(%d.00,%d.00)", RIGHT, TOP_Y))
  has(drawn, "\\fs34")
  lacks(drawn, "ends")
end)

test("the end time draws one line pitch under the reading", function()
  local clock = load({ ["osd-dimensions"] = { w = 1920, h = 1080 }, duration = 7200, ["time-pos"] = 0 })

  local drawn = clock.draw()

  has(drawn, string.format("\\pos(%d.00,%d.00)", RIGHT, TOP_Y))
  has(drawn, string.format("\\pos(%d.00,%d.00)", RIGHT, ENDS_Y))
  has(drawn, "ends ")
end)

test("the end time reads dimmer than the reading", function()
  local clock, theme = load({ ["osd-dimensions"] = { w = 1920, h = 1080 }, duration = 7200, ["time-pos"] = 0 })

  local drawn = clock.draw()
  local reading, ends = drawn:match("^(.-)\n(.*)$")

  assert(reading and ends, "the clock drew one line for a film with a duration")
  has(reading, "\\1a" .. theme.alpha.opaque)
  has(ends, "\\1a" .. theme.alpha.subdued)
end)

test("the reading hangs off the right edge of a wider canvas", function()
  local clock, theme = load({ ["osd-dimensions"] = { w = 2560, h = 1080 } })

  theme.update_canvas()
  local drawn = clock.draw()

  has(drawn, string.format("\\pos(%d.00,%d.00)", 2560 - MARGIN, TOP_Y))
end)

for _, each in ipairs(tests) do
  each.fn()
  io.write("ok  ", each.name, "\n")
end
