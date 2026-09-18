# web/static

Files the site serves as-is. `web/player/build.sh` copies the PNGs into
`dist/` (the Worker's static-asset root), so `/mascot.png` is a Cloudflare
asset request and never reaches the Worker.

## mascot.png

The site's avatar: a mint-haired chibi in headphones, in the flat
"minimal bot icon" style. Generated 2026-09-19 with fal.ai
`fal-ai/flux-pro/v1.1` (seed 23, `square_hd`), from the prompt in
`mascot.prompt.txt`; the fal response is kept in the session scratchpad.
Post-processing: the model's grey ground was replaced with the page's
`#0e1014`, the head cropped square, and the result downscaled to 256 px
(32 KB). The full 1024 px original and the other candidates live in the
lead's scratchpad under `mascot/`.

Use terms: FLUX 1.1 [pro] output is licensed for commercial use under
Black Forest Labs' API terms as fal.ai relays them — check the fal model
page before reusing the image anywhere other than this site.
