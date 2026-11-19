package harness

import "strings"

// OriginKind classifies what a manifest's repo value names.
type OriginKind int

const (
	// OriginPath is a tree on disk: a checkout or a plain copy.
	OriginPath OriginKind = iota
	// OriginRelease is the harness built into a release binary ("embedded:vX.Y.Z").
	OriginRelease
	// OriginZip is a repository zip the operator passed to update --from-zip
	// ("zip:sha256:<sum>;<trust>"), whose unpacked tree is gone (ADR-038).
	OriginZip
)

// Origin is a manifest's repo value, parsed once for every reader (IN6).
type Origin struct {
	Kind    OriginKind
	Path    string // OriginPath: the tree
	Release string // OriginRelease: the release tag
	Sum     string // OriginZip: the archive's sha256
	Trust   string // OriginZip: "unverified", "verified vX.Y.Z", or "" for a manifest from before the verdict was recorded
}

// ParseOrigin reads a manifest repo value.
func ParseOrigin(repo string) Origin {
	if release, ok := strings.CutPrefix(repo, EmbeddedPrefix); ok {
		return Origin{Kind: OriginRelease, Release: release}
	}
	if rest, ok := strings.CutPrefix(repo, ZipPrefix+"sha256:"); ok {
		sum, trust, _ := strings.Cut(rest, ";")
		return Origin{Kind: OriginZip, Sum: sum, Trust: trust}
	}
	return Origin{Kind: OriginPath, Path: repo}
}

// Origin is the parsed form of the manifest's repo value.
func (s State) Origin() Origin { return ParseOrigin(s.Repo) }

// ShortSum is the first 12 hex digits of a zip origin's sha256, enough to
// tell zips apart in a message.
func (o Origin) ShortSum() string {
	if len(o.Sum) > 12 {
		return o.Sum[:12]
	}
	return o.Sum
}

// OriginString is what the manifest records as this source: the explicit
// Origin when set (a zip's archive), the release for the embedded files, the
// tree's path otherwise.
func (s Source) OriginString() string {
	switch {
	case s.Origin != "":
		return s.Origin
	case s.Dir == "":
		return EmbeddedPrefix + s.Version
	}
	return s.Dir
}

// ZipOrigin is the manifest value for a zip install: the archive's sha256 and
// the verdict, so status can tell a verified install from an unverified one
// after the output has scrolled away.
func ZipOrigin(sum, verifiedTag string) string {
	if verifiedTag != "" {
		return ZipPrefix + "sha256:" + sum + ";verified " + verifiedTag
	}
	return ZipPrefix + "sha256:" + sum + ";unverified"
}
