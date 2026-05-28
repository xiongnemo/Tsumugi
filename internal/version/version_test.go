package version

import "testing"

func TestStringUsesInjectedMetadata(t *testing.T) {
	oldVersion, oldCommit, oldBranch, oldDirty := version, commit, branch, dirty
	t.Cleanup(func() {
		version, commit, branch, dirty = oldVersion, oldCommit, oldBranch, oldDirty
	})
	version = "v1.2.3"
	branch = "feature/test branch"
	commit = "1234567890abcdef"
	dirty = "true"

	got := String()
	want := "v1.2.3-feature-test-branch-1234567890ab-dirty"
	if got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}
