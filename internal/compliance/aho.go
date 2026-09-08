package compliance

// matcher is a byte-level Aho-Corasick automaton for multi-pattern substring
// search in a single pass. Patterns are matched exactly (byte-wise), so
// case-folding is the caller's responsibility; UTF-8 text (Chinese included)
// works because a multi-byte rune is just a fixed byte sequence.
type matcher struct {
	nodes []acNode
}

type acNode struct {
	next map[byte]int32
	fail int32
	// out lists the indexes of patterns ending at this node (including those
	// reachable via dictionary suffix links, flattened at build time).
	out []int32
}

// newMatcher builds an automaton over the given patterns. Empty patterns are
// ignored. Pattern indexes reported by Match refer to this slice.
func newMatcher(patterns []string) *matcher {
	m := &matcher{nodes: []acNode{{next: map[byte]int32{}, fail: 0}}}
	for i, p := range patterns {
		if p == "" {
			continue
		}
		cur := int32(0)
		for j := 0; j < len(p); j++ {
			b := p[j]
			nxt, ok := m.nodes[cur].next[b]
			if !ok {
				nxt = int32(len(m.nodes))
				m.nodes = append(m.nodes, acNode{next: map[byte]int32{}})
				m.nodes[cur].next[b] = nxt
			}
			cur = nxt
		}
		m.nodes[cur].out = append(m.nodes[cur].out, int32(i))
	}
	// BFS to compute failure links; children of root fail to root.
	queue := make([]int32, 0, len(m.nodes))
	for _, child := range m.nodes[0].next {
		m.nodes[child].fail = 0
		queue = append(queue, child)
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for b, child := range m.nodes[cur].next {
			f := m.nodes[cur].fail
			for {
				if n, ok := m.nodes[f].next[b]; ok && n != child {
					f = n
					break
				}
				if f == 0 {
					break
				}
				f = m.nodes[f].fail
			}
			m.nodes[child].fail = f
			// Flatten dictionary suffix outputs.
			if fo := m.nodes[f].out; len(fo) > 0 {
				m.nodes[child].out = append(m.nodes[child].out, fo...)
			}
			queue = append(queue, child)
		}
	}
	return m
}

// Match returns the set of pattern indexes that occur in text (deduplicated,
// in first-occurrence order).
func (m *matcher) Match(text string) []int32 {
	if m == nil || len(m.nodes) <= 1 {
		return nil
	}
	var found []int32
	seen := map[int32]struct{}{}
	cur := int32(0)
	for i := 0; i < len(text); i++ {
		b := text[i]
		for {
			if n, ok := m.nodes[cur].next[b]; ok {
				cur = n
				break
			}
			if cur == 0 {
				break
			}
			cur = m.nodes[cur].fail
		}
		for _, p := range m.nodes[cur].out {
			if _, dup := seen[p]; !dup {
				seen[p] = struct{}{}
				found = append(found, p)
			}
		}
	}
	return found
}
