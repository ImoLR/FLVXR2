// The user "已用流量" is its own cumulative counter (it keeps traffic of deleted rules), not
// the sum of the user's current rules. A user-level reset clears it together with the
// user's rules and tunnel permissions.
export const USER_TOTAL_FLOW_HINT =
  "累计流量：包含已删除规则的用量；用户归零时会同时清零其规则和隧道流量";

export const USER_RESET_FLOW_SCOPE_HINT =
  "归零会同时清零该用户的账号流量、全部隧道权限流量和全部规则流量（清零前的规则流量写入各规则的流量归零日志），此操作不可撤销。";
