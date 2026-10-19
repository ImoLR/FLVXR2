import { SharedCaps } from "./shared-caps";

import { useEffect, useState } from "react";
import toast from "react-hot-toast";

import { getUserGroupList } from "@/api";
import { LandingServer, saveLandingServer } from "@/api/landing";
import { UserGroupApiItem } from "@/api/types";
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

export const dateInput = (value?: number | null) =>
  value
    ? new Date(value - new Date(value).getTimezoneOffset() * 60000)
        .toISOString()
        .slice(0, 16)
    : "";
export const dateValue = (value: string) =>
  value ? new Date(value).getTime() : null;

export function ServerSettings({
  server,
  onClose,
  onSaved,
}: {
  server: LandingServer;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [form, setForm] = useState(server);
  const [groups, setGroups] = useState<UserGroupApiItem[]>([]);
  const [saving, setSaving] = useState(false);
  const [realityNamesInput, setRealityNamesInput] = useState(
    (server.realityServerNames || []).join(","),
  );
  const set = <K extends keyof LandingServer>(
    key: K,
    value: LandingServer[K],
  ) => setForm((prev) => ({ ...prev, [key]: value }));

  useEffect(() => {
    void getUserGroupList().then((res) => {
      if (res.code === 0) setGroups(res.data || []);
      else toast.error(res.msg);
    });
  }, []);
  const numberField = (
    label: string,
    key: keyof LandingServer,
    min = 0,
    step = "1",
    scale = 1,
  ) => (
    <Input
      label={label}
      min={String(min)}
      step={step}
      type="number"
      value={String(Number(form[key] ?? 0) / scale)}
      variant="bordered"
      onChange={(event) =>
        set(key, (Number(event.target.value) * scale) as never)
      }
    />
  );

  const save = async () => {
    if (
      !form.name.trim() ||
      !form.publicHost.trim() ||
      form.portMin < 1024 ||
      form.portMax > 65535 ||
      form.portMin > form.portMax ||
      !form.protocols.length ||
      form.trafficRatio <= 0 ||
      form.coreMemoryMb < 64 ||
      form.coreCpuPercent <= 0
    ) {
      toast.error("请检查名称、地址、端口段、协议、倍率和资源上限");
      return;
    }
    setSaving(true);
    const response = await saveLandingServer({
      ...form,
      realityServerNames: realityNamesInput
        .split(",")
        .map((name) => name.trim())
        .filter(Boolean),
    });
    setSaving(false);
    if (response.code !== 0) {
      toast.error(response.msg || "保存失败");
      return;
    }
    toast.success("落地服务器设置已保存");
    onSaved();
    onClose();
  };

  return (
    <Modal
      isOpen
      classNames={{
        base: "sm:left-auto sm:right-0 sm:translate-x-0 sm:rounded-r-none",
      }}
      scrollBehavior="inside"
      size="lg"
      onClose={onClose}
    >
      <ModalContent className="flvx-landing-text">
        <ModalHeader>落地服务器设置 · {server.name}</ModalHeader>
        <ModalBody className="space-y-6">
          {server.source === "shared" && <SharedCaps server={server} />}
          <section className="space-y-3">
            <h3 className="font-medium">基本设置</h3>
            <Input
              label="名称"
              value={form.name}
              variant="bordered"
              onChange={(event) => set("name", event.target.value)}
            />
            <Input
              description="用于直连链接，不含协议和端口"
              label="公网地址"
              value={form.publicHost}
              variant="bordered"
              onChange={(event) => set("publicHost", event.target.value)}
            />
            <div className="grid grid-cols-2 gap-3">
              {numberField("起始端口", "portMin", 1024)}
              {numberField("结束端口", "portMax", 1024)}
            </div>
            <p className="text-xs text-default-500">
              端口由系统自动分配，不能与规则端口段或现有监听冲突。
            </p>
            <Select
              label="状态"
              selectedKeys={[form.status]}
              onSelectionChange={(keys) =>
                set("status", String(Array.from(keys)[0]))
              }
            >
              <SelectItem key="active">启用</SelectItem>
              <SelectItem key="disabled">停用</SelectItem>
            </Select>
          </section>
          <section className="space-y-3">
            <h3 className="font-medium">计费与限制</h3>
            <div className="grid grid-cols-2 gap-3">
              <Select
                label="流量计算"
                selectedKeys={[String(form.flowType)]}
                onSelectionChange={(keys) =>
                  set("flowType", Number(Array.from(keys)[0]))
                }
              >
                <SelectItem key="1">单向</SelectItem>
                <SelectItem key="2">双向</SelectItem>
              </Select>
              {numberField("流量倍率", "trafficRatio", 0.01, "0.01")}
              {server.source !== "shared" &&
                numberField("服务器总连接数（0 不限）", "maxConnections")}
              {numberField("每人在本服务器最多节点数", "maxNodesPerUser", 1)}
            </div>
            <p className="text-xs text-default-500">
              落地计入用户同一个总流量。通过本人规则访问时按隧道与落地较贵的费率计费。
            </p>
          </section>
          <section className="space-y-3">
            <h3 className="font-medium">新节点默认值</h3>
            {server.source === "shared" &&
              numberField(
                "每个用户默认服务器限额 (GB)",
                "defaultUserQuotaBytes",
                0,
                "0.01",
                1073741824,
              )}
            <div className="grid grid-cols-2 gap-3">
              {numberField("节点连接数（0 不限）", "defaultMaxConnections")}
              {numberField("在线 IP 数（0 不限）", "defaultMaxOnlineIps")}
              {numberField(
                "流量上限 (GB，0 不限)",
                "defaultTrafficLimitBytes",
                0,
                "0.01",
                1073741824,
              )}
            </div>
            <Select
              label="默认有效期"
              selectedKeys={[form.defaultExpiryMode]}
              onSelectionChange={(keys) =>
                set("defaultExpiryMode", String(Array.from(keys)[0]))
              }
            >
              <SelectItem key="follow_user">跟随用户</SelectItem>
              <SelectItem key="days">创建后 N 天</SelectItem>
              <SelectItem key="fixed">固定日期</SelectItem>
              <SelectItem key="permanent">
                永久（仍受用户及服务器期限限制）
              </SelectItem>
            </Select>
            {form.defaultExpiryMode === "days" &&
              numberField("有效天数", "defaultExpiryDays", 1)}
            {form.defaultExpiryMode === "fixed" && (
              <Input
                label="默认到期日期"
                type="datetime-local"
                value={dateInput(form.defaultExpiresAt)}
                onChange={(event) =>
                  set("defaultExpiresAt", dateValue(event.target.value))
                }
              />
            )}
          </section>
          <section className="space-y-3">
            <h3 className="font-medium">协议模板</h3>
            <div className="flex flex-wrap gap-4">
              {(["ss2022", "vless_reality_vision"] as const).map((protocol) => (
                <Checkbox
                  key={protocol}
                  isSelected={form.protocols.includes(protocol)}
                  isDisabled={Boolean(
                    server.sharedCaps &&
                      !server.sharedCaps.protocols.includes(protocol),
                  )}
                  onValueChange={(checked) =>
                    set(
                      "protocols",
                      checked
                        ? [...form.protocols, protocol]
                        : form.protocols.filter((item) => item !== protocol),
                    )
                  }
                >
                  {protocol === "ss2022" ? "SS2022" : "VLESS Reality Vision"}
                </Checkbox>
              ))}
            </div>
            {form.protocols.includes("vless_reality_vision") && (
              <>
                <Input
                  label="Reality 目标 (host:port)"
                  value={form.realityDest}
                  variant="bordered"
                  onChange={(event) => set("realityDest", event.target.value)}
                />
                <Input
                  label="Reality Server Names"
                  description="多个域名用逗号分隔"
                  value={realityNamesInput}
                  variant="bordered"
                  onChange={(event) => setRealityNamesInput(event.target.value)}
                />
              </>
            )}
          </section>
          <section className="space-y-3">
            <h3 className="font-medium">可见用户组</h3>
            <p className="text-xs text-default-500">
              只有选中用户组的成员才能查看此服务器和创建节点。
            </p>
            <p className="text-xs text-default-500">
              不选任何用户组时，仅管理员和直接授权的用户可见（在「用户」编辑弹窗的「落地
              → 可用落地服务器」中授权
              {form.directUserCount
                ? `，当前直接授权 ${form.directUserCount} 人`
                : ""}
              ）。
            </p>
            <div className="flex flex-wrap gap-4">
              {groups.map((group) => (
                <Checkbox
                  key={group.id}
                  isSelected={(form.userGroupIds || []).includes(group.id)}
                  onValueChange={(checked) =>
                    set(
                      "userGroupIds",
                      checked
                        ? [...(form.userGroupIds || []), group.id]
                        : (form.userGroupIds || []).filter(
                            (id) => id !== group.id,
                          ),
                    )
                  }
                >
                  {group.name}
                </Checkbox>
              ))}
            </div>
            {groups.length === 0 && (
              <p className="text-sm text-default-500">
                暂无用户组，请先创建用户组。
              </p>
            )}
          </section>
          <section className="space-y-3">
            <h3 className="font-medium">服务器期限与资源</h3>
            <Input
              description="留空表示不单独限制；到期的节点停用，保留 7 天"
              label="服务器可用期限"
              type="datetime-local"
              value={dateInput(form.availableUntil)}
              onChange={(event) =>
                set("availableUntil", dateValue(event.target.value))
              }
            />
            {server.source !== "shared" && (
              <div className="grid grid-cols-2 gap-3">
                {numberField("内存上限 (MiB)", "coreMemoryMb", 64)}
                {numberField("CPU 上限 (%)", "coreCpuPercent", 1)}
              </div>
            )}
          </section>
        </ModalBody>
        <ModalFooter>
          <Button variant="light" onPress={onClose}>
            取消
          </Button>
          <Button color="primary" isLoading={saving} onPress={save}>
            保存设置
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  );
}
