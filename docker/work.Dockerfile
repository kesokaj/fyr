# The image `fyr work`'s docker runner runs each worker's agy session in
# (SPEC-fyr-work § --runner docker). It carries a linux fyr, the machine's
# Go toolchain and Node LTS, and no agy: the verb mounts a linux agy read-only.
# Build it from the checkout with this line, also in SPEC-fyr-work and
# printed by fyr work's refusals (TestBuildLinePinned); `docker buildx
# build`, so a missing BuildKit fails instead of falling back to a builder
# that ignores work.Dockerfile.dockerignore. fyr builds fyr-work:local from
# its embedded copy when the daemon is running and never pulls an image.
# Build: docker buildx build --load -f docker/work.Dockerfile --build-arg GO_MINOR="$(go env GOVERSION | sed -E 's/^go([0-9]+\.[0-9]+).*/\1/')" --build-arg FYR_VERSION="$(fyr version | cut -d' ' -f2)" -t fyr-work:local .
#
# GO_MINOR is the machine's Go major.minor, not the go.mod line's: the
# worker's own tests need the machine's Go, and the go line is a minimum.
ARG GO_MINOR

FROM golang:${GO_MINOR}-trixie AS build
ARG FYR_VERSION
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
# Named copies, never `COPY . .`: the layer holds only what the binary is
# built from under any builder (TestImageContextAllowList pins this list to
# embed.go and the ignore file).
COPY *.go ./
COPY cmd cmd
COPY internal internal
COPY context context
COPY skills skills
COPY agents agents
COPY commands commands
COPY rules rules
COPY docker docker
RUN case "${FYR_VERSION}" in ""|*[!A-Za-z0-9._+-]*) echo "FYR_VERSION must be fyr version's stamp" >&2; exit 1 ;; esac && \
    CGO_ENABLED=0 go build -trimpath -ldflags "-X main.version=${FYR_VERSION}" -o /out/fyr ./cmd/fyr

FROM golang:${GO_MINOR}-trixie
COPY --from=build /out/fyr /usr/local/bin/fyr
# Official Node 24 LTS (trixie-slim), pinned by multi-arch index digest so
# a TypeScript worker can run npm ci and npm test in docker (Item 13);
# Playwright browsers stay on the host. BuildKit dereferences a symlink named
# directly as a COPY source, which would duplicate the node binary at nodejs
# and break npm's relative ../lib/cli.js require, so copy the tree and link.
COPY --from=node:24-trixie-slim@sha256:0820eaa85e07d0490446dd01d14707e17e6108a36988cf374899cd057eb3812d /usr/local/bin/node /usr/local/bin/node
COPY --from=node:24-trixie-slim@sha256:0820eaa85e07d0490446dd01d14707e17e6108a36988cf374899cd057eb3812d /usr/local/include/node /usr/local/include/node
COPY --from=node:24-trixie-slim@sha256:0820eaa85e07d0490446dd01d14707e17e6108a36988cf374899cd057eb3812d /usr/local/lib/node_modules /usr/local/lib/node_modules
RUN ln -s /usr/local/bin/node /usr/local/bin/nodejs && \
    ln -s ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm && \
    ln -s ../lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx && \
    ln -s ../lib/node_modules/corepack/dist/corepack.js /usr/local/bin/corepack
# Pinned to sandbox.ImagePath by TestImagePathPinned: the box prepends the
# sandbox's bin directory to exactly this. GOPATH is the sandbox home's, so
# /go/bin is left out.
ENV PATH=/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
# The worker's TZ needs tzdata: a base without it fails here instead of
# running every worker in UTC unseen (workers D144; TestImageZoneGuard).
RUN test -f /usr/share/zoneinfo/Etc/UTC
# Node, npm, npx and corepack must execute on the final stage's glibc and
# resolve their symlinks into /usr/local/lib/node_modules (Item 13; TestImageNodePinned).
RUN node --version && nodejs --version && npm --version && npx --version && corepack --version
# On Apple Silicon (arm64), include amd64 glibc so an x86_64 agy-linux runs
# via Rosetta 2 under Colima --vz-rosetta while Go stays native arm64 (D8).
RUN if [ "$(dpkg --print-architecture)" = "arm64" ]; then \
        dpkg --add-architecture amd64 && \
        apt-get update && \
        apt-get install -y --no-install-recommends libc6:amd64 && \
        rm -rf /var/lib/apt/lists/*; \
    fi
# Not root by default: the verb passes the operator's --user, and a run
# without it still drops root.
USER 65532:65532
