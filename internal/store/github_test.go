package store

import "testing"

func TestValidateRepoFullName(t *testing.T) {
	got, err := ValidateRepoFullName("  Octo/Hello-World.git ")
	if err != nil || got != "Octo/Hello-World" {
		t.Fatalf("got %q err=%v", got, err)
	}
	for _, bad := range []string{"", "nope", "a/b/c", "../etc/passwd", "octo/.git", "/octo/hello"} {
		if _, err := ValidateRepoFullName(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestValidateBranch(t *testing.T) {
	got, err := ValidateBranch("refs/heads/release/1.0")
	if err != nil || got != "release/1.0" {
		t.Fatalf("got %q err=%v", got, err)
	}
	for _, bad := range []string{"", "../main", "/main", "main/", "has space", "bad;rm"} {
		if _, err := ValidateBranch(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestSanitizeCommitMeta(t *testing.T) {
	got := SanitizeCommitMeta(CommitMeta{
		SHA:     "abc1234",
		Message: "first line\nsecond",
		Author:  "Ada\nLovelace",
	})
	if got.SHA != "abc1234" || got.Message != "first line" || got.Author != "Ada" {
		t.Fatalf("got %+v", got)
	}
	if SanitizeCommitMeta(CommitMeta{SHA: "not-hex"}).SHA != "" {
		t.Fatal("invalid sha must be dropped")
	}
}

func TestGitHubCloneURL(t *testing.T) {
	if GitHubCloneURL("octo/hello") != "https://github.com/octo/hello.git" {
		t.Fatal(GitHubCloneURL("octo/hello"))
	}
	if GitHubCloneURL("bad") != "" {
		t.Fatal("invalid name must be empty")
	}
}

func TestSanitizeReturnTo(t *testing.T) {
	if SanitizeReturnTo("/applications/abc") != "/applications/abc" {
		t.Fatal("path")
	}
	if SanitizeReturnTo("https://evil.test/") != "/" {
		t.Fatal("absolute")
	}
	if SanitizeReturnTo("//evil.test") != "/" {
		t.Fatal("protocol-relative")
	}
}
