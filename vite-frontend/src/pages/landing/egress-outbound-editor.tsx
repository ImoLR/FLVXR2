import { useState } from "react";
import toast from "react-hot-toast";

import {
  EgressIPVersion,
  EgressOutbound,
  EgressOutboundType,
  EgressOverview,
  EgressProxyProtocol,
  EgressSettings,
  ipVersionOptions,
  ipVersionScope,
  outboundTypeLabel,
  parseEgressLink,
  proxyProtocolLabel,
  saveEgressOutbound,
} from "@/api/landing-egress";
import { landingProtocolLabel } from "@/api/landing";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Checkbox } from "@/shadcn-bridge/heroui/checkbox";
import { Input } from "@/shadcn-bridge/heroui/input";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";

const fingerprints = ["chrome", "firefox", "safari", "ios", "android", "edge"];

export function OutboundEditor({
  outbound,
  overview,
  onClose,
  onSaved,
}: {
  outbound: Partial<EgressOutbound> | null;
  overview: EgressOverview;
  onClose: () => void;
  onSaved: () => void;
}) {
  const editing = Boolean(outbound?.id);
  const [form, setForm] = useState<Partial<EgressOutbound>>({
    type: "warp",
    ipVersion: "system",
    ...outbound,
    settings: {
      warpMode: "auto",
      protocol: "socks",
      method: "2022-blake3-aes-128-gcm",
      realityFingerprint: "chrome",
      flow: "xtls-rprx-vision",
      ...(outbound?.settings || {}),
    },
  });
  const [link, setLink] = useState("");
  const [saving, setSaving] = useState(false);
  const [importing, setImporting] = useState(false);
  const type = (form.type || "warp") as EgressOutboundType;
  const s = form.settings || {};
  const set = (patch: Partial<EgressOutbound>) =>
    setForm((prev) => ({ ...prev, ...patch }));
  const setS = (patch: Partial<EgressSettings>) =>
    setForm((prev) => ({ ...prev, settings: { ...prev.settings, ...patch } }));
  const option = ipVersionOptions.find((o) => o.key === form.ipVersion);
  const upstream = overview.landingNodes.find((n) => n.id === s.landingNodeId);
  const forwards = overview.forwards.filter(
    (f) => f.landingNodeId === s.landingNodeId,
  );

  const importLink = async () => {
    if (!link.trim()) return;
    setImporting(true);
    const res = await parseEgressLink(link.trim());

    setImporting(false);
    if (res.code !== 0) {
      toast.error(res.msg);

      return;
    }
    setForm((prev) => ({
      ...prev,
      name: prev.name?.trim() ? prev.name : res.data.name,
      settings: { ...prev.settings, ...res.data.settings },
    }));
    setLink("");
    toast.success("已从链接填入");
  };

  const save = async () => {
    setSaving(true);
    const settings: EgressSettings = { ...s };

    if (type === "warp" && typeof settings.reserved === "string") {
      settings.reserved = String(settings.reserved)
        .split(/[,，\s]+/)
        .filter(Boolean)
        .map(Number);
    }
    const res = await saveEgressOutbound({ ...form, settings });

    setSaving(false);
    if (res.code !== 0) {
      toast.error(res.msg || "保存失败");

      return;
    }
    toast.success("出站已保存");
    (res.data?.warnings || []).forEach((w) => toast(w));
    onSaved();
    onClose();
  };

  const field = (
    label: string,
    key: keyof EgressSettings,
    props: Record<string, unknown> = {},
  ) => (
    <Input
      label={label}
      value={s[key] == null ? "" : String(s[key])}
      variant="bordered"
      onChange={(event) =>
        setS({
          [key]:
            props.type === "number"
              ? Number(event.target.value)
              : event.target.value,
        } as Partial<EgressSettings>)
      }
      {...props}
    />
  );

  return (
    <Modal isOpen scrollBehavior="inside" size="lg" onClose={onClose}>
      <ModalContent className="flvx-landing-text">
        <ModalHeader>
          {editing ? `编辑出站 · ${outbound?.name}` : "新建出站"}
        </ModalHeader>
        <ModalBody className="space-y-5">
          <section className="space-y-3">
            <Select
              description={editing ? "类型创建后不可修改" : undefined}
              isDisabled={editing}
              label="类型"
              selectedKeys={[type]}
              variant="bordered"
              onSelectionChange={(keys) =>
                set({ type: String(Array.from(keys)[0]) as EgressOutboundType })
              }
            >
              {(Object.keys(outboundTypeLabel) as EgressOutboundType[]).map(
                (key) => (
                  <SelectItem key={key}>{outboundTypeLabel[key]}</SelectItem>
                ),
              )}
            </Select>
            <Input
              label="名称"
              placeholder="例如：WARP、香港代理"
              value={form.name || ""}
              variant="bordered"
              onChange={(event) => set({ name: event.target.value })}
            />
            <Select
              label="IP 版本"
              selectedKeys={[form.ipVersion || "system"]}
              variant="bordered"
              onSelectionChange={(keys) =>
                set({
                  ipVersion: String(Array.from(keys)[0]) as EgressIPVersion,
                })
              }
            >
              {ipVersionOptions.map((o) => (
                <SelectItem key={o.key}>{o.label}</SelectItem>
              ))}
            </Select>
            <div className="space-y-1 text-xs text-default-500">
              <p
                className={
                  form.ipVersion?.startsWith("force")
                    ? "text-warning"
                    : undefined
                }
              >
                {option?.hint}
              </p>
              <p>{ipVersionScope[type]}</p>
            </div>
          </section>

          {type === "direct" && (
            <p className="rounded-lg bg-default-100 p-3 text-sm text-default-600">
              从落地服务器直接访问目标。内置的「直连（默认）」等同于跟随系统的直连，无需新建；需要固定
              IPv4/IPv6 时再建一个直连出站。
            </p>
          )}

          {type === "warp" && (
            <section className="space-y-3">
              <h3 className="font-medium">WARP</h3>
              <Select
                label="注册方式"
                selectedKeys={[s.warpMode || "auto"]}
                variant="bordered"
                onSelectionChange={(keys) =>
                  setS({
                    warpMode: String(Array.from(keys)[0]) as "auto" | "manual",
                  })
                }
              >
                <SelectItem key="auto">自动注册（推荐）</SelectItem>
                <SelectItem key="manual">手动填写</SelectItem>
              </Select>
              {(s.warpMode || "auto") === "auto" ? (
                <>
                  <p className="text-xs text-default-500">
                    面板为每台使用它的落地服务器各自注册一个免费 WARP
                    账号（首次使用时自动注册，可在列表中重新注册）。注册失败时会显示原因，可改用手动填写。
                  </p>
                  {field("WARP+ 许可证（可选）", "warpLicense", {
                    placeholder: "xxxxxxxx-xxxxxxxx-xxxxxxxx",
                  })}
                </>
              ) : (
                <>
                  {field("私钥 PrivateKey", "privateKey")}
                  {field("对端公钥 PublicKey", "peerPublicKey", {
                    placeholder: "bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo=",
                  })}
                  {field("端点（留空自动选择）", "endpoint", {
                    placeholder: "162.159.192.1:2408",
                  })}
                  <div className="grid gap-3 sm:grid-cols-2">
                    {field("IPv4 地址", "addressV4", {
                      placeholder: "172.16.0.2",
                    })}
                    {field("IPv6 地址", "addressV6")}
                    <Input
                      description="3 个 0–255 的数字，逗号分隔"
                      label="reserved"
                      value={
                        Array.isArray(s.reserved)
                          ? s.reserved.join(",")
                          : String(s.reserved || "")
                      }
                      variant="bordered"
                      onChange={(event) =>
                        setS({
                          reserved: event.target.value as unknown as number[],
                        })
                      }
                    />
                    {field("MTU（留空 1280）", "mtu", {
                      type: "number",
                      min: "1280",
                      max: "1500",
                    })}
                  </div>
                </>
              )}
            </section>
          )}

          {type === "proxy" && (
            <section className="space-y-3">
              <h3 className="font-medium">代理</h3>
              <div className="flex flex-col gap-2 sm:flex-row sm:items-end">
                <Input
                  className="flex-1"
                  label="粘贴链接导入"
                  placeholder="ss://… 或 vless://…"
                  value={link}
                  variant="bordered"
                  onChange={(event) => setLink(event.target.value)}
                />
                <Button
                  color="secondary"
                  isLoading={importing}
                  variant="flat"
                  onPress={() => void importLink()}
                >
                  导入
                </Button>
              </div>
              <Select
                label="协议"
                selectedKeys={[s.protocol || "socks"]}
                variant="bordered"
                onSelectionChange={(keys) =>
                  setS({
                    protocol: String(
                      Array.from(keys)[0],
                    ) as EgressProxyProtocol,
                  })
                }
              >
                {(Object.keys(proxyProtocolLabel) as EgressProxyProtocol[]).map(
                  (key) => (
                    <SelectItem key={key}>{proxyProtocolLabel[key]}</SelectItem>
                  ),
                )}
              </Select>
              <div className="grid gap-3 sm:grid-cols-[1fr_8rem]">
                {field("服务器", "server", {
                  placeholder: "域名或公网 IP",
                })}
                {field("端口", "port", { type: "number", min: "1" })}
              </div>
              {(s.protocol === "socks" || s.protocol === "http") && (
                <div className="grid gap-3 sm:grid-cols-2">
                  {field("用户名（可选）", "username")}
                  {field("密码（可选）", "password", { type: "password" })}
                </div>
              )}
              {s.protocol === "shadowsocks" && (
                <>
                  <Select
                    label="加密方式"
                    selectedKeys={[s.method || "2022-blake3-aes-128-gcm"]}
                    variant="bordered"
                    onSelectionChange={(keys) =>
                      setS({ method: String(Array.from(keys)[0]) })
                    }
                  >
                    {overview.ssMethods.map((m) => (
                      <SelectItem key={m}>{m}</SelectItem>
                    ))}
                  </Select>
                  {field("密码", "password", {
                    description: s.method?.startsWith("2022")
                      ? "2022 加密填 Base64 密钥；多用户服务器填 服务器密钥:用户密钥"
                      : undefined,
                  })}
                </>
              )}
              {s.protocol === "vless" && (
                <>
                  {field("UUID", "uuid")}
                  <div className="grid gap-3 sm:grid-cols-2">
                    <Select
                      label="Flow"
                      selectedKeys={[s.flow || "none"]}
                      variant="bordered"
                      onSelectionChange={(keys) => {
                        const v = String(Array.from(keys)[0]);

                        setS({ flow: v === "none" ? "" : v });
                      }}
                    >
                      <SelectItem key="xtls-rprx-vision">
                        xtls-rprx-vision
                      </SelectItem>
                      <SelectItem key="none">无</SelectItem>
                    </Select>
                    <Select
                      label="指纹"
                      selectedKeys={[s.realityFingerprint || "chrome"]}
                      variant="bordered"
                      onSelectionChange={(keys) =>
                        setS({
                          realityFingerprint: String(Array.from(keys)[0]),
                        })
                      }
                    >
                      {fingerprints.map((f) => (
                        <SelectItem key={f}>{f}</SelectItem>
                      ))}
                    </Select>
                  </div>
                  {field("SNI（serverName）", "realityServerName")}
                  {field("公钥 pbk", "realityPublicKey")}
                  {field("shortId（可留空）", "realityShortId")}
                </>
              )}
              <p className="text-xs text-default-500">
                上游地址不能是内网、回环或云元数据地址；保存和下发时都会检查。
              </p>
            </section>
          )}

          {type === "landing_node" && (
            <section className="space-y-3">
              <h3 className="font-medium">FLVX 落地节点</h3>
              <Select
                isSearchable
                label="上游落地节点"
                placeholder="选择一个已有的落地节点"
                selectedKeys={s.landingNodeId ? [String(s.landingNodeId)] : []}
                variant="bordered"
                onSelectionChange={(keys) =>
                  setS({
                    landingNodeId: Number(Array.from(keys)[0]),
                    viaForwardId: 0,
                  })
                }
              >
                {overview.landingNodes.map((n) => (
                  <SelectItem
                    key={String(n.id)}
                    textValue={`${n.name} · ${n.serverName} · ${landingProtocolLabel(n.protocol)}${n.userName ? ` · ${n.userName}` : ""}`}
                  >
                    {n.name} · {n.serverName} ·{" "}
                    {landingProtocolLabel(n.protocol)}
                    {n.userName ? ` · ${n.userName}` : ""}
                  </SelectItem>
                ))}
              </Select>
              <Select
                description="选一条目标是该节点的规则，流量先进 FLVX 隧道再到上游节点"
                isDisabled={!s.landingNodeId}
                label="经由 FLVX 规则（可选）"
                selectedKeys={[String(s.viaForwardId || 0)]}
                variant="bordered"
                onSelectionChange={(keys) =>
                  setS({ viaForwardId: Number(Array.from(keys)[0]) })
                }
              >
                {[
                  <SelectItem key="0">不经规则，直接连接该节点</SelectItem>,
                  ...forwards.map((f) => (
                    <SelectItem key={String(f.id)}>
                      {f.name}
                      {f.entries[0] ? ` · 入口 ${f.entries[0]}` : ""}
                    </SelectItem>
                  )),
                ]}
              </Select>
              <p className="text-xs text-default-500">
                凭据自动取自该节点{upstream ? `（${upstream.name}）` : ""}
                。经它的流量计入该节点所属用户；不能使用本服务器上的节点作为上游。
              </p>
            </section>
          )}

          <Input
            label="备注（可选）"
            value={form.remark || ""}
            variant="bordered"
            onChange={(event) => set({ remark: event.target.value })}
          />
          <div className="space-y-1">
            <Checkbox
              isSelected={Boolean(form.userAllowed)}
              onValueChange={(userAllowed) => set({ userAllowed })}
            >
              允许用户使用
            </Checkbox>
            <p className="text-xs text-default-500">
              勾选后，用户可在自己节点的「路由规则」里选这个出站（规则出站或节点默认出站）。用户只看到名称，看不到地址和凭据；不勾选时用户只能选直连或封锁。
            </p>
          </div>
        </ModalBody>
        <ModalFooter>
          <Button color="danger" variant="flat" onPress={onClose}>
            取消
          </Button>
          <Button
            color="primary"
            isDisabled={!form.name?.trim()}
            isLoading={saving}
            onPress={() => void save()}
          >
            保存出站
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  );
}
