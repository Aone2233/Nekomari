# site/ — the published documentation

The product documentation, served at **https://nekomari-docs.66040321.xyz**.

## Layout

| Path | What it is |
|---|---|
| `mkdocs.yml` | Site configuration, theme, navigation |
| `content/` | Pages written **for** this site — the only Markdown here that is edited by hand |
| `theme/assets/nekomari.css` | Style overrides over mkdocs-material |
| `script/build.mjs` | Stages the sources, and checks the navigation against what exists |
| `staging/` | **Generated.** What mkdocs reads. Never edited, never committed |
| `dist/` | **Generated.** The static site. Never edited, never committed |

## Two kinds of page, and why that is the central decision

`content/` holds documentation written for readers who want to install and operate Nekomari.

The repository's `docs/` directory holds the project's **engineering record** — reviews, post-mortems,
roadmaps, release follow-ups — and that material is deliberately not published. The reasoning is in the site
itself, under "仓库内其它文档", and it comes down to three things: the readers are different, writing for an
audience changes what gets written, and a dated review read as current documentation is worse than absent.

Two documents are maintained in `docs/` **and** published here, because they are genuinely useful to an
operator and already written for one:

- `docs/IP-INFO-API.md`
- `docs/THIRD-PARTY-LICENSES.md`

They are listed in `script/build.mjs` and copied at build time. Copying them into `content/` would give the
repository two versions of each, and one of them would always be stale.

## Building

```bash
node site/script/build.mjs stage                          # populate site/staging
node site/script/build.mjs check                          # navigation vs. what exists
docker run --rm -v "$PWD/site:/docs" -w /docs \
  squidfunk/mkdocs-material:latest build --strict         # produce site/dist
```

`--strict` is used so a broken internal link fails the build instead of shipping.

The same three steps run in `deploy/docs/build.sh` on the server, which is what the container image is built
from — see `deploy/docs/README.md`.

## Adding a page

1. Write it under `content/` (or add it to `REUSED` in `script/build.mjs` if it lives in `docs/`)
2. Add it to `nav:` in `mkdocs.yml`
3. `node site/script/build.mjs stage && node site/script/build.mjs check`

Step 3 is not optional: the check is what turns "the build succeeded" into "every page the navigation names
exists", which a strict build alone does not catch for a page that is named but missing.
