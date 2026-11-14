# Compression benchmark set

Fixed images, committed so every checkout measures the same pixels. There are
at least two images in each class AnyPic sees. Synthetic fixtures make the
numbers reproducible; real ones keep them honest.

| file | class | kind | source |
| --- | --- | --- | --- |
| `screenshot.png` (1280×800) | screenshot | synthetic | generated: flat UI, hairlines, solid fills |
| `screenshot-real.png` (1220×820) | screenshot | real | `docs/screenshot.png`, AnyPic's own UI |
| `photo.jpg` (1200×800) | photo | synthetic | generated: gradients, glow, grain |
| `photo-earth.jpg` (1005×1280) | photo | real | NASA/JPL PIA00342 “Earth and Moon”, public domain |
| `photo-earth2.jpg` (640×640) | photo | real | NASA/JPL PIA00122 Earth, public domain |
| `transparent.png` (512×384) | transparent | synthetic | generated: alpha disc with anti-aliased rim |
| `logo.png` (256×256) | transparent | real | `src/assets/logo-dark.png`, project asset |
| `animated.gif` (320×240, 10 frames) | animation | synthetic | generated: moving disc over a gradient |
| `animated-real.gif` (360×240, 6 frames) | animation | derived | pan across `docs/screenshot.png` |

Nine images in four classes: screenshots 2 (one real), photos 3 (two real),
transparent 2 (one real), animations 2 (one derived from a real screenshot).
The synthetic images keep the numbers reproducible; the real ones keep the
encoder from being measured only on generated content.

NASA JPL images are U.S. government works in the public domain; the catalog
pages are <https://photojournal.jpl.nasa.gov/catalog/PIA00342> and
<https://photojournal.jpl.nasa.gov/catalog/PIA00122>.

Synthetic images plus `animated-real.gif` are regenerated deterministically:

```sh
go run ./testdata/benchmark/generate   # from the repository root
```

`docs/screenshot.png`, `src/assets/logo-dark.png`, and the two NASA JPEGs are
copied in as real references; the generator does not overwrite them.

The whole set is `du` 832 KB, well under 1 MB, so `TestBenchmarkSet` runs
in the normal suite. It compresses each file with the formats AnyPic offers
(19 cases) and records size, SSIM, the `ssimMeasured` flag, and any target-size
quality. It compares against `baseline.json`: a result may not grow by more
than 2% or lose more than 0.002 SSIM.

Quality tiers (0.98 / 0.92) are calibrated from this set at quality 82:

| cluster | SSIM |
| --- | --- |
| real screenshots + logo | 0.999–0.991 |
| real photos (JPEG q60 and WebP q82) | 0.994–0.987 |
| synthetic photo + target-size WebP | 0.933–0.942 |

The measured rows behind those ranges: real screenshot WebP 0.9994 / 0.9987,
logo WebP 0.9908, real photos WebP q82 0.9865–0.9909 and JPEG q60
0.9909–0.9944, synthetic photo WebP 0.9329, and the target-size result 0.9421.
0.98 keeps the first two clusters in "nearly identical" without putting a q82
photo on the boundary; 0.92 keeps the synthetic photo in "slight loss" with
headroom on both sides. A measured zero is a real score and still gets a tier;
only `ssimMeasured=false` means "not measured".

Animations record `ssimMeasured: false`; they keep every frame and are shown
as “quality not measured”, not as a zero score.

- Re-record the baseline after a deliberate encoder change:
  `go test -run TestBenchmarkSet -update-benchmark .`
- Read the table: `go test -run TestBenchmarkSet -v .`

A baseline change is part of the review: say in the commit what changed in the
encoder parameters and why the new size/quality trade is better.
