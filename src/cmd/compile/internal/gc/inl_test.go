// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gc

import (
	"bufio"
	"internal/testenv"
	"io"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// TestIntendedInlining tests that specific runtime functions are inlined.
// This allows refactoring for code clarity and re-use without fear that
// changes to the compiler will cause silent performance regressions.
func TestIntendedInlining(t *testing.T) {
	if testing.Short() && testenv.Builder() == "" {
		t.Skip("skipping in short mode")
	}
	testenv.MustHaveGoRun(t)
	t.Parallel()

	// want is the list of function names (by package) that should
	// be inlined.
	want := map[string][]string{
		"runtime": {
			"tophash",
			"add",
			"addb",
			"subtractb",
			"(*bmap).keys",
			"bucketShift",
			"bucketMask",
			"fastrand",
			"noescape",
		},
		"unicode/utf8": {
			"FullRune",
			"FullRuneInString",
			"RuneLen",
			"ValidRune",
		},
	}

	if runtime.GOARCH != "386" {
		// nextFreeFast calls sys.Ctz64, which on 386 is implemented in asm and is not inlinable.
		// We currently don't have midstack inlining so nextFreeFast is also not inlinable on 386.
		// So check for it only on non-386 platforms.
		want["runtime"] = append(want["runtime"], "nextFreeFast")
	}

	notInlinedReason := make(map[string]string)
	pkgs := make([]string, 0, len(want))
	for pname, fnames := range want {
		pkgs = append(pkgs, pname)
		for _, fname := range fnames {
			notInlinedReason[pname+"."+fname] = "unknown reason"
		}
	}

	args := append([]string{"build", "-a", "-gcflags=-m -m"}, pkgs...)
	cmd := testenv.CleanCmdEnv(exec.Command(testenv.GoToolPath(t), args...))
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	cmdErr := make(chan error, 1)
	go func() {
		cmdErr <- cmd.Run()
		pw.Close()
	}()
	scanner := bufio.NewScanner(pr)
	curPkg := ""
	canInline := regexp.MustCompile(`: can inline ([^ ]*)`)
	cannotInline := regexp.MustCompile(`: cannot inline ([^ ]*): (.*)`)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "# ") {
			curPkg = line[2:]
			continue
		}
		if m := canInline.FindStringSubmatch(line); m != nil {
			fname := m[1]
			delete(notInlinedReason, curPkg+"."+fname)
			continue
		}
		if m := cannotInline.FindStringSubmatch(line); m != nil {
			fname, reason := m[1], m[2]
			fullName := curPkg + "." + fname
			if _, ok := notInlinedReason[fullName]; ok {
				// cmd/compile gave us a reason why
				notInlinedReason[fullName] = reason
			}
			continue
		}
	}
	if err := <-cmdErr; err != nil {
		t.Fatal(err)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	for fullName, reason := range notInlinedReason {
		t.Errorf("%s was not inlined: %s", fullName, reason)
	}
}