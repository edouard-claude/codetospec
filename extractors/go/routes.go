package main

import (
	"go/ast"
	"go/printer"
	"go/token"
	"go/types"
	"regexp"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Route is one detected HTTP route.
type Route struct {
	Method  string
	Path    string
	Handler string
	Ref     Ref
}

// httpVerbs are router method names that name an HTTP verb directly
// (gin/echo use upper-case, chi uses title-case).
var httpVerbs = map[string]string{
	"GET": "GET", "POST": "POST", "PUT": "PUT", "PATCH": "PATCH",
	"DELETE": "DELETE", "OPTIONS": "OPTIONS", "HEAD": "HEAD", "ANY": "ANY",
	"Get": "GET", "Post": "POST", "Put": "PUT", "Patch": "PATCH",
	"Delete": "DELETE", "Options": "OPTIONS", "Head": "HEAD", "Connect": "CONNECT", "Trace": "TRACE",
	"Any": "ANY",
}

var methodPrefixRe = regexp.MustCompile(`^(GET|POST|PUT|PATCH|DELETE|OPTIONS|HEAD)\s+(/\S*)$`)

// ExtractRoutes walks loaded packages and returns detected routes with their
// group prefixes resolved and handlers named via type information.
func ExtractRoutes(pkgs []*packages.Package, fset *token.FileSet, root string) []Route {
	var routes []Route
	for _, pkg := range pkgs {
		for _, file := range pkg.Syntax {
			// Resolve router-group prefixes within each function scope.
			ast.Inspect(file, func(n ast.Node) bool {
				var body *ast.BlockStmt
				switch fn := n.(type) {
				case *ast.FuncDecl:
					body = fn.Body
				case *ast.FuncLit:
					body = fn.Body
				}
				if body == nil {
					return true
				}
				prefixes := groupPrefixes(body)
				for _, stmt := range allCalls(body) {
					if r, ok := routeFrom(stmt, prefixes, pkg.TypesInfo, fset, root); ok {
						routes = append(routes, r)
					}
				}
				return true
			})
		}
	}
	return dedupRoutes(routes)
}

// groupPrefixes maps a router-group variable name to its cumulative path
// prefix, resolving chained groups (api := r.Group("/api"); v1 := api.Group("/v1")).
func groupPrefixes(body *ast.BlockStmt) map[string]string {
	direct := map[string]struct {
		base   string
		prefix string
	}{}
	ast.Inspect(body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			return true
		}
		lhs, ok := assign.Lhs[0].(*ast.Ident)
		if !ok {
			return true
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Group" || len(call.Args) == 0 {
			return true
		}
		prefix, ok := stringLit(call.Args[0])
		if !ok {
			return true
		}
		base := ""
		if recv, ok := sel.X.(*ast.Ident); ok {
			base = recv.Name
		}
		direct[lhs.Name] = struct {
			base   string
			prefix string
		}{base, prefix}
		return true
	})

	resolved := make(map[string]string, len(direct))
	var resolve func(name string, seen map[string]bool) string
	resolve = func(name string, seen map[string]bool) string {
		if p, done := resolved[name]; done {
			return p
		}
		d, ok := direct[name]
		if !ok || seen[name] {
			return ""
		}
		seen[name] = true
		full := strings.TrimRight(resolve(d.base, seen), "/") + d.prefix
		resolved[name] = full
		return full
	}
	for name := range direct {
		resolve(name, map[string]bool{})
	}
	return resolved
}

// allCalls returns every call expression inside a function body.
func allCalls(body *ast.BlockStmt) []*ast.CallExpr {
	var calls []*ast.CallExpr
	ast.Inspect(body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			calls = append(calls, call)
		}
		return true
	})
	return calls
}

