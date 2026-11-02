// 分组列表「全部收起 / 全部展开」的下一状态。
// expanded[target] 为 true 表示该分组展开；缺失或 true 都视为展开，
// 只有显式 false 才算已收起——与菜单栏面板里 expanded 映射的语义保持一致。
export function nextGroupExpansion(targets, expanded) {
  const list = Array.isArray(targets) ? targets : [];
  const values = expanded || {};
  const allCollapsed = list.length > 0 && list.every(function (target) {
    return values[target] === false;
  });
  // 并非全部收起时，本次点击统一收起；已经全部收起时，本次点击统一展开。
  const next = {};
  list.forEach(function (target) {
    next[target] = allCollapsed;
  });
  return next;
}
