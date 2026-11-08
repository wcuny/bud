package main

import (
	"sort"
	"strings"
)

func isSep(r rune) bool {
	return r == ' ' || r == '-' || r == '_' || r == '/' || r == '.' || r == ':'
}

// fuzzyScore returns 0 for no match. Substring matches outrank subsequence matches.
func fuzzyScore(needle, hay string) int {
	if hay == "" {
		return 0
	}
	if i := strings.Index(hay, needle); i >= 0 {
		pos := len([]rune(hay[:i]))
		s := 1000 - min(pos, 800)
		if pos == 0 || isSep([]rune(hay)[pos-1]) {
			s += 200
		}
		return s
	}
	n, h := []rune(needle), []rune(hay)
	ni, first, last := 0, -1, -1
	for i, r := range h {
		if ni < len(n) && r == n[ni] {
			if first < 0 {
				first = i
			}
			last = i
			ni++
		}
	}
	if ni < len(n) {
		return 0
	}
	gaps := (last - first + 1) - len(n)
	if gaps > 3*len(n)+10 {
		return 0
	}
	return max(1, 400-gaps*5)
}

func matchScore(c Command, tokens []string) int {
	name := strings.ToLower(c.Name)
	tags := strings.ToLower(strings.Join(c.Tags, " "))
	tip := strings.ToLower(c.Tip)
	cmd := strings.ToLower(c.Cmd)
	total := 0
	for _, t := range tokens {
		best := max(
			fuzzyScore(t, name)*3,
			fuzzyScore(t, tags)*2,
			fuzzyScore(t, tip)*3/2,
			fuzzyScore(t, cmd),
		)
		if best == 0 {
			return 0 // every token must match somewhere
		}
		total += best
	}
	return total
}

func recency(c Command) int64 { return max(c.LastRunAt, c.UpdatedAt) }

// rank filters by query (empty = keep all) and orders results. Ties favour
// the current scope, then global, then other scopes, then recency.
func rank(cmds []Command, query, scope string) []Command {
	type scored struct {
		c        Command
		score, p int
	}
	tokens := strings.Fields(strings.ToLower(query))
	var out []scored
	for _, c := range cmds {
		s := 1
		if len(tokens) > 0 {
			if s = matchScore(c, tokens); s == 0 {
				continue
			}
		}
		p := 2
		if c.Scope == scope {
			p = 0
		} else if c.Scope == "" {
			p = 1
		}
		out = append(out, scored{c, s, p})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if len(tokens) > 0 && a.score != b.score {
			return a.score > b.score
		}
		if a.p != b.p {
			return a.p < b.p
		}
		if ra, rb := recency(a.c), recency(b.c); ra != rb {
			return ra > rb
		}
		return a.c.Name < b.c.Name
	})
	res := make([]Command, len(out))
	for i, s := range out {
		res[i] = s.c
	}
	return res
}
