// Unit tests for the avatar's three display sizes (user, 2026-09-21:
// "기본적으로 아바타 크기 전반적으로 더 키우고 싶어").
//
// Each size is declared twice on purpose: once in CSS and once as the
// width/height attributes on the <img>, which reserve the box before the
// image loads so the page does not reflow. A CSS size that disagrees with
// the attributes reintroduces the layout shift the attributes exist to
// prevent — so every test here reads BOTH declarations and asserts they are
// the same number, not just that a number exists. What this file cannot do
// is lay a page out; the no-overflow-at-390 property and the row-height
// check live in the render pass (web/shot.sh plus a headless-Chrome
// measurement), which is why these tests pin the contract the render is
// checked against.
//
// Follows test/card.test.mjs: node:test, no dependencies, fakes shaped like
// the statements the handlers actually make.
//
// Run: node --test test/   (from web/)

import assert from "node:assert/strict";
import test from "node:test";

import { STYLE } from "../src/page.js";
import { PAGE_STYLE, resultRow } from "../src/search.js";
import { serveRunPage } from "../src/run.js";
import { serveUserPage } from "../src/user.js";

const BASE = "https://tape.midagedev.com";
// An avatar_key the way upload.js stores it; avatarPath accepts exactly this.
const AVATAR_KEY = `avatars/${"a".repeat(64)}.png`;

// The px size a CSS rule declares for an avatar selector, or null. Both
// width and height are declared; one number read back covers both, but the
// test asserts them separately so a "width only" regression fails by name.
function cssAvatarPx(style, selector) {
  const m = style.match(new RegExp(`${selector.replace(/\./g, "\\.")} \\{([^}]*)\\}`));
  if (!m) return null;
  const w = /width: (\d+)px/.exec(m[1]);
  const h = /height: (\d+)px/.exec(m[1]);
  if (!w || !h || w[1] !== h[1]) return null;
  return Number(w[1]);
}

// The width/height attributes on the first class="avatar" img in a rendered
// page, as numbers — the box reserved before the image loads.
function imgAttrs(html) {
  const m = /<img class="avatar"[^>]*>/.exec(html);
  if (!m) return null;
  const w = /width="(\d+)"/.exec(m[0]);
  const h = /height="(\d+)"/.exec(m[0]);
  if (!w || !h || w[1] !== h[1]) return null;
  return Number(w[1]);
}

test("the three CSS sizes are 20/32/96", () => {
  assert.equal(cssAvatarPx(STYLE, ".avatar"), 32, "the byline's base .avatar");
  assert.equal(cssAvatarPx(STYLE, ".uhead .avatar"), 96, "the user home header");
  assert.equal(cssAvatarPx(PAGE_STYLE, ".who .avatar"), 20, "a search row's .who line");
});

test("the home's avatar never passes the 128px ceiling the stored image sets", () => {
  // The upload keeps avatars at most 256×256 (internal/publish/avatar.go,
  // MaxAvatarWidth/MaxAvatarHeight), so a displayed size above 128px is
  // upscaled on a 2× display. This pins the ceiling as a number so raising
  // the display size past it fails a test rather than shipping soft circles.
  for (const sel of [".avatar", ".uhead .avatar", ".who .avatar"]) {
    const px = cssAvatarPx(sel === ".who .avatar" ? PAGE_STYLE : STYLE, sel);
    assert.ok(px !== null, `${sel} declares no single px size`);
    assert.ok(px <= 128, `${sel} at ${px}px passes the 128px ceiling the stored image sets`);
  }
});

test("the byline's rhythm still pulls it under the sub line", () => {
  // The negative top margin is what places the name where it sits relative
  // to the title above it; a size change that moved it would be a layout
  // change the bigger avatar did not ask for.
  assert.match(STYLE, /\.byline \{[^}]*margin: -\.75rem 0 1\.5rem/);
});

test("a search row's avatar attributes match its CSS: 20", () => {
  const html = resultRow(
    {
      id: "abcdefgh",
      tape_ext: ".toktape",
      card_key: "runs/abcdefgh/card.png",
      author_name: "Lab Rat",
      avatar_key: AVATAR_KEY,
      owner_token: "t_journal",
      owned: 1,
    },
    new URL(`${BASE}/`),
  );
  const attrs = imgAttrs(html);
  assert.ok(attrs, "the row's .who line carries no avatar img");
  assert.equal(attrs, cssAvatarPx(PAGE_STYLE, ".who .avatar"), "the attributes and the CSS disagree");
  assert.equal(attrs, 20);
});

// The run page's handler, with the one statement it makes: the row by id.
// Shaped like the fake in test/card.test.mjs, which this page's SELECT
// shares down to the AS owned column.
function runEnv(row) {
  return {
    DB: {
      prepare(sql) {
        return {
          bind() {
            return { first: async () => (sql.includes("AS owned") ? row : null) };
          },
        };
      },
    },
  };
}

test("the run page's byline avatar attributes match its CSS: 32", async () => {
  const row = {
    id: "abcdefgh",
    created_at: "2026-09-21T07:00:00Z",
    private: 0,
    index_json: JSON.stringify({ schema: 1, model_raw: "test", decode_per_sec: 1 }),
    tape_ext: ".toktape",
    tape_bytes: 25600,
    card_key: "runs/abcdefgh/card.png",
    author_name: "Lab Rat",
    author_link: null,
    avatar_key: AVATAR_KEY,
    title: null,
    note: null,
    owner_token: "t_journal",
    owned: 1,
  };
  const resp = await serveRunPage("abcdefgh", runEnv(row), BASE);
  const html = await resp.text();
  const attrs = imgAttrs(html);
  assert.ok(attrs, "the byline carries no avatar img");
  assert.equal(attrs, cssAvatarPx(STYLE, ".avatar"), "the attributes and the CSS disagree");
  assert.equal(attrs, 32);
});

// The user home makes more statements (profile, listing, facets, total), so
// the fake answers by shape: first() for the token and the COUNT, all() for
// the listing and the facets — an empty listing, which still renders the
// header this file is about.
function userEnv(profile) {
  return {
    DB: {
      prepare(sql) {
        return {
          bind() {
            return {
              first: async () => (sql.includes("FROM tokens") ? profile : { n: 0 }),
              all: async () => ({ results: [] }),
            };
          },
        };
      },
    },
  };
}

test("the user home's header avatar attributes match its CSS: 96", async () => {
  const env = userEnv({ id: "t_journal", name: "Lab Rat", link: null, avatar_key: AVATAR_KEY, bio: null });
  const resp = await serveUserPage("t-journal", new Request(`${BASE}/u/t-journal`), env);
  const html = await resp.text();
  const attrs = imgAttrs(html);
  assert.ok(attrs, "the home's header carries no avatar img");
  assert.equal(attrs, cssAvatarPx(STYLE, ".uhead .avatar"), "the attributes and the CSS disagree");
  assert.equal(attrs, 96);
});
