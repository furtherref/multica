package agent

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// unprotectedExecutableWrite matches an os.WriteFile call that creates an
// executable. On Linux such a write races the forks of parallel siblings: a
// child forked while the file is still open for writing makes the later exec
// fail with ETXTBSY ("text file busy"), which is how
// TestZeroclawSessionNewMissingAliasErrorIsActionable failed on CI.
var unprotectedExecutableWrite = regexp.MustCompile(`os\.WriteFile\([^\n]*,\s*0o?7[0-7]{2}\)`)

// TestFakeExecutablesUseWriteTestExecutable keeps every fake agent CLI in this
// package on writeTestExecutable, which holds syscall.ForkLock for the write.
// New provider tests tend to copy a plain os.WriteFile, so the race returns
// unless it is rejected here.
func TestFakeExecutablesUseWriteTestExecutable(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if name == "exec_fixture_windows_test.go" {
			// The Windows helper itself; ETXTBSY does not exist there.
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, loc := range unprotectedExecutableWrite.FindAllIndex(src, -1) {
			line := 1 + strings.Count(string(src[:loc[0]]), "\n")
			t.Errorf("%s:%d writes an executable with os.WriteFile; use writeTestExecutable", name, line)
		}
	}
}
