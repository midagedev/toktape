// The page side of the player: it paints the frames the wasm exports.
//
// web/player/main.go hands back exactly what the terminal would have shown —
// rows lines of cols columns with the terminal's own SGR escapes in them —
// and this file is what turns that into pixels. It is deliberately the only
// place that knows what an escape looks like: the renderer does not know it
// is in a browser, and nothing here reads a tape's fields.
//
// No framework and no build step. It is served next to the wasm as a static
// asset and loaded by /r/<id> with a plain <script>, because a share link's
// reader has already been asked to download the wasm and should not be
// asked to download a toolchain's output on top of it.
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
  // Paint at most this often. A frame costs ~6 ms in wasm (measured on the
  // hero tape); a display at 120 Hz asking for one on every vsync would
  // spend most of its budget re-drawing a spinner nobody can see move.
  const MAX_FPS = 30;

  const stage = document.querySelector("[data-tape]");
  if (!stage) return;
  const tapeURL = stage.dataset.tape;
  const button = stage.querySelector(".replay");
  const screen = stage.querySelector(".screen");
  const controls = stage.querySelector(".controls");
  const toggle = controls.querySelector(".toggle");
  const scrub = controls.querySelector(".scrub");
  const clock = controls.querySelector(".clock");
  const status = stage.querySelector(".status");

  let api = null;
  let durationMs = 0;
  let t = 0; // clip time, ms
  let playing = false;
  let wallAtT = 0; // performance.now() when t was last set
  let lastPaintAt = -1;
  let raf = 0;

  button.addEventListener("click", () => {
    button.disabled = true;
    start().catch((err) => {
      button.disabled = false;
      say(err.message || String(err));
    });
  });

  async function start() {
    say("loading the player…");
    if (!api) api = await loadWasm();
    say("fetching the record…");
    const res = await fetch(tapeURL);
    if (!res.ok) throw new Error(`the record did not download (${res.status})`);
    const bytes = new Uint8Array(await res.arrayBuffer());
    const loaded = api.load(bytes);
    if (!loaded.ok) throw new Error(`the record did not load: ${loaded.error}`);
    durationMs = loaded.durationMs;

    stage.classList.add("live");
    scrub.max = String(durationMs);
    fitFont();
    say("");
    seek(0);
    play();
  }

  async function loadWasm() {
    // wasm_exec.js is the classic-script loader for the exact Go that built
    // the binary; /r/<id> loads it before this file.
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
  }

  // -- transport ------------------------------------------------------------

  function play() {
    if (t >= durationMs) t = 0;
    playing = true;
    wallAtT = performance.now();
    toggle.textContent = "Pause";
    if (!raf) raf = requestAnimationFrame(tick);
  }

  function pause() {
    playing = false;
    toggle.textContent = t >= durationMs ? "Replay" : "Play";
  }

  function seek(ms) {
    t = Math.max(0, Math.min(durationMs, ms));
    wallAtT = performance.now();
    paint(true);
  }

  function tick(now) {
    raf = 0;
    if (playing) {
      t = Math.min(durationMs, t + (now - wallAtT));
      wallAtT = now;
      if (t >= durationMs) pause();
    }
    paint(false, now);
    if (playing) raf = requestAnimationFrame(tick);
  }

  toggle.addEventListener("click", () => (playing ? pause() : play()));
  scrub.addEventListener("input", () => {
    // Dragging the bar is scrubbing, not seeking-while-playing: the frame
    // follows the thumb and resumes from there on release.
    if (playing) pause();
    seek(Number(scrub.value));
  });
  screen.addEventListener("click", () => (playing ? pause() : play()));
  document.addEventListener("keydown", (e) => {
    if (!stage.classList.contains("live") || e.target.tagName === "INPUT") return;
    if (e.key === " " || e.key === "k") { e.preventDefault(); playing ? pause() : play(); }
    if (e.key === "ArrowLeft") seek(t - 5000);
    if (e.key === "ArrowRight") seek(t + 5000);
  });
  window.addEventListener("resize", fitFont);

  // -- painting -------------------------------------------------------------

  function paint(force, now = performance.now()) {
    if (!force && now - lastPaintAt < 1000 / MAX_FPS) return;
    lastPaintAt = now;
    screen.innerHTML = toHTML(api.frame(t, COLS, ROWS));
    scrub.value = String(t);
    clock.textContent = `${stamp(t)} / ${stamp(durationMs)}`;
  }

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

  function escapeHTML(s) {
    return s.replace(/[&<>]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;" })[c]);
  }

  // The frame is COLS columns wide whatever the screen is, so the font is
  // sized to make COLS cells fill the stage. Measured from a probe rather
  // than assumed from the font's nominal advance: monospace fonts differ.
  function fitFont() {
    const probe = document.createElement("span");
    probe.textContent = "0".repeat(COLS);
    probe.style.cssText = "position:absolute;visibility:hidden;white-space:pre;font-size:100px";
    screen.appendChild(probe);
    const perPx = probe.getBoundingClientRect().width / 100; // width of COLS cells at 1px
    probe.remove();
    const size = stage.getBoundingClientRect().width / perPx;
    screen.style.fontSize = `${Math.floor(size * 100) / 100}px`;
  }

  function stamp(ms) {
    const s = Math.floor(ms / 1000);
    return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
  }

  function say(msg) {
    status.textContent = msg;
    status.hidden = !msg;
  }
})();
