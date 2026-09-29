# Deploying the theme

**Editing the theme source changes nothing until it is deployed.** The source repository and the *installed*
copy are two different things, and getting that wrong is the most repeated mistake in this project's recent
history:

* the **background images** were added to the installed copy by hand and were not in the source, so rebuilding
  from source and replacing the installation removed them — the front page lost its background for a day
* the **traffic fix** was committed to the source, described as done, and never reached the site at all; the
  page kept showing the wrong figure until someone looked at it again

Both were done by hand. So:

```bash
# on the panel host, from a checkout or an unpacked copy of the theme source
export NVM_DIR="$HOME/.nvm"; . "$NVM_DIR/nvm.sh"
SOURCE=$HOME/nekomari-theme-src sh deploy/theme/deploy.sh
```

## What it does

| step | why |
|---|---|
| stages a **copy** of the source | what gets deployed is never the working tree with an uncommitted experiment in it |
| names every build input individually | a broad copy would carry the previous `dist/` along and a stale asset could survive into the new one |
| `npm install` only when asked (`--fresh-install`) | it is the slow part and the dependency tree rarely changes |
| **verifies before replacing** | `dist/index.html` present and a non-empty `assets/` — a build that "succeeded" without either would otherwise be swapped in and take the site down |
| backs up `dist`, stages the replacement **beside** the live copy, then swaps | the live directory is never removed until a complete replacement exists |
| prints the content-hashed entry bundle before and after | an unchanged hash means the swap did not take effect |

The order matters. The hand-run version was:

```bash
sudo rm -rf "$T/dist" && sudo mv "$T/dist.new" "$T/dist"
```

If the unpack failed, `dist` was gone. `set -e` was the only thing that stopped it from happening, and relying
on that is not a plan. The script now builds and verifies first, and keeps the previous version at
`theme-backups/<theme>-pre-deploy-<timestamp>/dist`.

## The hand-added files

An installed theme is not only a build product. The current deployment carries **eight background images** under
`dist/assets/` that were uploaded through the panel and are not produced by the build. They live in the theme's
**source** as well (`public/assets/bg-*.{jpg,jpeg,png}`, with `docs/background-assets.md` explaining what they
are), so a build from this source keeps them — but a build from the upstream theme source would not, and the
symptom is a missing background with a single 404 in the console.

## Verifying

```bash
curl -s  http://127.0.0.1:25774/ | grep -oE 'assets/index-[A-Za-z0-9_-]+\.js' | head -1   # origin
curl -sI https://<panel-host>/   | grep -i cf-cache-status                                  # edge
```

The panel sets `Cache-Control: max-age=60, must-revalidate` on the shell, so a swap becomes visible within a
minute. The asset filenames are content-hashed, so the new bundle is fetched automatically once the document
references it — no cache purge needed, which is why the earlier stylesheet-cache incident does not repeat here.

Compare **origin** and **edge** separately: when only the edge differs, the deployment worked and the cache has
not expired yet.