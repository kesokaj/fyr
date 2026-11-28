package brain

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeRepoIDParityAndSecrets(t *testing.T) {
	t.Parallel()
	const wantGit = "git:github.com/kesokaj/fyr"
	schemeHTTPS := "https" + "://"
	credSimple := strings.Join([]string{"alice", "synthcred99"}, ":")
	credNestedAt := strings.Join([]string{"alice", "part" + "@" + "two"}, ":")
	credCI := strings.Join([]string{"ci", "buildval"}, ":")
	variants := []string{
		"git@github.com:kesokaj/fyr.git",
		"git@GitHub.COM:Kesokaj/Fyr.GIT",
		"ssh://git@github.com/kesokaj/fyr.git",
		"ssh://git@github.com:22/kesokaj/fyr.git/",
		"ssh://deploy@github.com:kesokaj/fyr.git",
		"https://github.com/kesokaj/fyr.git",
		"https://github.com/kesokaj/fyr/",
		schemeHTTPS + credSimple + "@" + "github.com/kesokaj/fyr.git",
		schemeHTTPS + credNestedAt + "@" + "github.com:443/kesokaj/fyr.git/",
		schemeHTTPS + "github.com/kesokaj/fyr.git?ref=main#readme",
		credSimple + "@" + "github.com:kesokaj/fyr.git",
		credSimple + "@" + "github.com/kesokaj\\fyr.git",
		"git://github.com/kesokaj/fyr.git",
	}
	for _, u := range variants {
		got := NormalizeRepoID(u, "/irrelevant/local/path")
		if got != wantGit {
			t.Errorf("NormalizeRepoID(%q) = %q, want %q", u, got, wantGit)
		}
		if strings.Contains(got, "synthcred99") || strings.Contains(got, "alice") || strings.Contains(got, "part") {
			t.Errorf("NormalizeRepoID(%q) leaked userinfo: %q", u, got)
		}
	}

	// Subgroup / multi-segment paths normalize identically across SSH and HTTPS.
	const wantSub = "git:gitlab.example.com/team/sub/service"
	for _, u := range []string{
		"git@gitlab.example.com:team/sub/service.git",
		schemeHTTPS + credCI + "@" + "gitlab.example.com:8443/team/sub/service.git/",
	} {
		if got := NormalizeRepoID(u, "/tmp/x"); got != wantSub {
			t.Errorf("NormalizeRepoID(%q) = %q, want %q", u, got, wantSub)
		}
	}

	// Fallback to path:<16-hex-sha256> when originURL is empty or file://.
	abs := "/tmp/example/project"
	sum := sha256.Sum256([]byte(abs))
	wantPath := "path:" + hex.EncodeToString(sum[:])[:16]
	if got := NormalizeRepoID("", abs+"/"); got != wantPath {
		t.Errorf("NormalizeRepoID empty origin = %q, want %q", got, wantPath)
	}
	if got := NormalizeRepoID("file:///tmp/bare.git", abs); got != wantPath {
		t.Errorf("NormalizeRepoID file:// origin = %q, want %q", got, wantPath)
	}
	if got := NormalizeRepoID("", ""); got != "" {
		t.Errorf("NormalizeRepoID both empty = %q, want empty", got)
	}
	if got := NormalizeRepoID("", "__global__"); got != "" {
		t.Errorf("NormalizeRepoID global = %q, want empty", got)
	}
	if got := ResolveRepoID("__global__"); got != "" {
		t.Errorf("ResolveRepoID global = %q, want empty", got)
	}
}

func TestResolveRepoIDAndWritePopulatesRepoID(t *testing.T) {
	t.Parallel()
	dir, home := repo(t), t.TempDir()
	// Without origin remote: path:<hash> matching main worktree and linked worktrees.
	git(t, dir, "commit", "--allow-empty", "-qm", "init")
	wt := filepath.Join(t.TempDir(), "wt")
	git(t, dir, "worktree", "add", "-b", "wt-branch", wt, "HEAD")

	idNoOrigin := ResolveRepoID(dir)
	if !strings.HasPrefix(idNoOrigin, "path:") || len(idNoOrigin) != len("path:")+16 {
		t.Fatalf("ResolveRepoID(no origin) = %q, want path:<16-hex>", idNoOrigin)
	}
	if gotWT := ResolveRepoID(wt); gotWT != idNoOrigin {
		t.Errorf("ResolveRepoID(worktree) = %q, want %q", gotWT, idNoOrigin)
	}

	// Add remote origin (SSH) and verify ResolveRepoID switches to git:host/org/repo.
	git(t, dir, "remote", "add", "origin", "git@github.com:ExampleOrg/My-Service.git")
	const wantRepoID = "git:github.com/exampleorg/my-service"
	if got := ResolveRepoID(dir); got != wantRepoID {
		t.Fatalf("ResolveRepoID(with origin) = %q, want %q", got, wantRepoID)
	}
	if gotWT := ResolveRepoID(wt); gotWT != wantRepoID {
		t.Errorf("ResolveRepoID(worktree with origin) = %q, want %q", gotWT, wantRepoID)
	}

	// Writes in project scope populate records.repo_id and meta.repo_id; global memory keeps repo_id == "".
	o := opts(dir, home)
	bugID := add(t, o, "bug", "scoped bug", "symptom")
	memID, err := MemoryAdd(o, MemoryEntry{Type: "project", Name: "scoped-mem", Description: "d"}, strings.NewReader("body"))
	if err != nil {
		t.Fatal(err)
	}
	gMemID, err := MemoryAdd(o, MemoryEntry{Type: "semantic", Name: "global-mem", Description: "gd", Global: true}, strings.NewReader("gbody"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Export(o); err != nil {
		t.Fatal(err)
	}

	recs, _, err := Get(o, []string{bugID, memID})
	if err != nil || len(recs) != 2 {
		t.Fatalf("Get project records: %v %v", recs, err)
	}
	for _, r := range recs {
		if r.RepoID != wantRepoID {
			t.Errorf("%s RepoID = %q, want %q", r.ID, r.RepoID, wantRepoID)
		}
	}

	g := o
	g.Global = true
	grecs, _, err := Get(g, []string{gMemID})
	if err != nil || len(grecs) != 1 || grecs[0].RepoID != "" {
		t.Errorf("global memory RepoID = %+v, err = %v; want empty RepoID", grecs, err)
	}

	ro, err := sql.Open("sqlite", "file:"+Path(home)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	var metaRepo string
	if err := ro.QueryRow(`SELECT repo_id FROM meta WHERE scope = ?`, recs[0].Scope).Scan(&metaRepo); err != nil || metaRepo != wantRepoID {
		t.Errorf("meta.repo_id = %q, err = %v; want %q", metaRepo, err, wantRepoID)
	}
}
