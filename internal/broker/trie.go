package broker

// trie is the in-memory subscription index. Edges are concrete topic levels;
// '+' levels use the plus edge and '#' is stored on a terminal hash node.
//
// A node's members are the clients whose filter terminates exactly at that
// node, with the (possibly downgraded) maximum granted QoS per client. The
// trie is rebuilt from SQLite on startup and kept in sync on subscribe and
// clean-session teardown.
type trie struct {
	children map[string]*trieNode
}

type trieNode struct {
	next map[string]*trieNode // exact-level edges
	plus *trieNode            // '+' edge
	hash *trieNode            // '#' terminal node
	// members maps clientID -> granted QoS for subscriptions ending here.
	members map[string]byte
}

type subMember struct {
	clientID string
	qos      byte
}

func newTrie() *trie { return &trie{children: map[string]*trieNode{"": newTrieNode()}} }

func newTrieNode() *trieNode { return &trieNode{members: map[string]byte{}} }

// root returns the synthetic root; levels never include an empty 0th level
// except via the "" edge used internally.
func (t *trie) root() *trieNode { return t.children[""] }

// add registers a validated filter for clientID at granted QoS.
// levels must be strings.Split(filter, "/").
func (t *trie) add(levels []string, clientID string, qos byte) {
	n := t.root()
	for _, lv := range levels {
		switch lv {
		case "+":
			if n.plus == nil {
				n.plus = newTrieNode()
			}
			n = n.plus
		case "#":
			if n.hash == nil {
				n.hash = newTrieNode()
			}
			n = n.hash
		default:
			if n.next == nil {
				n.next = map[string]*trieNode{}
			}
			if n.next[lv] == nil {
				n.next[lv] = newTrieNode()
			}
			n = n.next[lv]
		}
	}
	// SUBSCRIBE replaces an existing subscription with the same filter;
	// the granted QoS is the new QoS (3.8.4).
	n.members[clientID] = qos
}

// removeClient erases every subscription of clientID from the trie.
func (t *trie) removeClient(clientID string) {
	t.prune(t.root(), clientID)
}

func (t *trie) prune(n *trieNode, clientID string) bool {
	delete(n.members, clientID)
	for k, c := range n.next {
		if t.prune(c, clientID) {
			delete(n.next, k)
		}
	}
	if n.plus != nil && t.prune(n.plus, clientID) {
		n.plus = nil
	}
	if n.hash != nil && t.prune(n.hash, clientID) {
		n.hash = nil
	}
	return len(n.members) == 0 && len(n.next) == 0 && n.plus == nil && n.hash == nil
}

// match collects every (client, granted QoS) pair whose filter matches the
// concrete topic levels. dollar reports whether the topic begins with '$':
// wildcard-first filters ("#", "+/...") then do not match (section 4.7.2).
func (t *trie) match(levels []string, dollar bool) []subMember {
	var out []subMember
	var walk func(n *trieNode, i int)
	walk = func(n *trieNode, i int) {
		if n == nil {
			return
		}
		if i == len(levels) {
			for cid, q := range n.members {
				out = append(out, subMember{cid, q})
			}
			// A '#' child at position i is the root wildcard only when the
			// topic is not a '$' topic; "a/#" reaching end with i>0 matches
			// zero remaining levels ("a/#" -> "a").
			if n.hash != nil && (i > 0 || !dollar) {
				for cid, q := range n.hash.members {
					out = append(out, subMember{cid, q})
				}
			}
			return
		}
		if c := n.next[levels[i]]; c != nil {
			walk(c, i+1)
		}
		if n.plus != nil && !(dollar && i == 0) {
			walk(n.plus, i+1) // '+' matches exactly one level, even empty
		}
		if n.hash != nil && !(dollar && i == 0) {
			// '#' matches this and every remaining level.
			for cid, q := range n.hash.members {
				out = append(out, subMember{cid, q})
			}
		}
	}
	walk(t.root(), 0)
	return out
}
