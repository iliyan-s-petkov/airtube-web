# Stage 1: the frontend. Discarded entirely — no Node, no node_modules, and no
# npm-sourced code other than the built bundle reaches the runtime image.
# node:26 is the current Node major as of this writing; the digest below is
# pinned but Dependabot bumps it as the tag moves, same as Go and distroless.
FROM --platform=$BUILDPLATFORM node:26-alpine@sha256:dbaa92e5758cbbcf85d65d5403fdb530fe3442cbe8c6dbfb7ef23365450d5070 AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
# `ci` not `install`: it installs exactly the committed lockfile and fails if
# package.json and the lockfile disagree. `--ignore-scripts` because a
# postinstall hook is arbitrary code execution at build time from a
# transitive package nobody reviewed.
RUN npm ci --ignore-scripts
COPY web/ ./
# Build input: web/src/styles/theme.css imports the kit's tokens.
COPY design-kit /src/design-kit
# `npm audit --audit-level=high` runs in CI, not here. Must be invoked with
# cwd=web/: vite.config.js's `root: '.'`
# resolves against process.cwd(), not the config file's own location — a
# root-level `npm run build --prefix web` would double-nest the output to
# web/web/ and the Go embed below would find nothing.
RUN npm run build

# Native builder, cross-compiled binary. Building with --platform instead would
# run the whole toolchain under emulation: slow, and `go mod download` fails.
FROM --platform=$BUILDPLATFORM golang:1.26@sha256:6c2a5538f964f1c82f97ad14988bf05de100d922d159d0e398b54c7b0ca0c6c9 AS build
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Copied after `COPY . .` so it overwrites the .keep-only dist tree from the
# repo. Without this the embed picks up an empty tree and the image serves
# the no-JavaScript site — the exact failure the .keep design makes silent.
COPY --from=web /src/internal/web/dist ./internal/web/dist
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/airbg ./cmd/airbg

# Distroless: no shell, no package manager, no writable document root. Nothing
# dropped into the container can be executed the way anything in the legacy
# www-root/ could be (spec §4.1).
# debian13 (trixie) is current stable; debian12 is oldstable. The binary is
# statically linked (CGO_ENABLED=0), so the base contributes only CA
# certificates, /etc/passwd and timezone data — but those still carry CVE
# fixes, so the digest below is pinned but Dependabot bumps it to track the
# current major rather than the one that was current when this was written.
FROM gcr.io/distroless/static-debian13:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
COPY --from=build /out/airbg /airbg
# airbg.yaml is mandatory: there is no defaults layer in code, so the image
# must carry a config file and AIRBG_CONFIG must name it.
COPY airbg.yaml /etc/airbg/airbg.yaml
ENV AIRBG_CONFIG=/etc/airbg/airbg.yaml
# The design kit, served read-only at /design-kit/. Copied from the repo rather
# than fetched, so the running kit is exactly the committed one and the route
# fails at startup — not at first request — if design_kit.dir goes wrong.
COPY design-kit /design-kit
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/airbg"]
# `serve` is the default: the container's primary job is serving the site.
# The collector runs as a separate scheduled invocation
# (`docker run ... airbg collect`), not as the image's default command.
CMD ["serve"]
