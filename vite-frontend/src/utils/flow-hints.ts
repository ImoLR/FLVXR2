// The user "已用流量" is its own cumulative counter (it keeps traffic of deleted rules), not
// the sum of the user's current rules. A user-level reset clears it together with the
// user's rules and tunnel permissions.
export const USER_TOTAL_FLOW_HINT =
  "累计流量：包含已删除转发的用量；用户归零时会同时清零其转发和隧道流量";

export const USER_RESET_FLOW_SCOPE_HINT =
  "归零会同时清零该用户的账号流量、全部隧道权限流量和全部转发流量（清零前的转发流量写入各转发的流量归零日志），此操作不可撤销。";

// Traffic limits are compared with billed traffic; every production tunnel bills 双向.
export const DUPLEX_FLOW_LIMIT_HINT =
  "流量限制按双向计算：(上传+下载)×倍率×2";
