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
| `mascot.png` | sitting, eyes closed, hugging a cassette — the first sticker the user picked ("최초 이미지") | 2483962039 (d_fluxpro_chibi) | brand row avatar (36 px circle), 256 px on the page's ground; the square window covers head and tape |
| `favicon.png` | the same | — | 64 px |
| `apple-touch-icon.png` | the same | — | 180 px |
| `mascot-sit.webp` | sitting, hugging a glowing cassette | 8 | the front page's empty state |
| `mascot-peek.webp` | peeking over an edge, head and hands | 5 | the front page's bottom-right margin (≥78rem wide) |
| `mascot-sleep.webp` | asleep on a cassette | 12 | a run page's bottom-right margin (≥78rem wide) |
| `mascot-wave.webp` | standing, waving, cassette in the other hand | 30 | the front page's top-right corner (small beside the brand row on a phone) |

## The link preview

`og.png` (1200×675) is what Reddit, X, Discord and Slack show under a link to
the front page. It is not hand-painted and not a screenshot of the site: it is
`og.html` in this directory, rendered by headless Chrome, so it can be redone
from a checkout when the wording changes.

```sh
cd web/static
"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
  --headless --disable-gpu --hide-scrollbars --allow-file-access-from-files \
  --window-size=1200,675 --virtual-time-budget=6000 \
  --screenshot=og.png "file://$PWD/og.html"
```

`--allow-file-access-from-files` is required: the page loads `mascot-sit.webp`
from beside it and JetBrains Mono from `assets/fonts`, which is the face the
run cards are set in, so the plate and a card read as one product.

1200×675 rather than 1200×630 for the reason the card is that size
(`internal/card/png/theme.go`): it is exact 16:9, which is what the platforms
crop a large image to. Every line on it is sized to survive the ~600 px a
desktop timeline actually gives it — measured, not assumed — and the single
accent on the plate is the install command's border, because that is the one
block a reader is meant to act on.

Post-processing (PIL, in the lead's session): the model's ground colours were
sampled at the border and flood-filled to alpha through the sticker outline,
the figure cropped to its bounds, the face composited onto `#0e1014`, and the
poses saved as WebP q88 (23–51 KB each) because their PNGs were 140–340 KB.

Use terms: FLUX 1.1 [pro] output is licensed for commercial use under Black
Forest Labs' API terms as fal.ai relays them — check the fal model page
before reusing the images anywhere other than this site.
