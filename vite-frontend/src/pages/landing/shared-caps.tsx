import {
  LANDING_EXTRA_PROTOCOLS,
  LandingServer,
  landingDate,
  landingGB,
  landingProtocolLabel,
  landingServerModeLabel,
  sharedProtocolCap,
} from "@/api/landing";

export function SharedCaps({ server }: { server: LandingServer }) {
  const caps = server.sharedCaps;

  if (!caps) return null;

  return (
    <div className="space-y-1.5 rounded-lg bg-default-100 p-3 text-sm">
      <p className="font-medium">妙妙屋共享上限（只读）</p>
      <p>
        状态：
        {(
          { active: "共享中", paused: "已暂停", revoked: "已撤销" } as Record<
            string,
            string
          >
        )[caps.status] || caps.status}
      </p>
      <p>
        端口段：{caps.port_min}–{caps.port_max}
      </p>
      <p>
        总流量：{landingGB(caps.traffic_used_bytes)} /{" "}
        {caps.traffic_limit_bytes
          ? landingGB(caps.traffic_limit_bytes)
          : "不限"}{" "}
        GB · 每月 {caps.reset_day} 日重置
      </p>
      <p>总连接数上限：{caps.max_connections || "不限"}</p>
      <p>共享截止：{landingDate(caps.share_until)}</p>
      <p>允许协议：{caps.protocols.map(landingProtocolLabel).join("、")}</p>
      {(() => {
        const extras = LANDING_EXTRA_PROTOCOLS.filter((protocol) =>
          sharedProtocolCap(server, protocol),
        );

        return extras.length ? (
          <p>
            扩展协议：
            {extras
              .map(
                (protocol) =>
                  `${landingProtocolLabel(protocol)}${
                    sharedProtocolCap(server, protocol)?.udp ? "" : "（仅 TCP）"
                  }`,
              )
              .join("、")}
            （FLVX 独立 Core，限制照常生效）
          </p>
        ) : null;
      })()}
      {landingServerModeLabel(caps.server_mode) && (
        <p>
          妙妙屋运行模式：{landingServerModeLabel(caps.server_mode)}（仅供参考）
        </p>
      )}
      {(server.busyPortCount ?? 0) > 0 && (
        <p>机器上已占用 {server.busyPortCount} 个端口，新节点会自动避开</p>
      )}
      {caps.dual_install && (
        <p>同机已有普通 FLVX 节点；禁止同时启用自有落地。</p>
      )}
      <p className="text-default-500">
        总流量为妙妙屋原始用量；下方 FLVX
        额度按计费流量计算。共享总连接数与资源上限由妙妙屋管理。
      </p>
    </div>
  );
}
