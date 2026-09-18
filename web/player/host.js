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

  // -- the actions under the run page's stage -------------------------------

  const copyButton = document.querySelector(".actions .copy");
  if (copyButton) {
    const label = copyButton.textContent;
    copyButton.addEventListener("click", async () => {
      const url = copyButton.dataset.url || location.href;
      try {
        // The clipboard where there is one; the share sheet where there is
        // not (a phone in a non-secure context, an old WebView).
        if (navigator.clipboard && navigator.clipboard.writeText) await navigator.clipboard.writeText(url);
        else if (navigator.share) { await navigator.share({ url }); return; }
        else throw new Error("no clipboard");
        copyButton.textContent = "Copied";
        copyButton.classList.add("done");
        setTimeout(() => { copyButton.textContent = label; copyButton.classList.remove("done"); }, 1600);
      } catch (err) {
        if (err && err.name === "AbortError") return;
        copyButton.textContent = "Could not copy — the link is in the address bar";
        setTimeout(() => { copyButton.textContent = label; }, 2500);
      }
    });
  }

  const mp4Button = document.querySelector(".actions .mp4");
  if (mp4Button) {
    const label = mp4Button.textContent;
    // The page's one non-feed player: the run this page is about.
    const page = players.find((p) => !p.feed) || players[0];
    if (!("VideoEncoder" in window)) {
      // Said up front rather than discovered on click. The CLI has the same
      // render with ffmpeg behind it, and the footer already names it.
      mp4Button.disabled = true;
      mp4Button.textContent = "mp4 needs a browser with WebCodecs";
    } else {
      mp4Button.addEventListener("click", async () => {
        mp4Button.disabled = true;
        try {
          mp4Button.textContent = "loading the player…";
          const api = await loadWasm();
          const bytes = await fetchTape(page.tapeURL);
          // The encoder needs the tape in the wasm for the whole pass; a
          // playing stage would keep swapping it out under the render.
          if (live) live.pause();
          const loaded = api.load(bytes);
          if (!loaded.ok) throw new Error(loaded.error);
          const blob = await encodeMP4(api, loaded.durationMs, (done, total) => {
            mp4Button.textContent = `encoding ${Math.round((100 * done) / total)}%`;
          });
          // Give the tape back to the stage that had it.
          if (live) { api.load(bytes); live.paint(true); }
          const a = document.createElement("a");
          a.href = URL.createObjectURL(blob);
          a.download = mp4Button.dataset.name || "run.mp4";
          document.body.appendChild(a);
          a.click();
          a.remove();
          setTimeout(() => URL.revokeObjectURL(a.href), 60000);
          mp4Button.textContent = `${label} (${(blob.size / 1048576).toFixed(1)} MB)`;
          mp4Button.classList.add("done");
        } catch (err) {
          report(err);
          mp4Button.textContent = `mp4 failed: ${err && err.message ? err.message : err}`;
        } finally {
          mp4Button.disabled = false;
        }
      });
    }
  }

  // -- the Details section under the run page's table --------------------------

  // The transcript, the card as text, and the record's own paperwork, read
  // out of the tape in the browser by the same wasm Replay paints with — so
  // this side still never opens a tape. Opt-in behind the button: a reader
  // who never scrolls down downloads nothing extra, and Replay starting
  // (from its button or the phone observer below) never loads this.
  const detailsButton = document.querySelector(".details .load");
  if (detailsButton) {
    const detailsBody = document.querySelector(".details .body");
    // The page's one non-feed player: the run this page is about — the same
    // lookup the mp4 button uses.
    const page = players.find((p) => !p.feed) || players[0];
    detailsButton.addEventListener("click", async () => {
      detailsButton.disabled = true;
      try {
        detailsButton.textContent = "loading the player…";
        const api = await loadWasm();
        detailsButton.textContent = "fetching the record…";
        const bytes = await fetchTape(page.tapeURL);
        // The wasm holds one tape. When it already holds this page's there
        // is nothing to load; otherwise load it and give the live stage its
        // own tape back afterwards, exactly as the mp4 handler does —
        // without pausing it.
        let raw;
        if (live === page) {
          raw = api.details();
        } else {
          const prior = live;
          const loaded = api.load(bytes);
          if (!loaded.ok) throw new Error(loaded.error);
          try {
            raw = api.details();
          } finally {
            if (prior) { api.load(bytesCache.get(prior.tapeURL)); prior.paint(true); }
          }
        }
        if (!raw) throw new Error("the player has no record loaded");
        renderDetails(detailsBody, JSON.parse(raw));
        detailsBody.hidden = false;
        detailsButton.remove();
      } catch (err) {
        report(err);
        detailsButton.disabled = false;
        detailsButton.textContent = `details failed: ${err && err.message ? err.message : err}`;
      }
    });
  }

  // Figures the record never observed print as ?, never as a zero nobody
  // measured — the schema rule, the page-side half of it (page.js fmt is
  // the server-side half, for the index row).
  function qInt(v) {
    return v === 0 || v === undefined || v === null ? "?" : String(v);
  }
  function qDec(v, digits) {
    return v === 0 || v === undefined || v === null ? "?" : Number(v).toFixed(digits);
  }

  // Each block is a <details class="block"> with a <summary>, in the order
  // the section promises: Transcript (open), Card, Reproduce, Why the
  // caveats fired, Summary (JSON). A field the wasm did not send — the
  // build's size budget keeps some out — leaves its block out rather than
  // printing a block about nothing. All model text crosses through
  // escapeHTML before innerHTML; nothing raw is ever templated.
  function renderDetails(body, d) {
    const streams = Array.isArray(d.streams) ? d.streams : [];
    const multiRound = streams.some((s) => s && s.round > 0);
    let out = `<details class="block" open><summary>Transcript</summary><div class="inner">`;
    for (const s of streams) out += renderStream(s);
    if (!streams.length) out += `<p>?</p>`;
    out += `</div></details>`;
    if (typeof d.card === "string" && d.card) {
      // Through toHTML, which boxes wide characters to two cells so the
      // box art lines up; a string with no SGR passes through it unchanged.
      out += `<details class="block"><summary>Card</summary><div class="inner"><pre class="cardtext">${toHTML(d.card)}</pre></div></details>`;
    }
    if (typeof d.reproduce === "string" && d.reproduce) {
      out += `<details class="block"><summary>Reproduce</summary><div class="inner">${renderReproduce(d.reproduce)}</div></details>`;
    }
    if (typeof d.explain === "string" && d.explain) {
      out += `<details class="block"><summary>Why the caveats fired</summary><div class="inner"><pre>${escapeHTML(d.explain)}</pre></div></details>`;
    }
    if (d.summary && typeof d.summary === "object") {
      out += `<details class="block"><summary>Summary (JSON)</summary><div class="inner"><pre>${escapeHTML(JSON.stringify(d.summary, null, 2))}</pre></div></details>`;
    }
    body.innerHTML = out;

    function renderStream(s) {
      const idx = Number(s.index) || 0;
      let head = `stream ${idx + 1}`;
      // The round only when the tape has more than one of them.
      if (multiRound) head += ` · round ${(Number(s.round) || 0) + 1}`;
      if (s.name) head += ` · ${escapeHTML(String(s.name))}`;
      const ended = s.ended === undefined || s.ended === null ? "?" : String(s.ended);
      const bad = ended.indexOf("failed") === 0 || ended === "clock cut" || ended === "token cap";
      const t = s.timings || {};
      head += ` <span class="ended${bad ? " bad" : ""}">${escapeHTML(ended)}</span>`;
      head += ` <span class="num">${qInt(t.predicted_n)} tok · ${qDec(t.predicted_per_second, 1)} tok/s · ttft ${qDec(t.ttft_ms, 0)} ms</span>`;
      let st = `<article class="stream"><h3>${head}</h3>`;
      for (const m of s.messages || []) {
        const role = m.role === undefined || m.role === null ? "" : String(m.role);
        // The label is the tape's own string; the class is the same string
        // sanitised to [a-z], so a role nobody has heard of still prints.
        st += `<div class="msg ${escapeHTML(role.toLowerCase().replace(/[^a-z]/g, ""))}"><span class="role">${escapeHTML(role) || "?"}</span><pre>${escapeHTML(m.content === undefined || m.content === null ? "" : String(m.content))}</pre></div>`;
      }
      if (s.reasoning) {
        st += `<details class="reasoning"><summary>thinking · ${qInt(s.reasoning_n)} tok</summary><pre>${escapeHTML(String(s.reasoning))}</pre></details>`;
      }
      const completion = s.completion === undefined || s.completion === null ? "" : String(s.completion);
      if (completion) {
        st += `<div class="msg answer"><span class="role">answer</span><pre>${escapeHTML(completion)}</pre></div>`;
      } else if (s.error) {
        st += `<div class="msg answer err"><span class="role">answer</span><pre>${escapeHTML(String(s.error))}</pre></div>`;
      } else {
        st += `<div class="msg answer"><span class="role">answer</span><pre>?</pre></div>`;
      }
      return st + `</article>`;
    }
  }

  // The reproduce string is Markdown whose outer <details> wrapper this page
  // already provides. No Markdown library: the block's shape is fixed (4-space
  // indented commands, paragraphs, `code` spans), so exact-string rules
  // render it. Everything is escaped first; the structure is derived from
  // the escaped text, never the other way round.
  function renderReproduce(md) {
    const s = String(md)
      .replace(/^<details><summary>Reproduce<\/summary>/, "")
      .replace(/<\/details>\s*$/, "");
    const lines = s.split("\n");
    let out = "";
    let pre = [];
    const flushPre = () => {
      if (pre.length) { out += `<pre>${escapeHTML(pre.join("\n"))}</pre>`; pre = []; }
    };
    const inlineCode = (line) => escapeHTML(line).split("`").map((part, i) =>
      (i % 2 ? `<code>${part}</code>` : part)).join("");
    for (const line of lines) {
      if (/^    /.test(line)) { pre.push(line.slice(4)); continue; }
      flushPre();
      if (/^\s*$/.test(line)) continue;
      out += `<p>${inlineCode(line)}</p>`;
    }
    flushPre();
    return out || `<p>?</p>`;
  }

  document.addEventListener("keydown", (e) => {
    if (!live || live.feed || e.target.tagName === "INPUT") return;
    // Space inside a <summary> toggles the block; the player must not steal it.
    if (e.target.closest && e.target.closest(".details")) return;
    if (e.key === " " || e.key === "k") { e.preventDefault(); live.playing ? live.pause() : live.play(); }
    if (e.key === "ArrowLeft") live.seek(live.t - 5000);
    if (e.key === "ArrowRight") live.seek(live.t + 5000);
  });
  // Every stage that has painted keeps its last frame at the font it was
  // fitted at; when the grid reflows (search.js .rows) that frame would clip
  // inside its own box, so each one is refitted, not only the live one.
  window.addEventListener("resize", () => {
    for (const p of players) if (p.stage.classList.contains("live")) p.fitFont();
    if (live) live.paint(true);
  });
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
  // 자동시작"). On a wide screen the run page's Replay button stays the way
  // in — a 1.6 MB download should be asked for where asking costs nothing —
  // but the listing plays on a desktop too, two or three runs across
  // (user, 2026-09-19: "데스크톱 리스트도 모바일처럼 리스트 재생"): the one
  // most in view starts, and the one under the pointer takes over.
  const narrow = window.matchMedia("(max-width: 48rem)").matches;
  const feed = players.filter((p) => p.feed || narrow);
  if (!narrow) {
    for (const p of feed) {
      p.stage.addEventListener("mouseenter", () => {
        if (live === p && p.playing) return;
        for (const q of feed) if (q !== p && q.playing) q.pause();
        p.resume();
      });
    }
  }
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
  // The same set, one character at a time, for the canvas painter.
  const WIDE_ONE = new RegExp(`^(?:${WIDE.source})$`, "u");
  const WIDE_ANY = new RegExp(WIDE.source, "u");
  function escapeHTML(s) {
    return s
      .replace(/[&<>]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;" })[c])
      .replace(WIDE, (c) => `<b class="w">${c}</b>`);
  }

  // -- the mp4: the same frames, encoded in the browser ------------------------
  //
  // The Worker never opens a tape (spec §9.6) and cannot run ffmpeg, and a
  // render queue somewhere else would be a second copy of the renderer to
  // keep in step. The page already has the renderer — it is what Replay
  // paints with — so the video is drawn here: each frame onto a canvas,
  // through WebCodecs' H.264 encoder, into an mp4 written by hand below.
  // 1280×720 at 30 fps, the shape `toktape render --mp4` would give at its
  // smaller size, and the one every phone's encoder accepts.
  const MP4_W = 1280, MP4_H = 720, MP4_FPS = 30;

  async function encodeMP4(api, durationMs, progress) {
    const canvas = document.createElement("canvas");
    canvas.width = MP4_W;
    canvas.height = MP4_H;
    const ctx = canvas.getContext("2d", { alpha: false });
    const cellW = MP4_W / COLS, cellH = MP4_H / ROWS;
    // Size the face so one glyph advances exactly one cell, measured rather
    // than assumed: the browser's monospace face is whatever it is.
    ctx.font = "100px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace";
    const fontPx = (100 * cellW) / ctx.measureText("0").width;
    const face = `${fontPx.toFixed(2)}px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace`;
    ctx.textBaseline = "alphabetic";

    const total = Math.ceil((durationMs / 1000) * MP4_FPS) + 1;
    const chunks = [];
    // Where the time went, printed at the end: the three costs are the wasm
    // frame, the canvas paint and the encoder, and a slow render is a
    // different fix depending on which.
    const cost = { frame: 0, paint: 0, wait: 0, started: performance.now() };
    let description = null;
    let failed = null;
    const encoder = new VideoEncoder({
      output: (chunk, meta) => {
        if (meta && meta.decoderConfig && meta.decoderConfig.description && !description) {
          description = new Uint8Array(meta.decoderConfig.description.slice(0));
        }
        const data = new Uint8Array(chunk.byteLength);
        chunk.copyTo(data);
        chunks.push({ data, key: chunk.type === "key" });
      },
      error: (e) => { failed = e; },
    });
    const config = {
      codec: "avc1.42001f",
      width: MP4_W,
      height: MP4_H,
      bitrate: 3_000_000,
      framerate: MP4_FPS,
      avc: { format: "avc" },
      latencyMode: "quality",
    };
    const support = await VideoEncoder.isConfigSupported(config);
    if (!support.supported) throw new Error("this browser cannot encode H.264");
    encoder.configure(config);

    for (let i = 0; i < total; i++) {
      if (failed) throw failed;
      const t = Math.min(durationMs, (i * 1000) / MP4_FPS);
      let mark = performance.now();
      const text = api.frame(t, COLS, ROWS);
      cost.frame += performance.now() - mark;
      mark = performance.now();
      paintCanvas(ctx, text, face, cellW, cellH);
      cost.paint += performance.now() - mark;
      const frame = new VideoFrame(canvas, { timestamp: Math.round((i * 1e6) / MP4_FPS), duration: Math.round(1e6 / MP4_FPS) });
      encoder.encode(frame, { keyFrame: i % (MP4_FPS * 2) === 0 });
      frame.close();
      progress(i + 1, total);
      // Let the encoder drain: a tight loop would queue every frame in
      // memory. Waiting on its own dequeue event rather than a timer, because
      // a background tab clamps timers to once a second and a render that
      // took 20 s in front took ten minutes behind (measured 2026-09-18).
      mark = performance.now();
      while (encoder.encodeQueueSize > 4) {
        await new Promise((r) => encoder.addEventListener("dequeue", r, { once: true }));
      }
      cost.wait += performance.now() - mark;
    }
    await encoder.flush();
    encoder.close();
    if (failed) throw failed;
    if (!description) throw new Error("the encoder gave no avcC");
    progress(total, total);
    const wall = performance.now() - cost.started;
    console.log(`toktape mp4: ${total} frames in ${(wall / 1000).toFixed(1)} s — wasm ${cost.frame.toFixed(0)} ms, canvas ${cost.paint.toFixed(0)} ms, waiting on the encoder ${cost.wait.toFixed(0)} ms, ${document.hidden ? "tab hidden" : "tab visible"}`);
    return new Blob([muxMP4(chunks, description, MP4_W, MP4_H, MP4_FPS)], { type: "video/mp4" });
  }

  // One frame onto the canvas: background, then each run of text in its
  // colour, wide characters two cells as everywhere else.
  function paintCanvas(ctx, frame, face, cellW, cellH) {
    ctx.fillStyle = "#101412";
    ctx.fillRect(0, 0, ctx.canvas.width, ctx.canvas.height);
    const lines = frame.split("\n");
    const baseline = cellH * 0.78;
    for (let y = 0; y < lines.length; y++) {
      let x = 0;
      let fg = "", bg = "", bold = false;
      const re = /\x1b\[([0-9;]*)m/g;
      let last = 0, m;
      // A run of narrow characters is one fillText: the face was sized so
      // each advances exactly a cell, so the run lands where the cells are.
      // Only a wide character is placed by hand, because its advance is the
      // fallback face's, not the cell's. Measured 2026-09-18: per-character
      // fillText was 16.6 s of a 738-frame render; runs cut most of it.
      const draw = (text) => {
        if (!text) return;
        let cells = 0;
        for (const ch of text) cells += WIDE_ONE.test(ch) ? 2 : 1;
        if (bg) { ctx.fillStyle = bg; ctx.fillRect(x * cellW, y * cellH, cells * cellW, cellH); }
        if (text.trim() !== "") {
          ctx.fillStyle = fg || "#e6e2d8";
          ctx.font = (bold ? "600 " : "") + face;
          if (!WIDE_ANY.test(text)) {
            ctx.fillText(text, x * cellW, y * cellH + baseline);
          } else {
            let cx = x;
            for (const ch of text) {
              const w = WIDE_ONE.test(ch) ? 2 : 1;
              if (ch !== " ") ctx.fillText(ch, cx * cellW, y * cellH + baseline);
              cx += w;
            }
          }
        }
        x += cells;
      };
      const line = lines[y];
      while ((m = re.exec(line))) {
        draw(line.slice(last, m.index));
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
      draw(line.slice(last));
    }
  }

  // A plain, unfragmented mp4: ftyp, one mdat with every sample, and a moov
  // describing them — the shape every player and every share sheet accepts.
  // The avcC comes from the encoder itself, so the SPS/PPS are the ones the
  // samples were coded against.
  function muxMP4(chunks, avcC, width, height, fps) {
    const TIMESCALE = 90000;
    const delta = Math.round(TIMESCALE / fps);
    const n = chunks.length;
    const durationTS = n * delta;
    const durationMs = Math.round((n * 1000) / fps);

    const be32 = (v) => [(v >>> 24) & 255, (v >>> 16) & 255, (v >>> 8) & 255, v & 255];
    const be16 = (v) => [(v >>> 8) & 255, v & 255];
    const str = (s) => [...s].map((c) => c.charCodeAt(0));
    const box = (type, ...parts) => {
      const body = concat(parts.map((p) => (p instanceof Uint8Array ? p : Uint8Array.from(p))));
      return concat([Uint8Array.from(be32(8 + body.length)), Uint8Array.from(str(type)), body]);
    };
    const full = (type, version, flags, ...parts) => box(type, [version, (flags >>> 16) & 255, (flags >>> 8) & 255, flags & 255], ...parts);

    const ftyp = box("ftyp", str("isom"), be32(0x200), str("isom"), str("iso2"), str("avc1"), str("mp41"));
    const sampleData = concat(chunks.map((c) => c.data));
    const mdat = box("mdat", sampleData);
    const mdatDataOffset = ftyp.length + 8;

    const mvhd = full("mvhd", 0, 0, be32(0), be32(0), be32(1000), be32(durationMs),
      be32(0x00010000), be16(0x0100), be16(0), be32(0), be32(0),
      be32(0x00010000), be32(0), be32(0), be32(0), be32(0x00010000), be32(0), be32(0), be32(0), be32(0x40000000),
      be32(0), be32(0), be32(0), be32(0), be32(0), be32(0), be32(2));
    const tkhd = full("tkhd", 0, 3, be32(0), be32(0), be32(1), be32(0), be32(durationMs),
      be32(0), be32(0), be16(0), be16(0), be16(0), be16(0),
      be32(0x00010000), be32(0), be32(0), be32(0), be32(0x00010000), be32(0), be32(0), be32(0), be32(0x40000000),
      be32(width << 16), be32(height << 16));
    const mdhd = full("mdhd", 0, 0, be32(0), be32(0), be32(TIMESCALE), be32(durationTS), be16(0x55c4), be16(0));
    const hdlr = full("hdlr", 0, 0, be32(0), str("vide"), be32(0), be32(0), be32(0), str("toktape\0"));
    const vmhd = full("vmhd", 0, 1, be16(0), be16(0), be16(0), be16(0));
    const dinf = box("dinf", full("dref", 0, 0, be32(1), full("url ", 0, 1)));
    const avc1 = box("avc1", be32(0), be16(0), be16(1), be32(0), be32(0), be32(0), be32(0),
      be16(width), be16(height), be32(0x00480000), be32(0x00480000), be32(0), be16(1),
      new Uint8Array(32), be16(0x0018), be16(0xffff), box("avcC", avcC));
    const stsd = full("stsd", 0, 0, be32(1), avc1);
    const stts = full("stts", 0, 0, be32(1), be32(n), be32(delta));
    const keys = chunks.map((c, i) => (c.key ? i + 1 : 0)).filter(Boolean);
    const stss = full("stss", 0, 0, be32(keys.length), ...keys.map(be32));
    const stsc = full("stsc", 0, 0, be32(1), be32(1), be32(n), be32(1));
    const stsz = full("stsz", 0, 0, be32(0), be32(n), ...chunks.map((c) => be32(c.data.length)));
    const stco = full("stco", 0, 0, be32(1), be32(mdatDataOffset));
    const stbl = box("stbl", stsd, stts, stss, stsc, stsz, stco);
    const minf = box("minf", vmhd, dinf, stbl);
    const mdia = box("mdia", mdhd, hdlr, minf);
    const trak = box("trak", tkhd, mdia);
    const moov = box("moov", mvhd, trak);
    return concat([ftyp, mdat, moov]);
  }

  function concat(arrays) {
    let n = 0;
    for (const a of arrays) n += a.length;
    const out = new Uint8Array(n);
    let o = 0;
    for (const a of arrays) { out.set(a, o); o += a.length; }
    return out;
  }

  function stamp(ms) {
    const s = Math.floor(ms / 1000);
    return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
  }
})();
