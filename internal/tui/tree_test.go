// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package tui

import (
	"testing"

	"github.com/codeactual/evolve/internal/plan"
)

// caseTree builds a one-plugin, one-skill tree with two trigger cases and one
// eval case, for the navigation/structure tests.
func caseTree() tree {
	tr := tree{}
	p := tr.add(treeNode{label: "plugin", parent: -1, expanded: true})
	s := tr.add(treeNode{label: "skill", depth: 1, parent: p, expanded: true, skill: "s"})
	tr.add(treeNode{label: "q1", depth: 2, parent: s, leaf: true, skill: "s", kind: plan.KindTriggers, caseKey: "q1", hasKind: true})
	tr.add(treeNode{label: "q2", depth: 2, parent: s, leaf: true, skill: "s", kind: plan.KindTriggers, caseKey: "q2", hasKind: true})
	tr.add(treeNode{label: "e1", depth: 2, parent: s, leaf: true, skill: "s", kind: plan.KindEvals, caseKey: "e1", hasKind: true})
	return tr
}

func TestTreeVisibleAndFold(t *testing.T) {
	tr := caseTree()
	if got := len(tr.visible()); got != 5 {
		t.Fatalf("fully expanded visible = %d, want 5", got)
	}
	// Collapse the skill (index 1): its three case leaves disappear.
	tr.nodes[1].expanded = false
	if got := len(tr.visible()); got != 2 {
		t.Errorf("skill collapsed visible = %d, want 2 (plugin + skill)", got)
	}
}

func TestTreeCaseLeaves(t *testing.T) {
	tr := caseTree()
	// Skill node aggregates all three cases.
	got := tr.caseLeaves(1)
	if len(got) != 3 {
		t.Fatalf("skill caseLeaves = %d, want 3", len(got))
	}
	// A case leaf yields just itself.
	leaf := tr.caseLeaves(4)
	if len(leaf) != 1 || leaf[0] != (plan.CaseRef{Skill: "s", Kind: plan.KindEvals, Case: "e1"}) {
		t.Errorf("leaf caseLeaves = %+v, want [s/evals/e1]", leaf)
	}
}

func TestTreeExpandWhere(t *testing.T) {
	tr := caseTree()
	// Only open branches containing q2.
	tr.expandWhere(func(cr plan.CaseRef) bool { return cr.Case == "q2" })
	if !tr.nodes[0].expanded || !tr.nodes[1].expanded {
		t.Error("plugin and skill containing q2 should be expanded")
	}

	// A predicate that matches nothing collapses every parent.
	tr.expandWhere(func(plan.CaseRef) bool { return false })
	if tr.nodes[0].expanded || tr.nodes[1].expanded {
		t.Error("no match should collapse all parents")
	}
}

func TestTreeExpandCollapseLevel(t *testing.T) {
	tr := caseTree()
	// Collapse all foldable levels, deepest first.
	tr.collapseLevel() // closes the skill (depth 1)
	if tr.nodes[1].expanded {
		t.Error("collapseLevel should close the skill first")
	}
	tr.collapseLevel() // closes the plugin (depth 0)
	if tr.nodes[0].expanded {
		t.Error("collapseLevel should then close the plugin")
	}
	tr.expandLevel() // reopens the plugin
	if !tr.nodes[0].expanded {
		t.Error("expandLevel should reopen the plugin first")
	}
}

func TestTreeNavLeafLeftJumpsToParent(t *testing.T) {
	tr := caseTree()
	// Cursor on the first case leaf (visible position 2).
	tr.cursor = 2
	tr.expand(false) // left on a leaf jumps to its parent (the skill)
	if got := tr.currentNode(); got != 1 {
		t.Errorf("after left on leaf, currentNode = %d, want 1 (skill)", got)
	}
}
