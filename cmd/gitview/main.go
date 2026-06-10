// Command gitview serves a GitHub-style web view of git repositories.
//
// Point it at a repository, a directory of repositories, or nothing at all
// to serve the current directory:
//
//	gitview
//	gitview ~/src/myproject
//	gitview ~/src
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/tamnd/gitview/backend"
	"github.com/tamnd/gitview/localgit"
	"github.com/tamnd/gitview/server"
)

var version = "dev"

func main() {
	addr := flag.String("addr", "127.0.0.1:9419", "listen address")
	open := flag.Bool("open", false, "open the browser after starting")
	dev := flag.Bool("dev", false, "show error details in responses")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Usage = usage
	flag.Parse()

	if *showVersion {
		fmt.Println("gitview", version)
		return
	}

	target := "."
	if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "gitview: expected at most one target")
		usage()
		os.Exit(2)
	}
	if flag.NArg() == 1 {
		target = flag.Arg(0)
	}

	ctx := context.Background()
	repos, err := openTarget(ctx, target)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gitview:", err)
		os.Exit(1)
	}

	srv, err := server.New(ctx, repos, server.Options{Dev: *dev, Version: version})
	if err != nil {
		fmt.Fprintln(os.Stderr, "gitview:", err)
		os.Exit(1)
	}

	url := "http://" + strings.Replace(*addr, "0.0.0.0", "127.0.0.1", 1)
	fmt.Fprintf(os.Stderr, "gitview: serving %d repositor%s on %s\n",
		len(repos), plural(len(repos), "y", "ies"), url)
	if *open {
		go openBrowser(url)
	}

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := httpSrv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, "gitview:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `Usage: gitview [flags] [target]

Targets:
  (none)          the repository in the current directory
  PATH            a local repository, bare or not
  DIR             a directory whose children are repositories

Flags:
`)
	flag.PrintDefaults()
}

// openTarget turns the command line target into backends. A git repository
// serves alone; any other directory is scanned one level deep for
// repositories.
func openTarget(ctx context.Context, target string) ([]backend.Repo, error) {
	abs, err := filepath.Abs(target)
	if err != nil {
		return nil, err
	}
	if repo, err := localgit.New(ctx, abs); err == nil {
		return []backend.Repo{repo}, nil
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s is not a git repository", target)
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var repos []backend.Repo
	for _, name := range names {
		if repo, err := localgit.New(ctx, filepath.Join(abs, name)); err == nil {
			repos = append(repos, repo)
		}
	}
	if len(repos) == 0 {
		return nil, fmt.Errorf("no git repositories found under %s", target)
	}
	return repos, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
