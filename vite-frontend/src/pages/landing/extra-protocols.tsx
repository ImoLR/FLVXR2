import {
  LANDING_EXTRA_PROTOCOLS,
  LandingServer,
  landingProtocolLabel,
  sharedProtocolCap,
} from "@/api/landing";
import { Checkbox } from "@/shadcn-bridge/heroui/checkbox";
import { Input } from "@/shadcn-bridge/heroui/input";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";

// Plan 121: AnyTLS / Snell / Mieru on 妙妙屋共享 servers. Hidden unless the
// share advertises them (contract landing-protocols-v1) or one is already
// enabled; defaults apply to nodes created afterwards.
export function ExtraProtocolSettings({
  server,
  form,
  set,
}: {
  server: LandingServer;
  form: LandingServer;
  set: <K extends keyof LandingServer>(key: K, value: LandingServer[K]) => void;
}) {
  if (server.source !== "shared") return null;
  const extras = LANDING_EXTRA_PROTOCOLS.filter(
    (protocol) =>
      sharedProtocolCap(server, protocol) || form.protocols.includes(protocol),
  );

  if (!extras.length) return null;
  const snell = sharedProtocolCap(server, "snell");
  const mieru = sharedProtocolCap(server, "mieru");
  const versions = snell?.versions?.length ? snell.versions : [4, 5];
  const obfsModes = snell?.obfs?.length ? snell.obfs : ["none", "http", "tls"];
  const transports = mieru?.transports?.length
    ? mieru.transports
    : ["TCP", "UDP"];
  const obfs = form.snellObfs || "none";

  return (
    <div className="space-y-3 rounded-lg border border-divider p-3">
      <div>
        <p className="text-sm font-medium">扩展协议（妙妙屋共享）</p>
        <p className="text-xs text-default-500">
          运行在 FLVX 独立的 Core 中，用户、连接数、在线 IP、流量和整机保护都由
          FLVX 管理，不影响妙妙屋官方已有的入站。
        </p>
      </div>
      <div className="flex flex-wrap gap-4">
        {extras.map((protocol) => {
          const cap = sharedProtocolCap(server, protocol);
          const enabled = form.protocols.includes(protocol);

          return (
            <Checkbox
              key={protocol}
              isDisabled={!cap && !enabled}
              isSelected={enabled}
              onValueChange={(checked) =>
                set(
                  "protocols",
                  checked
                    ? [...form.protocols, protocol]
                    : form.protocols.filter((item) => item !== protocol),
                )
              }
            >
              {landingProtocolLabel(protocol)}
              {cap && !cap.udp ? "（仅 TCP）" : ""}
            </Checkbox>
          );
        })}
      </div>
      {extras
        .filter(
          (protocol) =>
            form.protocols.includes(protocol) &&
            !sharedProtocolCap(server, protocol),
        )
        .map((protocol) => (
          <p
            key={protocol}
            className="rounded-lg bg-warning-50 p-2 text-xs text-warning"
          >
            妙妙屋端暂不支持 {landingProtocolLabel(protocol)}
            （该服务器 Helper
            需升级）：已有节点暂停下发，端口和凭据保留，恢复后自动重新下发。
          </p>
        ))}
      {form.protocols.includes("anytls") && (
        <Input
          description="留空时使用公网地址（域名才发送 SNI）；证书由面板自签，客户端用证书指纹校验"
          label="AnyTLS SNI（可留空）"
          value={form.anytlsServerName || ""}
          variant="bordered"
          onChange={(event) =>
            set("anytlsServerName", event.target.value.trim())
          }
        />
      )}
      {form.protocols.includes("snell") && (
        <div className="grid grid-cols-2 gap-3">
          <Select
            label="Snell 版本"
            selectedKeys={[String(form.snellVersion || 4)]}
            variant="bordered"
            onSelectionChange={(keys) =>
              set("snellVersion", Number(Array.from(keys)[0] || 4))
            }
          >
            {versions.map((version) => (
              <SelectItem key={String(version)}>{`v${version}`}</SelectItem>
            ))}
          </Select>
          <Select
            label="Snell 混淆"
            selectedKeys={[obfs]}
            variant="bordered"
            onSelectionChange={(keys) =>
              set("snellObfs", String(Array.from(keys)[0] || "none"))
            }
          >
            {obfsModes.map((mode) => (
              <SelectItem key={mode}>
                {mode === "none" ? "无" : mode.toUpperCase()}
              </SelectItem>
            ))}
          </Select>
        </div>
      )}
      {form.protocols.includes("snell") && obfs !== "none" && (
        <Input
          label="Snell 混淆域名"
          placeholder="例如 www.bing.com"
          value={form.snellObfsHost || ""}
          variant="bordered"
          onChange={(event) => set("snellObfsHost", event.target.value.trim())}
        />
      )}
      {form.protocols.includes("mieru") && (
        <Select
          description="服务端同一端口同时支持 TCP 与 UDP；这里决定客户端配置默认用哪种"
          label="Mieru 客户端传输"
          selectedKeys={[form.mieruTransport || "TCP"]}
          variant="bordered"
          onSelectionChange={(keys) =>
            set("mieruTransport", String(Array.from(keys)[0] || "TCP"))
          }
        >
          {transports.map((transport) => (
            <SelectItem key={transport}>{transport}</SelectItem>
          ))}
        </Select>
      )}
      <p className="text-xs text-default-500">
        以上参数只用于之后新建的节点；已有节点保持创建时的配置。
      </p>
    </div>
  );
}
