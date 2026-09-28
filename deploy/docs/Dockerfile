# The published documentation site, built from this repository.
#
# Two stages: build with mkdocs-material, then serve the result with nginx. The build stage is separate so the
# published image contains no Python, no pip cache and no source Markdown — only HTML, CSS and JS.
#
# NODE is needed because the staging step is a Node script. It runs `stage` and `check`; the `check` is what
# turns "the build succeeded" into "every page the navigation names actually exists".

FROM node:22-alpine AS stage
WORKDIR /src
COPY site/ ./site/
COPY docs/IP-INFO-API.md ./docs/IP-INFO-API.md
COPY docs/THIRD-PARTY-LICENSES.md ./docs/THIRD-PARTY-LICENSES.md
COPY README.md ./README.md
RUN node site/script/build.mjs stage && node site/script/build.mjs check

FROM squidfunk/mkdocs-material:9 AS build
WORKDIR /docs
COPY --from=stage /src/site/ /docs/
# `--strict` fails the build on a broken internal link. Publishing a site with dead links is worse than
# failing the build, because the dead link is only found by a reader.
RUN mkdocs build --strict

FROM nginx:alpine AS serve
COPY --from=build /docs/dist /usr/share/nginx/html
COPY deploy/docs/nginx-docs.conf /etc/nginx/conf.d/default.conf
# The image is immutable content served by nginx, so the health check is simply "can it answer".
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s \
  CMD wget -qO- http://127.0.0.1/ >/dev/null 2>&1 || exit 1
EXPOSE 80
