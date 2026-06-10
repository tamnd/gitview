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
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/tamnd/gitview/backend"
	"github.com/tamnd/gitview/ghapi"
	"github.com/tamnd/gitview/localgit"
	"github.com/tamnd/gitview/server"
)

var version = "dev"

func main() {
	addr := flag.String("addr", "127.0.0.1:9419", "listen address")
	open := flag.Bool("open", false, "open the browser after starting")
	dev := flag.Bool("dev", false, "show error details in responses")
	token := flag.String("token", "", "GitHub token for remote targets (defaults to $GITHUB_TOKEN)")
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

	if *token == "" {
		*token = os.Getenv("GITHUB_TOKEN")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	repos, err := openTarget(ctx, target, *token)
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
	errc := make(chan error, 1)
	go func() { errc <- httpSrv.ListenAndServe() }()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "gitview:", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		// Restore default signal handling so a second ^C kills us.
		stop()
		fmt.Fprintln(os.Stderr, "gitview: shutting down")
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(sctx); err != nil {
			fmt.Fprintln(os.Stderr, "gitview:", err)
		}
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `Usage: gitview [flags] [target]

Targets:
  (none)              the repository in the current directory
  PATH                a local repository, bare or not
  DIR                 a directory whose children are repositories
  gh:OWNER/REPO       a repository on github.com over the REST API
  https://github.com/OWNER/REPO  same, by URL

Flags:
`)
	flag.PrintDefaults()
}

// openTarget turns the command line target into backends. Remote GitHub
// targets get the API backend; a local git repository serves alone; any
// other directory is scanned one level deep for repositories.
func openTarget(ctx context.Context, target, token string) ([]backend.Repo, error) {
	if owner, name, ok := parseRemote(target); ok {
		return []backend.Repo{ghapi.New(owner, name, token)}, nil
	}
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

// parseRemote recognizes gh:owner/repo and github.com URLs.
func parseRemote(target string) (owner, name string, ok bool) {
	slug := ""
	switch {
	case strings.HasPrefix(target, "gh:"):
		slug = strings.TrimPrefix(target, "gh:")
	case strings.HasPrefix(target, "https://github.com/"):
		slug = strings.TrimPrefix(target, "https://github.com/")
	case strings.HasPrefix(target, "http://github.com/"):
		slug = strings.TrimPrefix(target, "http://github.com/")
	case strings.HasPrefix(target, "github.com/"):
		slug = strings.TrimPrefix(target, "github.com/")
	default:
		return "", "", false
	}
	slug = strings.TrimSuffix(strings.Trim(slug, "/"), ".git")
	owner, name, found := strings.Cut(slug, "/")
	if !found || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", false
	}
	return owner, name, true
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
