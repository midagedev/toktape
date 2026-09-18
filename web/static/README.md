# web/static

Files the site serves as-is. `web/player/build.sh` copies the PNGs and WebPs
into `dist/` (the Worker's static-asset root), so `/mascot.png` and the rest
are Cloudflare asset requests and never reach the Worker.

## The mascot

One character: a small chibi with a mint (#86c2b3) bob, big headphones with
mint ear cups, a dark hoodie, and a thick white sticker outline. Generated
2026-09-19 with fal.ai `fal-ai/flux-pro/v1.1` (`square_hd`, one seed per
pose) from the prompt in `mascot.prompt.txt` plus a pose clause. The lead
picked the poses off contact sheets; the fal responses and the full 1024 px
originals stay in the session scratchpad under `mascot/`.

| file | pose | seed | where it shows |
|---|---|---|---|
| `mascot.png` | face, front | 21 | brand row avatar (36 px circle), 256 px on the page's ground |
| `favicon.png` | the same | 21 | 64 px |
| `apple-touch-icon.png` | the same | 21 | 180 px |
| `mascot-sit.webp` | sitting, hugging a glowing cassette | 8 | the front page's empty state |
| `mascot-peek.webp` | peeking over an edge, head and hands | 5 | the front page's bottom-right margin (≥78rem wide) |
| `mascot-sleep.webp` | asleep on a cassette | 12 | a run page's bottom-right margin (≥78rem wide) |
| `mascot-wave.webp` | standing, waving, cassette in the other hand | 30 | the front page's top-right corner (small beside the brand row on a phone) |

Post-processing (PIL, in the lead's session): the model's ground colours were
sampled at the border and flood-filled to alpha through the sticker outline,
the figure cropped to its bounds, the face composited onto `#0e1014`, and the
poses saved as WebP q88 (23–51 KB each) because their PNGs were 140–340 KB.

Use terms: FLUX 1.1 [pro] output is licensed for commercial use under Black
Forest Labs' API terms as fal.ai relays them — check the fal model page
before reusing the images anywhere other than this site.
