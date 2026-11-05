# Snello brand assets

The mark is the letter S itself, Inter at weight 700, in blue, as Rust's mark is its R: a letter, not a drawing. Beside it the name is set in Inter at weight 650, as a word. Both are converted to outlines so no viewer needs the font. The system is the one goakt.dev uses: blue on near-black, blue on off-white, Inter for text and IBM Plex Mono for code.

| File | Use |
|---|---|
| `icon.svg` | The white S on a blue rounded square, 48 px box. The editor extension icon, the favicon, app tiles. |
| `favicon.svg` | Copy of `icon.svg` for browser tabs. |
| `avatar.svg`, `avatar.png` | The white S on blue, 1024 px. Upload the PNG as the GitHub organization avatar and for social profiles. GitHub takes PNG, JPG or GIF only, under 1 MB. |
| `logo-dark.svg` | The blue S and the name for dark backgrounds, 40 px tall. The site header. |
| `logo-light.svg` | The same for light backgrounds. |
| `wordmark-dark.svg`, `wordmark-light.svg` | The name alone. |
| `mark-dark.svg`, `mark-light.svg` | The S alone in blue, 256 px box. |
| `mark-mono.svg` | The S alone, inheriting `currentColor`. For toolbars, docs, status bars. |

## Colors

| Token | Dark | Light | Role |
|---|---|---|---|
| Background | `#0A0F1C` | `#F6F8FC` | the page |
| Text | `#E6EBF4` | `#0F172A` | the name, body text |
| Accent | `#3B82F6` | `#2563EB` | the S, links, buttons |
| Tile | `#2563EB` | `#2563EB` | the icon and avatar square |

## Type

Inter for text, IBM Plex Mono for code, loaded from Google Fonts. The name in a lockup is Inter 650 with letter-spacing −0.5 at 27 px, GoAkt's setting, so the two sites read as one hand.

## Rules

- Keep clear space around the mark equal to a third of its height.
- The S is blue, white on the tile, or `currentColor`, nothing else; never outlined, never in a ring.
- Minimum height for the lockup is 24 px. Below that, use the icon.
