package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// runInstall copies the running executable into a directory that is on the
// user's PATH so they can invoke `nlwproxy` from any terminal. It never needs
// administrator rights: on Windows it targets %LOCALAPPDATA%\nlwproxy\bin and
// on Unix ~/.local/bin. The chosen directory is created if missing, and the
// command prints a note if it is not yet on PATH.
func runInstall(args []string, out, errOut io.Writer) int {
	fs := commandFlags("install", errOut)
	dir := fs.String("dir", "", "target install directory (default: user bin on PATH)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	src, err := os.Executable()
	if err != nil {
		fmt.Fprintln(errOut, "install: cannot locate current executable:", err)
		return 1
	}
	src, _ = filepath.EvalSymlinks(src)

	target := *dir
	if target == "" {
		target = defaultInstallDir()
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		fmt.Fprintln(errOut, "install: cannot create target dir:", err)
		return 1
	}

	name := "nlwproxy"
	if runtime.GOOS == "windows" {
		name = "nlwproxy.exe"
	}
	dst := filepath.Join(target, name)

	if err := copyExecutable(src, dst); err != nil {
		fmt.Fprintln(errOut, "install: copy failed:", err)
		return 1
	}

	fmt.Fprintf(out, "Installed nlwproxy to %s\n", dst)
	if !dirOnPath(target) {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "This directory is not on your PATH yet. Add it, then restart your terminal:")
		if runtime.GOOS == "windows" {
			fmt.Fprintf(out, "  setx PATH \"%%PATH%%;%s\"\n", target)
		} else {
			fmt.Fprintf(out, "  export PATH=\"$PATH:%s\"   # add to ~/.bashrc or ~/.zshrc\n", target)
		}
	} else {
		fmt.Fprintln(out, "You can now run `nlwproxy` from any terminal.")
	}
	return 0
}

func defaultInstallDir() string {
	if runtime.GOOS == "windows" {
		if base := os.Getenv("LOCALAPPDATA"); base != "" {
			return filepath.Join(base, "nlwproxy", "bin")
		}
		return filepath.Join(os.Getenv("USERPROFILE"), "nlwproxy", "bin")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "bin")
}

func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	// Remove an existing binary first so a running copy on Windows can be
	// replaced (rename-then-write is more robust than in-place overwrite).
	_ = os.Remove(dst)
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func dirOnPath(dir string) bool {
	pathEnv := os.Getenv("PATH")
	sep := ":"
	if runtime.GOOS == "windows" {
		sep = ";"
	}
	want := filepath.Clean(dir)
	for _, p := range splitPath(pathEnv, sep) {
		if filepath.Clean(p) == want {
			return true
		}
	}
	return false
}

func splitPath(s, sep string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if string(r) == sep {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
