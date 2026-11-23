package check

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	// Matches `TestFoo` (`path/to/file_test.go[:N]`) or `TestFoo` in `path/to/file_test.go[:N]`.
	testThenPathRe = regexp.MustCompile("`(Test[A-Za-z0-9_]+)`\\s*(?:\\(\\s*`([^`\\s]+_test\\.go)(?::[0-9]+(?:-[0-9]+)?)?`\\s*\\)|in\\s+`([^`\\s]+_test\\.go)(?::[0-9]+(?:-[0-9]+)?)?`)")
	// Matches (`path/to/file_test.go[:N]`): `TestFoo`.
	parenPathColonTestRe = regexp.MustCompile("\\(\\s*`([^`\\s]+_test\\.go)(?::[0-9]+(?:-[0-9]+)?)?`\\s*\\)\\s*:\\s*`(Test[A-Za-z0-9_]+)`")
	// Matches `path/to/file_test.go[:N]` (... `TestFoo` ...).
	pathThenParenRe = regexp.MustCompile("`([^`\\s]+_test\\.go)(?::[0-9]+(?:-[0-9]+)?)?`\\s*\\(([^)]*)\\)")
	backtickTestRe  = regexp.MustCompile("`(Test[A-Za-z0-9_]+)`")
)

type testAttrPair struct {
	testName string
	testFile string
}

// testAttributionRule checks adjacent `Test*` <-> `*_test.go` pairs in TDD.md
// and features/*.md so misattributed test file citations fail check (F-017).
func (k *checker) testAttributionRule(docsFiles []string) []Finding {
	if !k.hasTrackedGoTests() {
		return nil
	}
	var out []Finding
	for _, p := range docsFiles {
		if p != "TDD.md" && path.Dir(p) != "features" {
			continue
		}
		data, err := k.readFile(filepath.FromSlash(p))
		if err != nil {
			continue
		}
		out = append(out, k.checkTestAttrDoc(p, string(data))...)
	}
	return out
}

func (k *checker) checkTestAttrDoc(where, text string) []Finding {
	var out []Finding
	unfenced(text, func(line int, l string) bool {
		seen := map[testAttrPair]bool{}
		for _, pair := range extractTestAttrPairs(l) {
			if seen[pair] {
				continue
			}
			seen[pair] = true
			if !k.isTracked(pair.testFile) {
				continue
			}
			if !k.testDeclaredIn(pair.testFile, pair.testName) {
				out = append(out, Finding{
					Where: where,
					Line:  line,
					Rule:  "test-ref",
					Text:  fmt.Sprintf("`%s`: not declared in `%s`", pair.testName, pair.testFile),
				})
			}
		}
		return true
	})
	return out
}

func extractTestAttrPairs(line string) []testAttrPair {
	var out []testAttrPair
	for _, m := range testThenPathRe.FindAllStringSubmatch(line, -1) {
		fp := m[2]
		if fp == "" {
			fp = m[3]
		}
		out = append(out, testAttrPair{testName: m[1], testFile: cleanTestPath(fp)})
	}
	for _, m := range parenPathColonTestRe.FindAllStringSubmatch(line, -1) {
		out = append(out, testAttrPair{testName: m[2], testFile: cleanTestPath(m[1])})
	}
	for _, m := range pathThenParenRe.FindAllStringSubmatch(line, -1) {
		if strings.Contains(m[2], "_test.go") {
			continue
		}
		fp := cleanTestPath(m[1])
		for _, tm := range backtickTestRe.FindAllStringSubmatch(m[2], -1) {
			out = append(out, testAttrPair{testName: tm[1], testFile: fp})
		}
	}
	return out
}

func cleanTestPath(p string) string {
	return strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(p)), "./")
}
