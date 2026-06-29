package dispatcher

import (
	"PromAI/pkg/alerting"
	"PromAI/pkg/database"
)

// matchRoute 选择告警匹配的路由叶子节点。
//
// 算法（与 Alertmanager 一致的简化版）：
//   1. 若规则显式指定 route_id，则从该节点出发；否则从根路由出发。
//   2. DFS 子节点，按 priority desc, id asc 顺序：
//      - 子节点 matchers 全部命中 → 走入子树；命中后默认终止（除非 continue=true 才继续尝试兄弟）
//      - 子节点 matchers 不命中 → 跳过
//   3. 找不到匹配子节点时，当前节点即为最终路由。
//
// 返回 nil 表示未找到任何根路由（极少发生：EnsureRootRoute 会自动建立）。
func matchRoute(routes []database.AlertRoute, labels alerting.LabelSet, ruleRouteID *uint) *database.AlertRoute {
	if len(routes) == 0 {
		return nil
	}
	byID := make(map[uint]*database.AlertRoute, len(routes))
	children := make(map[uint][]*database.AlertRoute, len(routes))
	var root *database.AlertRoute
	for i := range routes {
		r := &routes[i]
		byID[r.ID] = r
		if r.ParentID == nil {
			if root == nil {
				root = r
			}
		} else {
			children[*r.ParentID] = append(children[*r.ParentID], r)
		}
	}
	if root == nil {
		return nil
	}

	var start *database.AlertRoute
	if ruleRouteID != nil && *ruleRouteID > 0 {
		start = byID[*ruleRouteID]
	}
	if start == nil {
		start = root
	}
	return dfsMatch(start, children, labels)
}

func dfsMatch(node *database.AlertRoute, children map[uint][]*database.AlertRoute, labels alerting.LabelSet) *database.AlertRoute {
	// 当前节点本身的 matchers 必须命中（除根之外）
	if node.ParentID != nil {
		ms, _ := alerting.DecodeMatchers(node.MatchersJSON)
		if !alerting.MatchAll(ms, labels) {
			return nil
		}
	}
	// 尝试子节点
	kids := children[node.ID]
	var matched *database.AlertRoute
	for _, c := range kids {
		got := dfsMatch(c, children, labels)
		if got == nil {
			continue
		}
		matched = got
		if !c.Continue {
			break
		}
	}
	if matched != nil {
		return matched
	}
	return node
}

func findRoot(routes []database.AlertRoute) *database.AlertRoute {
	for i := range routes {
		if routes[i].ParentID == nil {
			return &routes[i]
		}
	}
	return nil
}
