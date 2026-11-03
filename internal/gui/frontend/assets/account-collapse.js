// 账号卡片的折叠判定。
//
// 语义：默认折叠；只有本次会话里被用户（或深链定位）显式展开过的账号才展开。
// 这里把「下一状态」抽成纯函数，避免再出现供应商组那种「集合语义写反、默认全展开」
// 的问题——主窗口直接调用它，JS 单测也直接断言它。

// accountStateKey 生成 provider/account 组合键，账号 id 里即便含斜杠也不会歧义，
// 因为 provider id 不会含斜杠。
export function accountStateKey(provider, account) {
  const providerID = String(provider || "").trim();
  const accountID = String(account || "").trim();
  if (!providerID) return "";
  return providerID + "/" + accountID;
}

// shouldCollapseAccount 判断一张账号卡片当前是否应当折叠。
// expandedKeys 是「已展开」键集合（Set 或数组均可）。
export function shouldCollapseAccount(expandedKeys, key) {
  if (!key) return true;
  if (expandedKeys instanceof Set) return !expandedKeys.has(key);
  if (Array.isArray(expandedKeys)) return expandedKeys.indexOf(key) < 0;
  return true;
}

// nextAccountExpansion 返回点击折叠热区后该账号的新状态（true=展开）。
export function nextAccountExpansion(expandedKeys, key) {
  if (!key) return false;
  return shouldCollapseAccount(expandedKeys, key);
}
