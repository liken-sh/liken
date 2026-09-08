-- These tests cover the one-request-in-flight gate in display/trickplay.lua,
-- against the fake mp table in display/test/mp.lua. They run under a plain
-- lua interpreter, because the display's modules reach mpv only through the
-- mp global.

-- The modules beside this file and the display modules one directory up, so
-- `make test-lua` runs from any directory.
local here = arg[0]:match("^(.*)[/\\][^/\\]+$") or "."
package.path = here .. "/?.lua;" .. here .. "/../?.lua;" .. package.path

local harness = require("mp")

-- The metric the display reads off mpv. A scale of one on both axes makes the
-- pixel box the canvas box, so the requests below carry the canvas numbers.
local function fake_theme()
  return {
    osd_scale = function()
      return { sx = 1, sy = 1, w = 1920 }
    end,
  }
end

-- The tile box in display/trickplay.lua, as the request carries it at this
-- metric.
local TILE_W = "360"
local TILE_H = "220"

-- Each test loads a module of its own. The gate is state inside the module,
-- and a second test must not read the first one's requests.
local function load_display()
  package.loaded["trickplay"] = nil
  package.loaded["theme"] = fake_theme()
  _G.mp = harness.new()
  return require("trickplay"), _G.mp
end

-- The art requests among the commands the display sent. An overlay-add for a
-- tile already held is not a request.
local function requests(fake)
  local out = {}
  for _, command in ipairs(fake.commands) do
    if command[1] == "script-message" and command[2] == "liken-art-request" then
      out[#out + 1] = command
    end
  end
  return out
end

-- One reply from the bridge. It carries the tile and no key, the way the
-- bridge answers.
local function reply(display)
  display.on_art("trickplay", "/run/liken/art/tile.bgra", "16", "9", "64")
end

local function count(fake, want_count)
  local sent = requests(fake)
  assert(#sent == want_count, string.format("sent %d requests, want %d", #sent, want_count))
  return sent
end

local function at(sent, index, ms)
  assert(sent[index][4] == ms, string.format("request %d asked at %s, want %s", index, sent[index][4], ms))
end

local tests = {}

local function test(name, fn)
  tests[#tests + 1] = { name = name, fn = fn }
end

test("three syncs during one request in flight send one request", function()
  local display, fake = load_display()

  display.sync(true, 10, 100)
  display.sync(true, 11, 100)
  display.sync(true, 12, 100)

  local sent = count(fake, 1)
  at(sent, 1, "10000")
  assert(sent[1][5] == TILE_W and sent[1][6] == TILE_H, "the request carries the pixel box")
end)

test("the reply sends the newest want and stops the timer", function()
  local display, fake = load_display()

  display.sync(true, 10, 100)
  display.sync(true, 11, 100)
  display.sync(true, 12, 100)
  reply(display)

  local sent = count(fake, 2)
  at(sent, 2, "12000")
  assert(fake.timeouts[1].killed, "the reply left the first timer running")
  assert(#fake.timeouts == 2, "the second request armed no timer")
end)

test("a reply for the want the display holds sends nothing", function()
  local display, fake = load_display()

  display.sync(true, 10, 100)
  display.sync(true, 10.2, 100)
  reply(display)
  display.sync(true, 10.4, 100)

  count(fake, 1)
end)

test("the timeout reopens the gate when no reply arrives", function()
  local display, fake = load_display()

  display.sync(true, 10, 100)
  display.sync(true, 11, 100)
  fake.fire_timeout()
  display.sync(true, 12, 100)

  local sent = count(fake, 2)
  at(sent, 2, "12000")
end)

test("a new item reopens the gate", function()
  local display, fake = load_display()

  display.sync(true, 10, 100)
  display.on_item()
  display.sync(true, 20, 100)

  local sent = count(fake, 2)
  at(sent, 2, "20000")
end)

test("a resize reopens the gate and asks at the new box", function()
  local display, fake = load_display()

  display.sync(true, 10, 100)
  display.on_resize()
  display.sync(true, 10, 100)

  local sent = count(fake, 2)
  at(sent, 2, "10000")
end)

for _, each in ipairs(tests) do
  each.fn()
  io.write("ok  ", each.name, "\n")
end
