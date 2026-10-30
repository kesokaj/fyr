package sandbox

// ImagePath is the PATH of docker/work.Dockerfile's image, which a
// docker-mode box (Phase 7b-1 of PLAN-2026-10-29-workers) will put after the
// sandbox's bin directory so the fyr wrapper and its --home pin come first. The host cannot read the image's PATH
// without starting it, so the Dockerfile sets this exact value
// (TestImagePathPinned).
const ImagePath = "/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
