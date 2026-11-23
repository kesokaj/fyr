package isolate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func renderSeatbeltProfile(p Policy) (string, error) {
	var b strings.Builder
	b.WriteString("(version 1)\n(deny default)\n(import \"system.sb\")\n")
	b.WriteString("(allow process-exec)\n(allow process-fork)\n(allow signal (target same-sandbox))\n")
	b.WriteString("(allow pseudo-tty)\n")
	b.WriteString("(allow file-read* file-write* file-ioctl (literal \"/dev/ptmx\"))\n")
	b.WriteString("(allow sysctl-read)\n(allow file-read-metadata)\n")
	b.WriteString("(allow mach-lookup (global-name \"com.apple.system.opendirectoryd.libinfo\") (global-name \"com.apple.trustd\") (global-name \"com.apple.trustd.agent\"))\n")
	b.WriteString("(deny mach-lookup (global-name \"com.apple.SecurityServer\") (global-name \"com.apple.coreservices.launchservicesd\") (global-name \"com.apple.pasteboard.1\") (global-name \"com.apple.lsd.mapdb\"))\n")
	if err := writeSeatbeltRules(&b, "file-read*", p.ROPaths); err != nil {
		return "", err
	}
	if err := writeSeatbeltRules(&b, "file-read* file-write* file-ioctl", p.RWPaths); err != nil {
		return "", err
	}
	if p.Network {
		b.WriteString("(allow network*)\n")
	} else {
		b.WriteString("(deny network*)\n")
	}
	return b.String(), nil
}

func writeSeatbeltRules(b *strings.Builder, op string, paths []string) error {
	seen := make(map[string]bool, len(paths)*2)
	for _, raw := range paths {
		if raw == "" {
			continue
		}
		candidates := []string{raw}
		if res, err := filepath.EvalSymlinks(raw); err == nil && res != raw {
			candidates = append(candidates, res)
		}
		for _, c := range candidates {
			clean, err := safeSeatbeltPath(c)
			if err != nil {
				return err
			}
			if seen[clean] {
				continue
			}
			seen[clean] = true
			if st, err := os.Lstat(clean); err == nil && st.Mode().IsRegular() {
				fmt.Fprintf(b, "(allow %s (literal %q))\n", op, clean)
			} else {
				fmt.Fprintf(b, "(allow %s (subpath %q) (literal %q))\n", op, clean, clean)
			}
		}
	}
	return nil
}

func safeSeatbeltPath(p string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("seatbelt path must be absolute: %q", p)
	}
	if strings.ContainsAny(p, "\"\n\r\x00") {
		return "", fmt.Errorf("seatbelt path holds unsafe character: %q", p)
	}
	return filepath.Clean(p), nil
}
