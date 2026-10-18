package web

import "strings"

type route struct {
	method  string
	path    string
	handler Handler
}

// Leaf is a complete pattern
// Its handler and its param name in path order.
type leaf struct {
	handler Handler
	names   []string
	pattern string // the registered path, e.g. "/id/:id"
}

// node is a one level of a per-methods path trie.
// each level has three possible children, tried in priority order
// 1. Static
// 2. Param
// 3. Wildcard
type node struct {
	static map[string]*node
	param  *node
	leaf   *leaf // a pattern that ends at this node
	wild   *leaf // a pattern that ends in *
}

type router struct {
	roots  map[string]*node
	routes []route // full definitions, kept so Use can re-register them.
}

func newRouter() *router {
	return &router{roots: make(map[string]*node)}
}

// slot identifies where a pattern's leaf is stored: a node's normal leaf,
// or its wildcard leaf.
type slot struct {
	n    *node
	wild bool
}

func (r *router) insert(method, path string, h Handler) {
	r.routes = append(r.routes, route{method, path, h})

	seen := make(map[slot]bool)
	for _, segments := range expandOptional(splitPath(path)) {
		r.insertSegments(method, segments, h, path, seen)
	}
}

// expandOptional turn ":id?" into two patterns
// 1 with the segment
// 2 without it
// So "/id/:id?" registers both "/id" and "/id/:id"
func expandOptional(segments []string) [][]string {
	for i, s := range segments {
		if strings.HasPrefix(s, ":") && strings.HasSuffix(s, "?") {
			with := make([]string, len(segments))
			copy(with, segments)
			with[i] = strings.TrimSuffix(s, "?")

			without := make([]string, 0, len(segments)-1)
			without = append(without, segments[:i]...)
			without = append(without, segments[i+1:]...)

			return append(expandOptional(with), expandOptional(without)...)
		}
	}
	return [][]string{segments}
}

func (r *router) insertSegments(method string, segments []string, h Handler, pattern string, seen map[slot]bool) {
	n, ok := r.roots[method]
	if !ok {
		n = &node{}
		r.roots[method] = n
	}
	var names []string
	for _, s := range segments {
		switch {
		case s == "*":
			// A wildcard captures the rest of the path, so nothing can follow it.
			k := slot{n, true}
			if !seen[k] {
				seen[k] = true
				n.wild = &leaf{handler: h, names: append(names, "*"), pattern: pattern}
			}
			return
		case strings.HasPrefix(s, ":"):
			names = append(names, s[1:])
			if n.param == nil {
				n.param = &node{}
			}
			n = n.param
		default:
			if n.static == nil {
				n.static = make(map[string]*node)
			}
			child, ok := n.static[s]
			if !ok {
				child = &node{}
				n.static[s] = child
			}
			n = child
		}
	}
	k := slot{n, false}
	if !seen[k] {
		seen[k] = true
		n.leaf = &leaf{handler: h, names: names, pattern: pattern}
	}
}

// match finds a handler for method+path.
// A method-specific route wins over /an All ("*") route with the same path.
func (r *router) match(method, path string) (Handler, map[string]string, string) {
	segments := splitPath(path)
	for _, m := range []string{method, "*"} {
		root, ok := r.roots[m]
		if !ok {
			continue
		}
		if l, vals := search(root, segments, nil); l != nil {
			params := make(map[string]string, len(l.names))
			for i, name := range l.names {
				params[name] = vals[i]
			}
			return l.handler, params, l.pattern
		}
	}
	return nil, nil, ""
}

// search walks the trie with priority static > dynamic > wildcard, backtracking
// on dead ends. It returns the matched pattern and the captured values in order.
func search(n *node, segments []string, vals []string) (*leaf, []string) {
	if len(segments) == 0 {
		if n.leaf != nil {
			return n.leaf, vals
		}
		if n.wild != nil { // a wildcard also matches zero segments
			return n.wild, append(vals, "")
		}
		return nil, nil
	}
	if child, ok := n.static[segments[0]]; ok {
		if l, v := search(child, segments[1:], vals); l != nil {
			return l, v
		}
	}
	if n.param != nil {
		if l, v := search(n.param, segments[1:], append(vals, segments[0])); l != nil {
			return l, v
		}
	}
	if n.wild != nil {
		return n.wild, append(vals, strings.Join(segments, "/"))
	}
	return nil, nil
}

func splitPath(path string) []string {
	p := strings.Trim(path, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}
