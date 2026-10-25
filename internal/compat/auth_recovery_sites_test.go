package compat

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// authRecoverySites lists the files that react to "the upstream rejected this
// managed account's credential", keyed by path from the repository root. Each one
// decides only whether a failure is an auth failure and how to count the retry;
// the recovery order itself (refresh the current account once, then fail over)
// lives in auth.RequestAuth.RecoverAuthFailure.
//
// The list is checked in both directions against the sources, and a call site is
// recognised whether it names the shared step itself or reaches it through a
// layer helper (see scanAuthRecoverySources). The client layer's five upstream
// call sites are the reason: they only call recoverManagedAuth, so a scan for the
// shared step's name alone would not see them.
var authRecoverySites = []string{
	"internal/completionruntime/nonstream.go",
	"internal/deepseek/client/client_auth.go",
	"internal/deepseek/client/client_auth_failure.go",
	"internal/deepseek/client/client_session.go",
	"internal/deepseek/client/client_session_delete.go",
	"internal/deepseek/client/client_upload.go",
	"internal/httpapi/openai/chat/vercel_stream.go",
}

// authRecoveryInternals are the pieces the shared step is built from. Outside
// internal/auth they mean someone is assembling the recovery sequence by hand
// again, which is what makes one policy change fan out over every call site.
// RefreshTokenErr is in the list because the removed per-layer helper was built
// on it: a restored copy that avoids MarkAuthFailure would otherwise slip past.
var authRecoveryInternals = []string{
	"RefreshTokenErr(",
	"RefreshTokenOnce(",
	"AuthRefreshAlreadySucceeded(",
	"MarkAuthFailure(",
}

// perLayerCopies are the helpers that used to re-implement the sequence inside
// one layer. They were removed so the layers cannot drift apart; reintroducing
// one would restore exactly the bug they caused (client_session.go and
// client_session_delete.go used a narrower failure predicate than the other
// call sites, so the same upstream failure was retried in one place and given up
// on in another).
var perLayerCopies = []string{
	"handleManagedAuthFailure(",
	"retryManagedAuthFailure(",
}

// TestAuthRecoveryStaysInOnePlace pins the property that makes a change to
// "what happens when a managed account's token is rejected" a one-file change.
//
// The shared step is checked by name because the property is about the shape of
// the call sites rather than about a value they compute; the behaviour behind it
// is covered by internal/auth/auth_recovery_test.go.
//
// The scan covers the whole module rather than internal/ alone: a call site under
// cmd/, api/ or app/ is the same drift. The matching is textual (a name followed
// by "("), so a comment or a string that spells one of the names is reported too.
// That strictness is deliberate — these names have no business appearing outside
// the recovery path — and the cost is confined to whoever writes such a comment.
func TestAuthRecoveryStaysInOnePlace(t *testing.T) {
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root %s has no go.mod (%v); the scan assumes the test runs from internal/compat", root, err)
	}

	sources, helpers, err := scanAuthRecoverySources(root)
	if err != nil {
		t.Fatalf("scan %s failed: %v", root, err)
	}
	if len(helpers) == 0 {
		t.Fatal("no layer helper calls auth.RequestAuth.RecoverAuthFailure; the shared step or the client-layer wrapper was renamed, so the call-site scan below would be blind")
	}

	found := map[string]bool{}
	for rel, source := range sources {
		for _, name := range perLayerCopies {
			if strings.Contains(source.src, name) {
				t.Errorf("%s defines or calls %s; the recovery sequence belongs to auth.RequestAuth.RecoverAuthFailure", rel, name)
			}
		}
		if strings.HasPrefix(rel, "internal/auth/") {
			// The package that owns the shared step is the one place the internals
			// are legal.
			continue
		}
		for _, name := range authRecoveryInternals {
			if strings.Contains(source.src, name) {
				t.Errorf("%s uses %s directly; call auth.RequestAuth.RecoverAuthFailure so the refresh-then-failover order stays in one place", rel, name)
			}
		}
		if recoversManagedAuth(source.src, helpers) {
			found[rel] = true
		}
	}

	for _, rel := range authRecoverySites {
		if !found[rel] {
			t.Errorf("%s no longer recovers an auth failure; if it moved, update authRecoverySites", rel)
		}
	}
	rels := make([]string, 0, len(found))
	for rel := range found {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		if !containsString(authRecoverySites, rel) {
			t.Errorf("%s recovers an auth failure but is not in authRecoverySites; add it so the next policy change does not miss it", rel)
		}
	}
}