// routeFrom interprets one call as a route registration, if it is one.
func routeFrom(call *ast.CallExpr, prefixes map[string]string, info *types.Info, fset *token.FileSet, root string) (Route, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || len(call.Args) == 0 {
		return Route{}, false
	}
	name := sel.Sel.Name
	recvPrefix := ""
	if recv, ok := sel.X.(*ast.Ident); ok {
		recvPrefix = prefixes[recv.Name]
	}

	var method, path string
	switch {
	case httpVerbs[name] != "":
		p, ok := stringLit(call.Args[0])
		if !ok || !strings.HasPrefix(p, "/") {
			return Route{}, false
		}
		method, path = httpVerbs[name], p
	case name == "Handle" || name == "Method":
		// gin: Handle(method, path, h...) ; chi: Method(method, path, h)
		if len(call.Args) < 2 {
			return Route{}, false
		}
		m, okM := stringLit(call.Args[0])
		p, okP := stringLit(call.Args[1])
		if okM && okP && isHTTPMethod(m) && strings.HasPrefix(p, "/") {
			method, path = strings.ToUpper(m), p
		} else if p2, ok := stringLit(call.Args[0]); ok && strings.HasPrefix(p2, "/") {
			method, path = "", p2 // net/http mux.Handle(path, handler)
		} else {
			return Route{}, false
		}
	case name == "HandleFunc":
		p, ok := stringLit(call.Args[0])
		if !ok {
			return Route{}, false
		}
		if m := methodPrefixRe.FindStringSubmatch(p); m != nil { // Go 1.22 "GET /path"
			method, path = m[1], m[2]
		} else if strings.HasPrefix(p, "/") {
			method, path = "", p
		} else {
			return Route{}, false
		}
	default:
		return Route{}, false
	}

	full := strings.TrimRight(recvPrefix, "/") + path
	if !strings.HasPrefix(full, "/") {
		full = "/" + full
	}
	return Route{
		Method:  method,
		Path:    full,
		Handler: handlerName(call.Args[len(call.Args)-1], info),
		Ref:     refOf(call, fset, root),
	}, true
}

// handlerName resolves a handler argument to a qualified symbol name so the
// route can later be linked to the file that defines it.
func handlerName(expr ast.Expr, info *types.Info) string {
	switch e := expr.(type) {
	case *ast.FuncLit:
		return "closure"
	case *ast.Ident:
		if info != nil {
			if obj := info.Uses[e]; obj != nil {
				return qualify(obj)
			}
		}
		return e.Name
	case *ast.SelectorExpr:
		if info != nil {
			if sel, ok := info.Selections[e]; ok {
				if recv := sel.Recv(); recv != nil {
					return typeName(recv) + "." + e.Sel.Name
				}
			}
			if obj := info.Uses[e.Sel]; obj != nil {
				return qualify(obj)
			}
		}
		if x, ok := e.X.(*ast.Ident); ok {
			return x.Name + "." + e.Sel.Name
		}
		return e.Sel.Name
	case *ast.CallExpr:
		return handlerName(e.Fun, info) // constructor / wrapper
	default:
		return renderExpr(expr)
	}
}

func qualify(obj types.Object) string {
	if obj.Pkg() != nil {
		return obj.Pkg().Name() + "." + obj.Name()
	}
	return obj.Name()
}

func typeName(t types.Type) string {
	t = deref(t)
	if named, ok := t.(*types.Named); ok {
		obj := named.Obj()
		if obj.Pkg() != nil {
			return obj.Pkg().Name() + "." + obj.Name()
		}
		return obj.Name()
	}
	return t.String()
}

func deref(t types.Type) types.Type {
	if ptr, ok := t.(*types.Pointer); ok {
		return ptr.Elem()
	}
	return t
}

func stringLit(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	return strings.Trim(lit.Value, "`\""), true
}

func isHTTPMethod(s string) bool {
	switch strings.ToUpper(s) {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD", "CONNECT", "TRACE":
		return true
	}
	return false
}

func renderExpr(expr ast.Expr) string {
	var b strings.Builder
	_ = printer.Fprint(&b, token.NewFileSet(), expr)
	return b.String()
}

func refOf(node ast.Node, fset *token.FileSet, root string) Ref {
	start := fset.Position(node.Pos())
	end := fset.Position(node.End())
	path := start.Filename
	if rel, err := relTo(root, path); err == nil {
		path = rel
	}
	return Ref{Path: path, Lines: itoa(start.Line) + "-" + itoa(end.Line)}
}

func dedupRoutes(routes []Route) []Route {
	seen := make(map[string]bool, len(routes))
	var out []Route
	for _, r := range routes {
		key := r.Method + " " + r.Path
		if !seen[key] {
			seen[key] = true
			out = append(out, r)
		}
	}
	return out
}
