# The image `fyr work --runner docker` runs each worker's agy session in
# (SPEC-fyr-work § The worker image). It carries a linux fyr and the
# machine's Go toolchain, and no agy: the verb mounts a linux agy read-only.
# Build it from the checkout with this line, also in SPEC-fyr-work and
# printed by fyr work's refusals (TestBuildLinePinned); `docker buildx
# build`, so a missing BuildKit fails instead of falling back to a builder
# that ignores work.Dockerfile.dockerignore. The verb never builds or pulls it.
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
RUN case "${FYR_VERSION}" in ""|*[!A-Za-z0-9._+-]*) echo "FYR_VERSION must be fyr version's stamp" >&2; exit 1 ;; esac && \
    CGO_ENABLED=0 go build -trimpath -ldflags "-X main.version=${FYR_VERSION}" -o /out/fyr ./cmd/fyr

FROM golang:${GO_MINOR}-trixie
COPY --from=build /out/fyr /usr/local/bin/fyr
# Pinned to sandbox.ImagePath by TestImagePathPinned: the box prepends the
# sandbox's bin directory to exactly this. GOPATH is the sandbox home's, so
# /go/bin is left out.
ENV PATH=/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
# The worker's TZ needs tzdata: a base without it fails here instead of
# running every worker in UTC unseen (workers D144; TestImageZoneGuard).
RUN test -f /usr/share/zoneinfo/Etc/UTC
# Not root by default: the verb passes the operator's --user, and a run
# without it still drops root.
USER 65532:65532
