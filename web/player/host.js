// The page side of the player: it paints the frames the wasm exports.
//
// web/player/main.go hands back exactly what the terminal would have shown —
// rows lines of cols columns with the terminal's own SGR escapes in them —
// and this file is what turns that into pixels. It is deliberately the only
// place that knows what an escape looks like: the renderer does not know it
// is in a browser, and nothing here reads a tape's fields.
//
// No framework and no build step. It is served next to the wasm as a static
// asset and loaded with a plain <script>, because a share link's reader has
// already been asked to download the wasm and should not be asked to
// download a toolchain's output on top of it.
//
// Two pages use it. /r/<id> has one stage with a Replay button and a
// transport; the front page has one stage per listed run and no buttons —
// there a run plays when it scrolls into view and stops when it leaves
// (user, 2026-09-18: "모바일로 들어왔을 때 스크롤하는 대로 자동재생되는
// 목록"). Both are the same player: the wasm holds one tape at a time, so
// exactly one stage is live and the others hold their last frame.
//
// Time is clip time. The frame at t is a pure function of t (spec §1,
// decision 10), so play is "advance t with the wall clock", scrub is "set t",
// and pause is "stop advancing" — none of them holds state the renderer does
// not already have.

(() => {
  // The size the run is replayed at. The tape does not record the size it
  // was watched at, so this is the terminal's own default
  // (internal/render: DefaultWidth × DefaultHeight) rather than a guess
  // from the page's width. A narrow screen scales the font, never the
  // column count: below internal/tui's MinWidth × MinHeight the renderer
  // paints "too small" instead of the run.
  const COLS = 120;
  const ROWS = 36;
  // Paint at most this often. A frame costs ~6 ms in wasm on a laptop
  // (measured on the hero tape) and a few times that on a phone; a display
  // at 120 Hz asking for one on every vsync would spend most of its budget
  // re-drawing a spinner nobody can see move.
  const MAX_FPS = 30;

  const stages = [...document.querySelectorAll("[data-tape]")];
  if (!stages.length) return;

  // -- shared: the wasm, the bytes, the one live player --------------------

  let apiPromise = null;
  function loadWasm() {
    if (apiPromise) return apiPromise;
    apiPromise = (async () => {
      // wasm_exec.js is the classic-script loader for the exact Go that
      // built the binary; the page loads it before this file.
      if (typeof Go !== "function") throw new Error("wasm_exec.js did not load");
      const go = new Go();
      const src = fetch("/player/toktape.wasm");
      const result = WebAssembly.instantiateStreaming
        ? await WebAssembly.instantiateStreaming(src, go.importObject)
        : await WebAssembly.instantiate(await (await src).arrayBuffer(), go.importObject);
      // Not awaited: main() parks in select {} so the exports stay callable.
      go.run(result.instance);
      if (!globalThis.toktape) throw new Error("the player did not export itself");
      return globalThis.toktape;
    })();
    apiPromise.catch(() => { apiPromise = null; });
    return apiPromise;
  }

  const bytesCache = new Map();
  async function fetchTape(url) {
    if (bytesCache.has(url)) return bytesCache.get(url);
    const res = await fetch(url);
    if (!res.ok) throw new Error(`the record did not download (${res.status})`);
    const bytes = new Uint8Array(await res.arrayBuffer());
    bytesCache.set(url, bytes);
    return bytes;
  }

  // The player whose tape the wasm currently holds. Switching is a load(),
  // which is cheap (a few ms for a 50 KB record) and keeps the wasm at one
  // tape rather than one per stage on the page.
  let live = null;
  let raf = 0;

  // A feed item has nowhere to print a failure, so it goes to the console
  // rather than nowhere: a page where nothing plays should at least say why
  // to whoever opens the tools.
  function report(err) {
    console.error("toktape player:", err && err.message ? err.message : err);
  }

  function tick(now) {
    raf = 0;
    if (!live) return;
    live.tick(now);
    if (live.playing) raf = requestAnimationFrame(tick);
  }
  function schedule() {
    if (!raf) raf = requestAnimationFrame(tick);
  }

  // -- one stage -------------------------------------------------------------

  class Player {
    constructor(stage) {
      this.stage = stage;
      this.tapeURL = stage.dataset.tape;
      this.screen = stage.querySelector(".screen");
      this.button = stage.querySelector(".replay");
      this.controls = stage.querySelector(".controls");
      this.status = stage.querySelector(".status");
      // A stage without a button is a feed item: it plays on its own when
      // it is in view, loops, and a tap on it is the link it sits in.
      this.feed = !this.button;
      this.api = null;
      this.durationMs = 0;
      this.t = 0;
      this.playing = false;
      this.wallAtT = 0;
      this.lastPaintAt = -1;
      this.loading = false;

      if (this.button) {
        this.label = this.button.textContent;
        this.button.addEventListener("click", () => this.startFromButton());
      }
      if (this.controls) {
        this.toggle = this.controls.querySelector(".toggle");
        this.scrub = this.controls.querySelector(".scrub");
        this.clock = this.controls.querySelector(".clock");
        this.toggle.addEventListener("click", () => (this.playing ? this.pause() : this.play()));
        this.scrub.addEventListener("input", () => {
          // Dragging the bar is scrubbing, not seeking-while-playing: the
          // frame follows the thumb and resumes from there on release.
          if (this.playing) this.pause();
          this.seek(Number(this.scrub.value));
        });
        this.screen.addEventListener("click", () => (this.playing ? this.pause() : this.play()));
      }
    }

    startFromButton() {
      this.button.disabled = true;
      this.say("");
      this.start(true).catch((err) => {
        this.button.disabled = false;
        this.button.textContent = this.label;
        this.say(err.message || String(err));
      });
    }

    // Bring the tape into the wasm and this stage to the front. Progress
    // goes on the button the reader pressed, where their eye already is;
    // a feed item has no button and says nothing — it simply starts.
    async start(fromButton) {
      if (this.loading) return;
      this.loading = true;
      try {
        if (fromButton) this.button.textContent = "loading the player…";
        this.api = await loadWasm();
        if (fromButton) this.button.textContent = "fetching the record…";
        const bytes = await fetchTape(this.tapeURL);
        this.activate(bytes);
      } finally {
        this.loading = false;
      }
    }

    activate(bytes) {
      if (live && live !== this) live.pause();
      const loaded = this.api.load(bytes);
      if (!loaded.ok) throw new Error(`the record did not load: ${loaded.error}`);
      live = this;
      this.durationMs = loaded.durationMs;
      this.stage.classList.add("live");
      if (this.scrub) this.scrub.max = String(this.durationMs);
      this.fitFont();
      this.seek(this.t);
      this.play();
    }

    // A feed item leaving the view stops; coming back it resumes where it
    // was, re-loading its tape into the wasm if another item took it.
    resume() {
      if (live === this) { if (!this.playing) this.play(); return; }
      if (!this.api) { this.start(false).catch(report); return; }
      const bytes = bytesCache.get(this.tapeURL);
      if (bytes) this.activate(bytes);
      else this.start(false).catch(report);
    }

    play() {
      if (live !== this) return;
      if (this.t >= this.durationMs) this.t = 0;
      this.playing = true;
      this.wallAtT = performance.now();
      if (this.toggle) this.toggle.textContent = "Pause";
      schedule();
    }

    pause() {
      this.playing = false;
      if (this.toggle) this.toggle.textContent = this.t >= this.durationMs ? "Replay" : "Play";
    }

    seek(ms) {
      this.t = Math.max(0, Math.min(this.durationMs, ms));
      this.wallAtT = performance.now();
      if (!this.playing && this.toggle) this.toggle.textContent = this.t >= this.durationMs ? "Replay" : "Play";
      this.paint(true);
    }

    tick(now) {
      if (this.playing) {
        this.t = Math.min(this.durationMs, this.t + (now - this.wallAtT));
        this.wallAtT = now;
        if (this.t >= this.durationMs) {
          // A feed item loops — it is a moving picture in a list. The run
          // page stops on the card, which is what a reader scrubbed to.
          if (this.feed) this.t = 0;
          else this.pause();
        }
      }
      this.paint(false, now);
    }

    paint(force, now = performance.now()) {
      if (live !== this) return;
      if (!force && now - this.lastPaintAt < 1000 / MAX_FPS) return;
      this.lastPaintAt = now;
      this.screen.innerHTML = toHTML(this.api.frame(this.t, COLS, ROWS));
      if (this.scrub) this.scrub.value = String(this.t);
      if (this.clock) this.clock.textContent = `${stamp(this.t)} / ${stamp(this.durationMs)}`;
    }

    // The frame is COLS columns wide whatever the screen is, so the font is
    // sized to make COLS cells fill the stage. Measured from a probe rather
    // than assumed from the font's nominal advance: monospace fonts differ.
    fitFont() {
      const probe = document.createElement("span");
      probe.textContent = "0".repeat(COLS);
      probe.style.cssText = "position:absolute;visibility:hidden;white-space:pre;font-size:100px";
      this.screen.appendChild(probe);
      const perPx = probe.getBoundingClientRect().width / 100;
      probe.remove();
      const size = this.stage.getBoundingClientRect().width / perPx;
      this.screen.style.fontSize = `${Math.floor(size * 100) / 100}px`;
    }

    say(msg) {
      if (!this.status) return;
      this.status.textContent = msg;
      this.status.hidden = !msg;
    }
  }

  const players = stages.map((s) => new Player(s));

  document.addEventListener("keydown", (e) => {
    if (!live || live.feed || e.target.tagName === "INPUT") return;
    if (e.key === " " || e.key === "k") { e.preventDefault(); live.playing ? live.pause() : live.play(); }
    if (e.key === "ArrowLeft") live.seek(live.t - 5000);
    if (e.key === "ArrowRight") live.seek(live.t + 5000);
  });
  window.addEventListener("resize", () => { if (live) { live.fitFont(); live.paint(true); } });
  document.addEventListener("visibilitychange", () => {
    if (document.hidden && live) live.pause();
  });

  // -- the feed: whichever item is most in view is the one that plays -----
  //
  // One at a time, by the wasm's nature and by the reader's: two runs
  // animating side by side is twice the battery for half the attention.
  //
  // On a phone the run page's own stage joins in: there is no hover and no
  // idle cursor to invite a click, so the run starts when it scrolls into
  // view, the way a video in a feed does (user, 2026-09-18: "스크롤하면
  // 자동시작"). On a wide screen the Replay button stays the way in — a
  // 1.6 MB download should be asked for where asking costs nothing.
  const narrow = window.matchMedia("(max-width: 48rem)").matches;
  const feed = players.filter((p) => p.feed || narrow);
  if (feed.length && "IntersectionObserver" in window) {
    const ratio = new Map();
    const io = new IntersectionObserver((entries) => {
      for (const e of entries) ratio.set(e.target, e.isIntersecting ? e.intersectionRatio : 0);
      let best = null, bestRatio = 0.5;
      for (const p of feed) {
        const r = ratio.get(p.stage) || 0;
        if (r > bestRatio) { best = p; bestRatio = r; }
      }
      for (const p of feed) if (p !== best && p.playing) p.pause();
      if (best && !(live === best && best.playing)) {
        if (best.button && !best.button.disabled) best.startFromButton();
        else best.resume();
      }
    }, { threshold: [0, 0.25, 0.5, 0.75, 1] });
    for (const p of feed) io.observe(p.stage);
  }

  // -- painting ---------------------------------------------------------------

  // The frame's SGR escapes, and nothing else: reset, bold, and the 24-bit
  // foreground/background the colour theme emits (internal/tui/theme.go sets
  // the renderer to TrueColor). Anything else is dropped rather than shown,
  // so a code this did not anticipate costs a colour, not the layout.
  function toHTML(frame) {
    let out = "";
    let fg = "", bg = "", bold = false;
    const re = /\x1b\[([0-9;]*)m/g;
    let last = 0, m;
    const flush = (text) => {
      if (!text) return;
      const style = (fg ? `color:${fg};` : "") + (bg ? `background:${bg};` : "") + (bold ? "font-weight:600;" : "");
      if (style) out += `<span style="${style}">${escapeHTML(text)}</span>`;
      else out += escapeHTML(text);
    };
    while ((m = re.exec(frame))) {
      flush(frame.slice(last, m.index));
      last = re.lastIndex;
      const p = m[1] === "" ? [0] : m[1].split(";").map(Number);
      for (let i = 0; i < p.length; i++) {
        const c = p[i];
        if (c === 0) { fg = ""; bg = ""; bold = false; }
        else if (c === 1) bold = true;
        else if (c === 22) bold = false;
        else if (c === 39) fg = "";
        else if (c === 49) bg = "";
        else if ((c === 38 || c === 48) && p[i + 1] === 2) {
          const rgb = `rgb(${p[i + 2]},${p[i + 3]},${p[i + 4]})`;
          if (c === 38) fg = rgb; else bg = rgb;
          i += 4;
        }
      }
    }
    flush(frame.slice(last));
    return out;
  }

  // The renderer laid the frame out in terminal cells, where a Hangul or CJK
  // character is exactly two cells wide. A browser's monospace font does
  // not promise that — the fallback face it picks for those characters has
  // its own advance — so a line with Korean in it came out wider than its
  // 120 columns and pushed the right-hand panels off the stage (seen on a
  // DeepSeek run answering in Korean, 2026-09-18). Each wide character is
  // therefore boxed to two cells of the ASCII face, which is what the
  // renderer assumed.
  const WIDE = /[ᄀ-ᅟ⺀-〾ぁ-㏿㐀-䶿一-鿿ꀀ-꓏가-힣豈-﫿︰-﹏＀-｠￠-￦]|[\u{1F300}-\u{1F64F}\u{1F900}-\u{1F9FF}\u{20000}-\u{3FFFD}]/gu;
  function escapeHTML(s) {
    return s
      .replace(/[&<>]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;" })[c])
      .replace(WIDE, (c) => `<b class="w">${c}</b>`);
  }

  function stamp(ms) {
    const s = Math.floor(ms / 1000);
    return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
  }
})();