// authRecoverySource is what the scan learns from one non-test Go file.
type authRecoverySource struct {
	src string
	// calls is the set of function and method names called from the file's
	// function bodies.
	calls map[string]bool
	// directHelpers are the functions declared in the file whose body calls the
	// shared step directly.
	directHelpers []string
}

// scanAuthRecoverySources reads every non-test Go file under root and derives the
// set of layer helpers: functions that call the shared step directly and are
// called from at least two other files. That is the shape of a wrapper several
// call sites share — the client layer's recoverManagedAuth, used by all five of
// its upstream call sites. A function that calls the shared step but is used from
// a single file is that file's own step: the file that defines it is already a
// site (completionruntime's canRetryOnAlternateAccount, the Vercel switch
// handler), and its caller consumes a decision rather than making one. Following
// every direct caller instead would put retry loops and route tables on the list.
//
// The walk covers the whole module so a call site under cmd/, api/ or app/ is
// judged like one under internal/.
func scanAuthRecoverySources(root string) (map[string]*authRecoverySource, map[string]bool, error) {
	sources := map[string]*authRecoverySource{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if skipAuthRecoveryDir(rel, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sources[rel] = parseAuthRecoverySource(string(raw))
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	definingFile := map[string]string{}
	callerFiles := map[string]map[string]bool{}
	for rel, source := range sources {
		for name := range source.calls {
			if callerFiles[name] == nil {
				callerFiles[name] = map[string]bool{}
			}
			callerFiles[name][rel] = true
		}
		for _, name := range source.directHelpers {
			definingFile[name] = rel
		}
	}
	helpers := map[string]bool{}
	for name, defFile := range definingFile {
		if name == "RecoverAuthFailure" {
			// The shared step itself; files that name it are already detected by
			// name below, so it is not a helper to follow.
			continue
		}
		callers := 0
		for rel := range callerFiles[name] {
			if rel != defFile {
				callers++
			}
		}
		if callers >= 2 {
			helpers[name] = true
		}
	}
	return sources, helpers, nil
}

// skipAuthRecoveryDir reports whether a directory is outside the scan: dependency
// trees (vendor, node_modules), tool state (the dot-directories, .git included)
// and the Web UI, which is not part of the Go module. The dependency and dot
// checks are by name so they apply at any depth; webui is only skipped at the
// repository root, because internal/webui is a Go package like any other.
func skipAuthRecoveryDir(rel, name string) bool {
	if rel == "." {
		return false
	}
	if rel == "webui" {
		return true
	}
	if name == "vendor" || name == "node_modules" {
		return true
	}
	return strings.HasPrefix(name, ".")
}

// recoversManagedAuth reports whether src reacts to a rejected managed-account
// credential: it names the shared step, or it calls one of the layer helpers that
// does.
func recoversManagedAuth(src string, helpers map[string]bool) bool {
	if strings.Contains(src, "RecoverAuthFailure(") {
		return true
	}
	for name := range helpers {
		if strings.Contains(src, name+"(") {
			return true
		}
	}
	return false
}

// parseAuthRecoverySource extracts the call names and the functions that call the
// shared step from one source file. A file that does not parse yields no names;
// the textual checks still run on it, and a parse failure in the module is
// already a build error.
func parseAuthRecoverySource(src string) *authRecoverySource {
	source := &authRecoverySource{src: src, calls: map[string]bool{}}
	file, err := parser.ParseFile(token.NewFileSet(), "auth_recovery_scan.go", src, 0)
	if err != nil {
		return source
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if collectCallNames(fn.Body, source.calls, "RecoverAuthFailure") {
			source.directHelpers = append(source.directHelpers, fn.Name.Name)
		}
	}
	return source
}

// collectCallNames records every function or method name called inside node and
// reports whether one of them is want; the receiver or package qualifier is
// irrelevant because a.RecoverAuthFailure and r.RecoverAuthFailure are the same
// step.
func collectCallNames(node ast.Node, calls map[string]bool, want string) bool {
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			calls[fun.Name] = true
			if fun.Name == want {
				found = true
			}
		case *ast.SelectorExpr:
			if fun.Sel != nil {
				calls[fun.Sel.Name] = true
				if fun.Sel.Name == want {
					found = true
				}
			}
		}
		return true
	})
	return found
}
