package joshbot

import (
	"fmt"
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

// These are the only production files that should be creating raw outbound
// HTTP clients. The crawler boundary owns normal web traffic. The GitHub
// publisher is separate because it talks to the GitHub API with its own
// authentication and should never receive crawler Web Bot Auth credentials.
var approvedRawOutboundHTTPFiles = map[string]string{
	"internal/robots/http.go":             "shared crawler outbound HTTP boundary",
	"internal/githubpublish/publisher.go": "GitHub API publisher",
}

// These net/http symbols can create or operate a raw outbound HTTP path. If one
// shows up somewhere new, I want the test to fail so the new path has to be
// reviewed instead of quietly bypassing the crawler boundary.
var rawOutboundHTTPSymbols = map[string]struct{}{
	"Client":                {},
	"DefaultClient":         {},
	"DefaultTransport":      {},
	"Get":                   {},
	"Head":                  {},
	"NewRequest":            {},
	"NewRequestWithContext": {},
	"Post":                  {},
	"PostForm":              {},
	"RoundTripper":          {},
	"Transport":             {},
}

func TestRawOutboundHTTPIsRestrictedToApprovedBoundaries(t *testing.T) {
	for path, reason := range approvedRawOutboundHTTPFiles {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("approved raw HTTP file %q has no reason", path)
		}

		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("approved raw HTTP file %q: %v", path, err)
			continue
		}

		if !info.Mode().IsRegular() {
			t.Errorf("approved raw HTTP path %q is not a regular file", path)
		}
	}

	fileSet := token.NewFileSet()
	violations := make([]string, 0)

	for _, root := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(
			root,
			func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}

				if entry.IsDir() {
					return nil
				}

				if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
					return nil
				}

				repositoryPath := filepath.ToSlash(path)
				if _, approved := approvedRawOutboundHTTPFiles[repositoryPath]; approved {
					return nil
				}

				source, err := parser.ParseFile(fileSet, path, nil, 0)
				if err != nil {
					return fmt.Errorf("parse %s: %w", repositoryPath, err)
				}

				httpAlias := ""
				for _, importSpec := range source.Imports {
					if importSpec.Path.Value != `"net/http"` {
						continue
					}

					httpAlias = "http"
					if importSpec.Name == nil {
						break
					}

					switch importSpec.Name.Name {
					case ".", "_":
						violations = append(
							violations,
							fmt.Sprintf(
								"%s: net/http import %q is not allowed",
								repositoryPath,
								importSpec.Name.Name,
							),
						)
						return nil
					default:
						httpAlias = importSpec.Name.Name
					}

					break
				}

				if httpAlias == "" {
					return nil
				}

				ast.Inspect(source, func(node ast.Node) bool {
					selector, ok := node.(*ast.SelectorExpr)
					if !ok {
						return true
					}

					identifier, ok := selector.X.(*ast.Ident)
					if !ok || identifier.Name != httpAlias {
						return true
					}

					if _, outbound := rawOutboundHTTPSymbols[selector.Sel.Name]; !outbound {
						return true
					}

					position := fileSet.Position(selector.Pos())
					violations = append(
						violations,
						fmt.Sprintf(
							"%s: raw outbound HTTP reference %s.%s",
							position.String(),
							httpAlias,
							selector.Sel.Name,
						),
					)

					return true
				})

				return nil
			},
		)
		if err != nil {
			t.Fatalf("inspect production Go files under %q: %v", root, err)
		}
	}

	if len(violations) == 0 {
		return
	}

	sort.Strings(violations)

	t.Fatalf(
		"raw outbound HTTP bypasses the approved JoshBot boundaries:\n%s\n\n"+
			"crawler web requests must use internal/robots; intentional exceptions need an explicit allowlist entry",
		strings.Join(violations, "\n"),
	)
}
